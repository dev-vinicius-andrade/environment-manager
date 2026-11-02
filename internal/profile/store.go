// Package profile stores named profiles on disk. A profile holds environment
// variables, hosts entries, or both; each part is applied separately (see
// .claude/docs/profiles.md).
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	varRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

	ErrNotFound = errors.New("profile not found")
)

// Profile is a named set of environment variables and hosts entries. Either
// part may be empty. Hosts is omitted from the JSON when empty, so files
// written before hosts existed load unchanged.
type Profile struct {
	Name  string            `json:"name"`
	Vars  map[string]string `json:"vars"`
	Hosts []HostEntry       `json:"hosts,omitempty"`
}

// ValidName reports whether s is a valid profile name (safe as a file name).
func ValidName(s string) bool { return nameRe.MatchString(s) }

// ValidVar reports whether s is a valid environment variable name (safe to embed in shell code).
func ValidVar(s string) bool { return varRe.MatchString(s) }

// Validate checks the profile name, every variable name and every hosts entry.
func (p Profile) Validate() error {
	if !ValidName(p.Name) {
		return fmt.Errorf("invalid profile name %q", p.Name)
	}
	for k := range p.Vars {
		if !ValidVar(k) {
			return fmt.Errorf("invalid variable name %q", k)
		}
	}
	if _, err := NormalizeHosts(p.Hosts); err != nil {
		return fmt.Errorf("profile %q: %w", p.Name, err)
	}
	return nil
}

// Store keeps one JSON file per profile under Dir/profiles.
type Store struct{ Dir string }

// DefaultDir returns $ENVMGR_HOME, or <user config dir>/envmgr.
func DefaultDir() (string, error) {
	if d := os.Getenv("ENVMGR_HOME"); d != "" {
		return d, nil
	}
	c, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(c, "envmgr"), nil
}

// NewStore opens the store at DefaultDir.
func NewStore() (*Store, error) {
	d, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return &Store{Dir: d}, nil
}

func (s *Store) path(name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("invalid profile name %q", name)
	}
	return filepath.Join(s.Dir, "profiles", name+".json"), nil
}

// Get loads a profile; returns ErrNotFound when missing.
func (s *Store) Get(name string) (Profile, error) {
	p, err := s.path(name)
	if err != nil {
		return Profile{}, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	var pr Profile
	if err := json.Unmarshal(b, &pr); err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", name, err)
	}
	if pr.Vars == nil {
		pr.Vars = map[string]string{}
	}
	pr.Name = name
	if err := pr.Validate(); err != nil {
		return pr, err
	}
	// Canonical form (Validate already checked it cannot fail).
	pr.Hosts, _ = NormalizeHosts(pr.Hosts)
	return pr, nil
}

// Save validates and writes a profile (file mode 0600: profiles may hold secrets).
func (s *Store) Save(pr Profile) error {
	if err := pr.Validate(); err != nil {
		return err
	}
	p, _ := s.path(pr.Name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if pr.Vars == nil {
		pr.Vars = map[string]string{}
	}
	hs, err := NormalizeHosts(pr.Hosts)
	if err != nil {
		return err
	}
	pr.Hosts = hs
	b, err := json.MarshalIndent(pr, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// List returns profile names, sorted.
func (s *Store) List() ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(s.Dir, "profiles"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range ents {
		n := e.Name()
		if filepath.Ext(n) == ".json" && ValidName(n[:len(n)-5]) {
			out = append(out, n[:len(n)-5])
		}
	}
	sort.Strings(out)
	return out, nil
}

// Delete removes a profile.
func (s *Store) Delete(name string) error {
	p, err := s.path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

// SetVar sets one variable, creating the profile if needed.
func (s *Store) SetVar(name, key, value string) error {
	if !ValidVar(key) {
		return fmt.Errorf("invalid variable name %q", key)
	}
	pr, err := s.Get(name)
	if errors.Is(err, ErrNotFound) {
		pr = Profile{Name: name, Vars: map[string]string{}}
	} else if err != nil {
		return err
	}
	pr.Vars[key] = value
	return s.Save(pr)
}

// UnsetVar removes one variable from a profile.
func (s *Store) UnsetVar(name, key string) error {
	pr, err := s.Get(name)
	if err != nil {
		return err
	}
	delete(pr.Vars, key)
	return s.Save(pr)
}

// SetHost maps hostnames to ip in a profile's hosts entries, creating the
// profile if needed (see Profile.AddHost).
func (s *Store) SetHost(name, ip string, hostnames ...string) error {
	return s.SetHostEntry(name, HostEntry{IP: ip, Names: hostnames})
}

// SetHostEntry is SetHost with an optional comment (an empty comment keeps
// the entry's existing one).
func (s *Store) SetHostEntry(name string, e HostEntry) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	pr, err := s.Get(name)
	if errors.Is(err, ErrNotFound) {
		pr = Profile{Name: name, Vars: map[string]string{}}
	} else if err != nil {
		return err
	}
	if _, err := pr.AddHost(e.IP, e.Names, e.Comment); err != nil {
		return err
	}
	return s.Save(pr)
}

// UnsetHost removes hostname from a profile's hosts entries (ErrHostNotFound
// when the profile does not map it).
func (s *Store) UnsetHost(name, hostname string) error {
	pr, err := s.Get(name)
	if err != nil {
		return err
	}
	if err := pr.RemoveHostname(hostname); err != nil {
		return err
	}
	return s.Save(pr)
}
