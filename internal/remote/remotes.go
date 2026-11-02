package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

const remotesFile = "remotes.json"

// ErrRemoteNotFound is returned when a named remote is not configured.
var ErrRemoteNotFound = errors.New("remote not found")

// Remote is a paired server as saved on the controlling machine.
type Remote struct {
	Name        string `json:"-"`
	Address     string `json:"address"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
}

// Validate checks every field of the remote.
func (r Remote) Validate() error {
	if !profile.ValidName(r.Name) {
		return fmt.Errorf("invalid remote name %q", r.Name)
	}
	if err := ValidAddress(r.Address); err != nil {
		return err
	}
	if !validToken(r.Token) {
		return errors.New("invalid token: want the 64 hex chars printed by `envmgr serve`")
	}
	if _, err := NormalizeFingerprint(r.Fingerprint); err != nil {
		return err
	}
	return nil
}

// ValidAddress checks that a is host:port with a numeric port.
func ValidAddress(a string) error {
	host, port, err := net.SplitHostPort(a)
	if err != nil || host == "" {
		return fmt.Errorf("invalid address %q: want host:port", a)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid port in %q", a)
	}
	return nil
}

// Remotes is the set of paired servers stored in <dir>/remotes.json (0600).
type Remotes struct {
	path    string
	entries map[string]Remote
}

type remotesJSON struct {
	Remotes map[string]Remote `json:"remotes"`
}

// LoadRemotes reads <dir>/remotes.json; a missing file yields an empty set.
func LoadRemotes(dir string) (*Remotes, error) {
	rs := &Remotes{path: filepath.Join(dir, remotesFile), entries: map[string]Remote{}}
	b, err := os.ReadFile(rs.path)
	if errors.Is(err, os.ErrNotExist) {
		return rs, nil
	}
	if err != nil {
		return nil, err
	}
	var doc remotesJSON
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", rs.path, err)
	}
	for n, r := range doc.Remotes {
		r.Name = n
		rs.entries[n] = r
	}
	return rs, nil
}

// Get returns a remote by name.
func (rs *Remotes) Get(name string) (Remote, error) {
	r, ok := rs.entries[name]
	if !ok {
		return Remote{}, fmt.Errorf("%w: %q", ErrRemoteNotFound, name)
	}
	return r, nil
}

// Names returns the configured remote names, sorted.
func (rs *Remotes) Names() []string {
	out := make([]string, 0, len(rs.entries))
	for n := range rs.entries {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Add validates and stores r (fingerprint normalized). It does not save.
func (rs *Remotes) Add(r Remote) error {
	r.Token = strings.TrimSpace(r.Token)
	if err := r.Validate(); err != nil {
		return err
	}
	r.Fingerprint, _ = NormalizeFingerprint(r.Fingerprint)
	rs.entries[r.Name] = r
	return nil
}

// Remove deletes a remote by name. It does not save.
func (rs *Remotes) Remove(name string) error {
	if _, ok := rs.entries[name]; !ok {
		return fmt.Errorf("%w: %q", ErrRemoteNotFound, name)
	}
	delete(rs.entries, name)
	return nil
}

// Save writes the file atomically with mode 0600.
func (rs *Remotes) Save() error {
	if err := os.MkdirAll(filepath.Dir(rs.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(remotesJSON{Remotes: rs.entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp := rs.path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, rs.path)
}

// Client builds a pinned client for r.
func (r Remote) Client() (*Client, error) {
	return NewClient(r.Address, r.Token, r.Fingerprint)
}

// MaskedValue is shown instead of a secret value. It does not reveal length.
const MaskedValue = "********"

// WriteProfile prints a profile as sorted KEY=value lines; values are masked
// unless reveal is true.
func WriteProfile(w io.Writer, p profile.Profile, reveal bool) error {
	keys := make([]string, 0, len(p.Vars))
	for k := range p.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := MaskedValue
		if reveal {
			v = p.Vars[k]
		}
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, v); err != nil {
			return err
		}
	}
	return nil
}
