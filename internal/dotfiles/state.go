package dotfiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Store keeps envmgr's dotfiles state inside the envmgr home directory.
type Store struct{ Dir string }

// Config is the registered dotfiles repository.
type Config struct {
	Dir    string `json:"dir"`
	Source string `json:"source,omitempty"` // where it was cloned from
}

// AppliedLink records one destination envmgr created.
type AppliedLink struct {
	Src    string `json:"src"`
	Dest   string `json:"dest"`
	Mode   string `json:"mode"`             // "symlink" or "copy"
	Backup string `json:"backup,omitempty"` // what was moved out of the way
}

// Applied is everything envmgr created; unlink touches nothing else.
type Applied struct {
	Links []AppliedLink `json:"links"`
}

func (s Store) configPath() string  { return filepath.Join(s.Dir, "dotfiles.json") }
func (s Store) appliedPath() string { return filepath.Join(s.Dir, "dotfiles-applied.json") }

// LoadConfig returns the registered repository; ok is false when none is.
func (s Store) LoadConfig() (c Config, ok bool, err error) {
	err = readJSON(s.configPath(), &c)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	}
	return c, err == nil && c.Dir != "", err
}

// SaveConfig registers the repository.
func (s Store) SaveConfig(c Config) error { return writeJSON(s.configPath(), c) }

// LoadApplied returns the applied record (empty when there is none).
func (s Store) LoadApplied() (Applied, error) {
	var a Applied
	err := readJSON(s.appliedPath(), &a)
	if errors.Is(err, os.ErrNotExist) {
		return Applied{}, nil
	}
	return a, err
}

// SaveApplied replaces the applied record.
func (s Store) SaveApplied(a Applied) error { return writeJSON(s.appliedPath(), a) }

func (a *Applied) find(dest string) int {
	for i, l := range a.Links {
		if samePath(l.Dest, dest) {
			return i
		}
	}
	return -1
}

func (a *Applied) set(l AppliedLink) {
	if i := a.find(l.Dest); i >= 0 {
		a.Links[i] = l
		return
	}
	a.Links = append(a.Links, l)
}

func (a *Applied) drop(dest string) {
	if i := a.find(dest); i >= 0 {
		a.Links = append(a.Links[:i], a.Links[i+1:]...)
	}
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
