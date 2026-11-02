package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

const bindingFile = "parent.json"

var (
	// ErrNotBound means this machine has not joined a parent.
	ErrNotBound = errors.New("this machine is not connected to a parent (run `envmgr join <code>`)")
	// ErrAlreadyBound means this machine already has a parent.
	ErrAlreadyBound = errors.New("this machine is already bound to a parent (use --force to replace it, or `envmgr agent leave`)")
	// ErrRevoked means the parent rejected this child's credential.
	ErrRevoked = errors.New("the parent revoked this machine's access (run `envmgr join` with a new code to reconnect)")
	// ErrInviteExpired means the join code's expiry has passed.
	ErrInviteExpired = errors.New("join code has expired; ask the parent for a new one")
)

// JoinCode is what a parent hands to a child: base64url(JSON).
type JoinCode struct {
	Addr   string `json:"addr"`
	FP     string `json:"fp"`
	Secret string `json:"secret"`
	Name   string `json:"name,omitempty"`
	Exp    int64  `json:"exp"` // unix seconds
}

// Encode returns the code as unpadded base64url JSON.
func (c JoinCode) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeJoinCode parses and validates a join code.
func DecodeJoinCode(s string) (JoinCode, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) > 4096 {
		return JoinCode{}, errors.New("invalid join code")
	}
	var c JoinCode
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return JoinCode{}, errors.New("invalid join code")
	}
	if err := ValidAddress(c.Addr); err != nil {
		return JoinCode{}, fmt.Errorf("invalid join code: %w", err)
	}
	fp, err := NormalizeFingerprint(c.FP)
	if err != nil {
		return JoinCode{}, fmt.Errorf("invalid join code: %w", err)
	}
	c.FP = fp
	if !validSecret(c.Secret) {
		return JoinCode{}, errors.New("invalid join code: bad secret")
	}
	if c.Name != "" && !profile.ValidName(c.Name) {
		return JoinCode{}, errors.New("invalid join code: bad name")
	}
	return c, nil
}

// Binding is the child's single parent, saved in <agent dir>/parent.json (0600).
type Binding struct {
	Parent      string    `json:"parent"` // parent's label
	Addr        string    `json:"addr"`
	Fingerprint string    `json:"fingerprint"`
	Credential  string    `json:"credential"` // this child's own credential
	Name        string    `json:"name"`       // this child's agent name at the parent
	Role        Role      `json:"role"`
	Caps        []string  `json:"caps,omitempty"` // extra grants from the parent, e.g. CapHosts
	EnrolledAt  time.Time `json:"enrolledAt"`
}

// LoadBinding reads parent.json from dir.
func LoadBinding(dir string) (*Binding, error) {
	b, err := os.ReadFile(filepath.Join(dir, bindingFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotBound
	}
	if err != nil {
		return nil, err
	}
	var bd Binding
	if err := json.Unmarshal(b, &bd); err != nil {
		return nil, fmt.Errorf("%s: %w", bindingFile, err)
	}
	if ValidAddress(bd.Addr) != nil || !validSecret(bd.Credential) || !bd.Role.Valid() || !ValidCaps(bd.Caps) {
		return nil, fmt.Errorf("%s is malformed; run `envmgr agent leave` and join again", bindingFile)
	}
	if bd.Fingerprint, err = NormalizeFingerprint(bd.Fingerprint); err != nil {
		return nil, err
	}
	return &bd, nil
}

// SaveBinding writes parent.json (0600, atomic).
func SaveBinding(dir string, b *Binding) error {
	return writeJSONFile(filepath.Join(dir, bindingFile), b)
}

// RemoveBinding deletes parent.json.
func RemoveBinding(dir string) error {
	err := os.Remove(filepath.Join(dir, bindingFile))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotBound
	}
	return err
}

