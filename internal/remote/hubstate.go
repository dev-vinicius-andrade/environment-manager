package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

const (
	agentsFile  = "agents.json"
	invitesFile = "invites.json"
	seenFile    = "agents-seen.json" // written only by the running hub
	lockFile    = "state.lock"

	lockStaleAfter = 30 * time.Second
	lockWait       = 10 * time.Second
)

var (
	// ErrUnauthorized is any failed enrollment or agent authentication. It
	// deliberately carries no detail (unknown, used, expired: all the same).
	ErrUnauthorized = errors.New("unauthorized")
	// ErrNameTaken means the agent name is already enrolled.
	ErrNameTaken = errors.New("agent name already in use")
	// ErrAgentNotFound is returned for an unknown agent name.
	ErrAgentNotFound = errors.New("agent not found")
	// ErrInviteNotFound is returned for an unknown pending invite name.
	ErrInviteNotFound = errors.New("invite not found")
)

// Invite is a pending, single-use enrollment secret. Only its hash is stored.
type Invite struct {
	Name       string    `json:"name"`
	NameFixed  bool      `json:"nameFixed"` // parent chose the name; the child cannot change it
	SecretHash string    `json:"secretHash"`
	Role       Role      `json:"role"`
	Caps       []string  `json:"caps,omitempty"` // extra grants, e.g. CapHosts
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// AgentRecord is an enrolled child. Only the hash of its credential is stored.
type AgentRecord struct {
	Name           string    `json:"name"`
	CredentialHash string    `json:"credentialHash"`
	Role           Role      `json:"role"`
	Caps           []string  `json:"caps,omitempty"` // copied from the invite; change = revoke + re-invite
	EnrolledAt     time.Time `json:"enrolledAt"`
}

// HubState is the hub's on-disk state under dir (agents.json, invites.json,
// 0600, atomic writes). Every call re-reads the files and every change is made
// under a cross-process lock file, so the `envmgr parent` CLI and a running
// hub see each other's changes immediately.
type HubState struct {
	dir string
	now func() time.Time
}

// NewHubState returns the state stored in dir.
func NewHubState(dir string) *HubState { return &HubState{dir: dir, now: time.Now} }

// Dir returns the state directory.
func (h *HubState) Dir() string { return h.dir }

func (h *HubState) withLock(fn func() error) error {
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(h.dir, lockFile)
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if st, serr := os.Stat(p); serr == nil && time.Since(st.ModTime()) > lockStaleAfter {
			_ = os.Remove(p) // holder died
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("hub state is locked (%s); remove it if no envmgr parent command is running", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.Remove(p)
	return fn()
}

func readJSONList[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []T{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []T
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if out == nil {
		out = []T{}
	}
	return out, nil
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}

func (h *HubState) readAgents() ([]AgentRecord, error) {
	return readJSONList[AgentRecord](filepath.Join(h.dir, agentsFile))
}

func (h *HubState) readInvites() ([]Invite, error) {
	return readJSONList[Invite](filepath.Join(h.dir, invitesFile))
}

func (h *HubState) writeAgents(list []AgentRecord) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return writeJSONFile(filepath.Join(h.dir, agentsFile), list)
}

func (h *HubState) writeInvites(list []Invite) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return writeJSONFile(filepath.Join(h.dir, invitesFile), list)
}

func (h *HubState) pending(list []Invite) []Invite {
	now := h.now()
	out := make([]Invite, 0, len(list))
	for _, inv := range list {
		if now.Before(inv.ExpiresAt) {
			out = append(out, inv)
		}
	}
	return out
}

// Agents returns the enrolled agents, sorted by name.
func (h *HubState) Agents() ([]AgentRecord, error) {
	list, err := h.readAgents()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, err
}

// Agent returns one enrolled agent.
func (h *HubState) Agent(name string) (AgentRecord, error) {
	list, err := h.readAgents()
	if err != nil {
		return AgentRecord{}, err
	}
	for _, a := range list {
		if a.Name == name {
			return a, nil
		}
	}
	return AgentRecord{}, fmt.Errorf("%w: %q", ErrAgentNotFound, name)
}

// Invites returns the pending (unexpired) invites, sorted by name.
func (h *HubState) Invites() ([]Invite, error) {
	list, err := h.readInvites()
	if err != nil {
		return nil, err
	}
	return h.pending(list), nil
}

// CreateInvite stores a new single-use invite and returns its secret (shown
// once, never stored). An empty name gets a generated one the child may
// replace with its own proposal. caps are extra grants (only CapHosts exists).
func (h *HubState) CreateInvite(name string, role Role, ttl time.Duration, caps ...string) (string, Invite, error) {
	if ttl <= 0 {
		return "", Invite{}, errors.New("invite expiry must be positive")
	}
	if !role.Valid() {
		return "", Invite{}, fmt.Errorf("invalid role %q", role)
	}
	if !ValidCaps(caps) {
		return "", Invite{}, fmt.Errorf("invalid capabilities %q", caps)
	}
	fixed := name != ""
	if !fixed {
		name = "agent-" + randomID(3)
	}
	if !profile.ValidName(name) {
		return "", Invite{}, fmt.Errorf("invalid agent name %q", name)
	}
	secret, err := generateSecret()
	if err != nil {
		return "", Invite{}, err
	}
	now := h.now().UTC()
	inv := Invite{Name: name, NameFixed: fixed, SecretHash: HashSecret(secret), Role: role, Caps: caps, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	err = h.withLock(func() error {
		agents, err := h.readAgents()
		if err != nil {
			return err
		}
		for _, a := range agents {
			if a.Name == name {
				return fmt.Errorf("%w: %q (revoke it first)", ErrNameTaken, name)
			}
		}
		invites, err := h.readInvites()
		if err != nil {
			return err
		}
		invites = h.pending(invites)
		for _, i := range invites {
			if i.Name == name {
				return fmt.Errorf("a pending invite named %q already exists (cancel it first)", name)
			}
		}
		return h.writeInvites(append(invites, inv))
	})
	if err != nil {
		return "", Invite{}, err
	}
	return secret, inv, nil
}

// CancelInvite removes a pending invite.
func (h *HubState) CancelInvite(name string) error {
	return h.withLock(func() error {
		invites, err := h.readInvites()
		if err != nil {
			return err
		}
		invites = h.pending(invites)
		out := invites[:0]
		found := false
		for _, i := range invites {
			if i.Name == name {
				found = true
				continue
			}
			out = append(out, i)
		}
		if !found {
			return fmt.Errorf("%w: %q", ErrInviteNotFound, name)
		}
		return h.writeInvites(out)
	})
}

// Revoke removes an enrolled agent; its credential stops working on its next
// request.
func (h *HubState) Revoke(name string) error {
	return h.withLock(func() error {
		agents, err := h.readAgents()
		if err != nil {
			return err
		}
		out := agents[:0]
		found := false
		for _, a := range agents {
			if a.Name == name {
				found = true
				continue
			}
			out = append(out, a)
		}
		if !found {
			return fmt.Errorf("%w: %q", ErrAgentNotFound, name)
		}
		return h.writeAgents(out)
	})
}

// Enroll consumes the invite matching secret (single use) and enrolls a new
// agent, returning its record and credential (shown once, never stored).
// proposedName is used only when the invite's name was not fixed by the
// parent. Unknown, used or expired secrets all return ErrUnauthorized.
func (h *HubState) Enroll(secret, proposedName string) (AgentRecord, string, error) {
	var rec AgentRecord
	var cred string
	err := h.withLock(func() error {
		invites, err := h.readInvites()
		if err != nil {
			return err
		}
		invites = h.pending(invites) // expired invites can never match
		hashes := make([]string, len(invites))
		for i, inv := range invites {
			hashes[i] = inv.SecretHash
		}
		idx := -1
		if validSecret(secret) {
			idx = ctLookup(hashes, secret)
		}
		if idx < 0 {
			return ErrUnauthorized
		}
		inv := invites[idx]
		name := inv.Name
		if !inv.NameFixed && proposedName != "" {
			if !profile.ValidName(proposedName) {
				return fmt.Errorf("invalid agent name %q", proposedName)
			}
			name = proposedName
		}
		agents, err := h.readAgents()
		if err != nil {
			return err
		}
		for _, a := range agents {
			if a.Name == name {
				return fmt.Errorf("%w: %q", ErrNameTaken, name)
			}
		}
		if cred, err = generateSecret(); err != nil {
			return err
		}
		rec = AgentRecord{Name: name, CredentialHash: HashSecret(cred), Role: inv.Role, Caps: inv.Caps, EnrolledAt: h.now().UTC()}
		// Consume the invite first: a crash in between leaves an unusable
		// invite rather than a reusable one.
		if err := h.writeInvites(append(invites[:idx:idx], invites[idx+1:]...)); err != nil {
			return err
		}
		return h.writeAgents(append(agents, rec))
	})
	if err != nil {
		return AgentRecord{}, "", err
	}
	return rec, cred, nil
}

// AuthenticateAgent resolves a credential to an enrolled agent (constant-time
// comparison against every entry). It re-reads agents.json on each call.
func (h *HubState) AuthenticateAgent(cred string) (AgentRecord, error) {
	if !validSecret(cred) {
		return AgentRecord{}, ErrUnauthorized
	}
	agents, err := h.readAgents()
	if err != nil {
		return AgentRecord{}, err
	}
	hashes := make([]string, len(agents))
	for i, a := range agents {
		hashes[i] = a.CredentialHash
	}
	idx := ctLookup(hashes, cred)
	if idx < 0 || !profile.ValidName(agents[idx].Name) || !agents[idx].Role.Valid() {
		return AgentRecord{}, ErrUnauthorized
	}
	return agents[idx], nil
}

// LastSeen returns the last time each agent contacted the hub, as recorded by
// the hub in agents-seen.json.
func (h *HubState) LastSeen() map[string]time.Time {
	m := map[string]time.Time{}
	if b, err := os.ReadFile(filepath.Join(h.dir, seenFile)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (h *HubState) writeSeen(m map[string]time.Time) error {
	return writeJSONFile(filepath.Join(h.dir, seenFile), m)
}
