// Package hosts edits the system hosts file safely: envmgr only ever changes
// the lines of its own managed block (between "# BEGIN envmgr" and
// "# END envmgr"); every other byte of the file is preserved. The same code
// serves the local `envmgr hosts` command and the remote hosts.* operations a
// parent relays to a child, so validation and parsing exist only here.
package hosts

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hostspec"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

const (
	// EnvFile overrides the hosts file path (tests, trying envmgr safely).
	EnvFile = "ENVMGR_HOSTS_FILE"
	// DefaultGroup holds entries added without --group.
	DefaultGroup = "default"
	// MaxHostnameLen is the RFC 1123 limit for a full host name.
	MaxHostnameLen = hostspec.MaxHostnameLen
	// MaxHostnamesPerEntry caps the names on one line (some resolvers have limits).
	MaxHostnamesPerEntry = hostspec.MaxHostnamesPerEntry
	// ProfileGroupPrefix starts the name of the group that holds the hosts
	// entries of an applied profile ("profile:dev"). ':' is not allowed in
	// user group names (profile-name rules), so it can never collide with a
	// group made with `envmgr hosts add --group`.
	ProfileGroupPrefix = "profile:"
)

// The validation itself lives in internal/hostspec (shared with the hosts
// entries stored in profiles); these names keep this package's API.
var (
	// ParseIP validates an IPv4 or IPv6 address and returns its canonical form.
	ParseIP = hostspec.ParseIP
	// ValidHostname checks an RFC 1123 host name and returns it in lower case.
	ValidHostname = hostspec.ValidHostname
	// NormalizeHostnames validates, lower-cases and de-duplicates names (1-32).
	NormalizeHostnames = hostspec.NormalizeHostnames
	// IsCritical reports whether name is one the system relies on.
	IsCritical = hostspec.IsCritical
)

// ProfileGroup returns the managed-block group name of profile name's hosts.
func ProfileGroup(name string) string { return ProfileGroupPrefix + name }

// ProfileOfGroup returns the profile name of a profile group ("profile:dev"
// gives "dev", true), or "", false for any other group.
func ProfileOfGroup(g string) (string, bool) {
	if !strings.HasPrefix(g, ProfileGroupPrefix) {
		return "", false
	}
	name := g[len(ProfileGroupPrefix):]
	if !profile.ValidName(name) {
		return "", false
	}
	return name, true
}

// ValidGroup reports whether g is a valid group name in the managed block: a
// user group (profile-name rules) or a profile group ("profile:<profile>").
func ValidGroup(g string) bool {
	if profile.ValidName(g) {
		return true
	}
	_, ok := ProfileOfGroup(g)
	return ok
}

// CheckGroup validates a group given by the user for an edit, mapping "" to
// DefaultGroup. Profile groups are refused: they are written only by
// `envmgr hosts apply` from the profile's own entries.
func CheckGroup(g string) (string, error) {
	if g == "" {
		return DefaultGroup, nil
	}
	if p, ok := ProfileOfGroup(g); ok {
		return "", fmt.Errorf("group %q holds the hosts of profile %q and is managed by `envmgr hosts apply`: edit the profile (envmgr profile hosts add|remove %s ...) and apply it again", g, p, p)
	}
	if !ValidGroup(g) {
		return "", fmt.Errorf("invalid group name %q", g)
	}
	return g, nil
}

// DefaultPath returns the system hosts file for goos. getenv reads SystemRoot
// on Windows.
func DefaultPath(goos string, getenv func(string) string) string {
	if goos == "windows" {
		root := ""
		if getenv != nil {
			root = getenv("SystemRoot")
		}
		if root == "" {
			root = `C:\Windows`
		}
		return strings.TrimRight(root, `\/`) + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// ResolvePath returns the hosts file to use and where that choice came from:
// flagValue (--file), then $ENVMGR_HOSTS_FILE, then the system default.
func ResolvePath(flagValue string) (path, source string) {
	if flagValue != "" {
		return flagValue, "--file"
	}
	if v := os.Getenv(EnvFile); v != "" {
		return v, EnvFile
	}
	return DefaultPath(runtime.GOOS, os.Getenv), "system default"
}
