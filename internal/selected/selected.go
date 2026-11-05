// Package selected stores the machine-wide profile selection: the profile the
// user picked in the tray (or with `envmgr select`). Shells that run the hook
// (`envmgr hook`) follow it on their next prompt unless they are pinned
// (ENVMGR_PINNED, set by `envmgr use` and the TUI).
//
// The selection lives in <ENVMGR_HOME>/selected.json as {"profile", "env",
// "hosts", "updatedAt"}; an empty profile means "none". env and hosts are the
// selected scopes; a file without them (written before scopes existed) means
// env only. It never holds variable values.
package selected

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// FileName is the selection file inside the envmgr home directory.
const FileName = "selected.json"

// Selection is the content of selected.json.
type Selection struct {
	Profile string `json:"profile"`
	// Env: following shells activate the profile's variables (hook / sync).
	Env bool `json:"env"`
	// Hosts: the profile's hosts entries are wanted on this machine. Nothing
	// applies them implicitly (that needs administrator rights): sync only
	// hints at `envmgr hosts apply`.
	Hosts     bool      `json:"hosts"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Scopes says which parts of a profile are selected.
type Scopes struct {
	Env, Hosts bool
}

// EnvProfile is the profile following shells activate: Profile when the env
// scope is selected, else "".
func (s Selection) EnvProfile() string {
	if s.Env {
		return s.Profile
	}
	return ""
}

// HostsProfile is the profile whose hosts are wanted, or "".
func (s Selection) HostsProfile() string {
	if s.Hosts {
		return s.Profile
	}
	return ""
}

// fileSelection reads both the current and the pre-scopes format.
type fileSelection struct {
	Profile   string    `json:"profile"`
	Env       *bool     `json:"env"`
	Hosts     *bool     `json:"hosts"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Selector reads and writes the selection next to a profile store.
type Selector struct {
	Profiles *profile.Store
	// now is the clock (tests).
	now func() time.Time
}

// New returns a Selector for the default store (ENVMGR_HOME or the user config dir).
func New() (*Selector, error) {
	st, err := profile.NewStore()
	if err != nil {
		return nil, err
	}
	return For(st), nil
}

// For returns a Selector for the given profile store.
func For(st *profile.Store) *Selector { return &Selector{Profiles: st} }

// Path returns the selection file path.
func (s *Selector) Path() string { return filepath.Join(s.Profiles.Dir, FileName) }

// Get returns the current selection. A missing file is no selection. A file
// whose profile name is invalid is an error (it is never used in shell code).
func (s *Selector) Get() (Selection, error) {
	b, err := readRetry(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return Selection{}, nil
	}
	if err != nil {
		return Selection{}, err
	}
	var f fileSelection
	if err := json.Unmarshal(b, &f); err != nil {
		return Selection{}, fmt.Errorf("%s: %w", s.Path(), err)
	}
	if f.Profile != "" && !profile.ValidName(f.Profile) {
		return Selection{}, fmt.Errorf("%s: invalid profile name %q", s.Path(), f.Profile)
	}
	sel := Selection{Profile: f.Profile, UpdatedAt: f.UpdatedAt}
	switch {
	case f.Profile == "":
	case f.Env == nil && f.Hosts == nil:
		sel.Env = true // written before scopes existed: environment only
	default:
		sel.Env = f.Env != nil && *f.Env
		sel.Hosts = f.Hosts != nil && *f.Hosts
	}
	return sel, nil
}

// Set selects an existing profile for the environment scope only.
func (s *Selector) Set(name string) error { return s.SetScoped(name, Scopes{Env: true}) }

// SetScoped selects an existing profile for the given scopes (at least one).
func (s *Selector) SetScoped(name string, sc Scopes) error {
	if !profile.ValidName(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	if !sc.Env && !sc.Hosts {
		return errors.New("select at least one scope (environment or hosts); use Clear to select nothing")
	}
	if _, err := s.Profiles.Get(name); errors.Is(err, profile.ErrNotFound) {
		return fmt.Errorf("profile %q not found", name)
	} else if err != nil {
		return err
	}
	return s.write(name, sc)
}

// Clear removes the selection (writes an empty profile, so watchers see a change).
func (s *Selector) Clear() error { return s.write("", Scopes{}) }

func (s *Selector) write(name string, sc Scopes) error {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	b, err := json.MarshalIndent(Selection{Profile: name, Env: sc.Env, Hosts: sc.Hosts, UpdatedAt: now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(s.Path(), append(b, '\n'))
}

// ReadFileRetry reads a file written with WriteFileAtomic (see readRetry).
func ReadFileRetry(path string) ([]byte, error) { return readRetry(path) }

// readRetry reads path. On Windows, a read that races with the rename in
// WriteFileAtomic fails with a sharing violation for a moment: retry briefly.
func readRetry(path string) ([]byte, error) {
	for i := 0; ; i++ {
		b, err := os.ReadFile(path)
		if err == nil || errors.Is(err, os.ErrNotExist) || i >= 20 || runtime.GOOS != "windows" {
			return b, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// WriteFileAtomic writes data to a temp file in the same directory (mode
// 0600) and renames it over path, so readers see either the old or the new
// content. Also used for hosts-applied.json (internal/hostsapply).
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func(e error) error {
		f.Close()
		os.Remove(tmp)
		return e
	}
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return cleanup(err)
	}
	if _, err := f.Write(data); err != nil {
		return cleanup(err)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// On Windows a reader holding the file open (a shell hook reading it) can
	// make the rename fail for a moment: retry briefly.
	for i := 0; ; i++ {
		err = os.Rename(tmp, path)
		if err == nil || i >= 20 || runtime.GOOS != "windows" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
