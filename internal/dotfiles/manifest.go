// Package dotfiles links the files of a dotfiles repository into place.
//
// The repository describes what goes where in a manifest (the "links" section
// of its .dotfiles.config.json). envmgr never deletes a file it did not
// create: whatever is in the way is moved to a timestamped backup, and what
// envmgr made is recorded so that unlink only touches that.
package dotfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ManifestFile is the repository file that holds the manifest.
const ManifestFile = ".dotfiles.config.json"

// Link maps one file or directory of the repository to its destination.
type Link struct {
	Src  string   `json:"src"`
	Dest string   `json:"dest"`
	OS   []string `json:"os,omitempty"` // GOOS values; empty = every OS
}

// Manifest is the part of the repository's config that envmgr reads; every
// other key (package managers, packages...) is ignored.
type Manifest struct {
	Links []Link `json:"links"`
}

// StripComments removes // and /* */ comments outside of strings, so the
// JSON-with-comments files that editors accept can be parsed.
func StripComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(b) {
				i++
				out = append(out, b[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				if b[i] == '\n' {
					out = append(out, '\n')
				}
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}

// LoadManifest reads and validates dir/.dotfiles.config.json.
func LoadManifest(dir string) (Manifest, error) {
	p := filepath.Join(dir, ManifestFile)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, fmt.Errorf("%s not found: add a \"links\" section to it (see .claude/instructions/dotfiles.md)", p)
	}
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(StripComments(b), &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", p, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", p, err)
	}
	return m, nil
}

// Validate checks every link: src stays inside the repository, dest is set,
// no two links share a destination.
func (m Manifest) Validate() error {
	if len(m.Links) == 0 {
		return errors.New(`no "links" in the manifest`)
	}
	seen := map[string]int{}
	for i, l := range m.Links {
		n := i + 1
		if strings.TrimSpace(l.Src) == "" || strings.TrimSpace(l.Dest) == "" {
			return fmt.Errorf("link %d: src and dest are required", n)
		}
		if err := checkSrc(l.Src); err != nil {
			return fmt.Errorf("link %d: %w", n, err)
		}
		if j, dup := seen[l.Dest]; dup {
			return fmt.Errorf("link %d: dest %q already used by link %d", n, l.Dest, j)
		}
		seen[l.Dest] = n
	}
	return nil
}

func checkSrc(src string) error {
	if filepath.IsAbs(src) || strings.HasPrefix(src, "/") || strings.HasPrefix(src, `\`) || filepath.VolumeName(src) != "" {
		return fmt.Errorf("src %q must be relative to the repository", src)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(src)))
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return fmt.Errorf("src %q must stay inside the repository", src)
	}
	return nil
}

// ExpandDest expands ~, $VAR and ${VAR} in dest (home stands for ~, $HOME and
// $USERPROFILE when those are unset) and requires the result to be absolute.
func ExpandDest(dest, home string, env func(string) string) (string, error) {
	if env == nil {
		env = os.Getenv
	}
	switch {
	case dest == "~":
		dest = home
	case strings.HasPrefix(dest, "~/") || strings.HasPrefix(dest, `~\`):
		dest = home + dest[1:]
	}
	var missing string
	dest = os.Expand(dest, func(name string) string {
		if v := env(name); v != "" {
			return v
		}
		if name == "HOME" || name == "USERPROFILE" {
			return home
		}
		missing = name
		return ""
	})
	if missing != "" {
		return "", fmt.Errorf("dest uses $%s, which is not set", missing)
	}
	dest = filepath.Clean(filepath.FromSlash(dest))
	if !filepath.IsAbs(dest) {
		return "", fmt.Errorf("dest %q is not absolute (start it with ~ or a variable)", dest)
	}
	return dest, nil
}

// appliesTo reports whether the link is meant for goos.
func (l Link) appliesTo(goos string) bool {
	if len(l.OS) == 0 {
		return true
	}
	for _, o := range l.OS {
		if strings.EqualFold(o, goos) {
			return true
		}
	}
	return false
}

// samePath compares two paths the way the OS does.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