// Join enrolls this machine with the parent described by code and saves the
// binding in dir. The parent's certificate must match the code's fingerprint.
// A machine is bound to exactly one parent: an existing binding is an error
// unless force is set (then the old parent is notified best-effort).
func Join(ctx context.Context, dir, code, name string, force bool) (*Binding, error) {
	jc, err := DecodeJoinCode(code)
	if err != nil {
		return nil, err
	}
	if time.Now().Unix() >= jc.Exp {
		return nil, ErrInviteExpired
	}
	if name != "" && !profile.ValidName(name) {
		return nil, fmt.Errorf("invalid name %q", name)
	}
	old, err := LoadBinding(dir)
	switch {
	case err == nil && !force:
		return nil, ErrAlreadyBound
	case err != nil && !errors.Is(err, ErrNotBound) && !force:
		return nil, err
	}
	c, err := pinnedClient(jc.FP, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var resp EnrollResponse
	_, err = doJSON(ctx, c, http.MethodPost, "https://"+jc.Addr+"/v1/enroll", "", enrollRequest{Secret: jc.Secret, Name: name}, &resp)
	if err != nil {
		if StatusOf(err) == http.StatusUnauthorized {
			return nil, errors.New("the parent rejected the join code (already used, expired or cancelled)")
		}
		return nil, err
	}
	if !validSecret(resp.Credential) || !profile.ValidName(resp.Name) || !resp.Role.Valid() || !ValidCaps(resp.Caps) {
		return nil, errors.New("invalid enrollment response from parent")
	}
	if old != nil {
		if a, err := NewAgent(old, nil, nil); err == nil {
			_ = a.Leave(ctx)
		}
	}
	b := &Binding{Parent: resp.Parent, Addr: jc.Addr, Fingerprint: jc.FP, Credential: resp.Credential, Name: resp.Name, Role: resp.Role, Caps: resp.Caps, EnrolledAt: time.Now().UTC()}
	if err := SaveBinding(dir, b); err != nil {
		return nil, err
	}
	return b, nil
}

// Agent is the child-side loop: it long-polls its parent and executes the
// allow-listed jobs against the local store.
type Agent struct {
	b     *Binding
	store *profile.Store
	log   *log.Logger
	http  *http.Client
	// OnState, if set, is called with "connected" / "disconnected: <err>".
	OnState func(string)
	// MaxBackoff caps the reconnect delay (default 60s).
	MaxBackoff time.Duration
	// HostsFile is this machine's hosts file for granted hosts.* jobs
	// (default: hosts.ResolvePath, i.e. $ENVMGR_HOSTS_FILE or the system file).
	// It is never taken from the network.
	HostsFile string
}

// NewAgent returns the loop for binding b.
func NewAgent(b *Binding, store *profile.Store, logger *log.Logger) (*Agent, error) {
	if logger == nil {
		logger = discardLogger()
	}
	c, err := pinnedClient(b.Fingerprint, 60*time.Second)
	if err != nil {
		return nil, err
	}
	hostsFile, _ := hosts.ResolvePath("")
	return &Agent{b: b, store: store, log: logger, http: c, MaxBackoff: 60 * time.Second, HostsFile: hostsFile}, nil
}

func (a *Agent) url(path string) string { return "https://" + a.b.Addr + path }

// Ping checks the credential with the parent and returns what it granted.
func (a *Agent) Ping(ctx context.Context) (AgentInfo, error) {
	var info AgentInfo
	_, err := doJSON(ctx, a.http, http.MethodGet, a.url("/v1/agent/ping"), a.b.Credential, nil, &info)
	if StatusOf(err) == http.StatusUnauthorized {
		return info, ErrRevoked
	}
	return info, err
}

// Leave tells the parent to forget this child (best effort by callers).
func (a *Agent) Leave(ctx context.Context) error {
	_, err := doJSON(ctx, a.http, http.MethodPost, a.url("/v1/agent/leave"), a.b.Credential, nil, nil)
	return err
}

// Run polls until ctx is cancelled (returns nil) or the parent revokes this
// child (returns ErrRevoked). Network errors reconnect with capped
// exponential backoff.
func (a *Agent) Run(ctx context.Context) error {
	if a.store == nil {
		return errors.New("remote: agent has no store")
	}
	backoff := time.Second
	connected := false
	state := func(s string) {
		if a.OnState != nil {
			a.OnState(s)
		}
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		var err error
		if !connected {
			// Confirm the credential before the first long-poll so the user
			// sees "connected" (or "revoked") right away.
			if _, err = a.Ping(ctx); err == nil {
				connected = true
				backoff = time.Second
				state("connected")
			}
		}
		if err == nil {
			if err = a.once(ctx); err == nil {
				continue
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrRevoked) {
			return ErrRevoked
		}
		if errors.Is(err, ErrFingerprintMismatch) {
			return err // never talk to a different certificate
		}
		connected = false
		state("disconnected: " + err.Error())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > a.MaxBackoff {
			backoff = a.MaxBackoff
		}
	}
}

// once performs one poll and, if a job arrived, executes it and posts the result.
func (a *Agent) once(ctx context.Context) error {
	var job Job
	status, err := doJSON(ctx, a.http, http.MethodGet, a.url("/v1/agent/poll"), a.b.Credential, nil, &job)
	if StatusOf(err) == http.StatusUnauthorized {
		return ErrRevoked
	}
	if err != nil {
		return err
	}
	if status == http.StatusNoContent {
		return nil
	}
	res := ExecuteJob(Target{Store: a.store, Role: a.b.Role, Caps: a.b.Caps, HostsFile: a.HostsFile}, job)
	if job.IsHosts() {
		// Audit trail for hosts edits (security sensitive).
		a.log.Printf("parent=%q op=%s profile=%q group=%q hostnames=%v dryRun=%v ok=%v%s", a.b.Parent, job.Op, job.Args.Profile, job.Args.Group, job.Args.Hostnames, job.Args.DryRun, res.OK, errSuffix(res))
	} else {
		a.log.Printf("parent op=%s profile=%q ok=%v", job.Op, job.Args.Profile, res.OK)
	}
	if !validJobID(job.ID) {
		return nil
	}
	_, err = doJSON(ctx, a.http, http.MethodPost, a.url("/v1/agent/result/"+job.ID), a.b.Credential, res, nil)
	if StatusOf(err) == http.StatusUnauthorized {
		return ErrRevoked
	}
	if StatusOf(err) == http.StatusNotFound {
		return nil // parent stopped waiting
	}
	return err
}

func errSuffix(res Result) string {
	if res.OK || res.Error == "" {
		return ""
	}
	return fmt.Sprintf(" error=%q", res.Error)
}

func validJobID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
