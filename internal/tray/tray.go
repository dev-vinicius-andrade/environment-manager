package tray

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// ErrUnsupported is returned on every platform but Windows.
var ErrUnsupported = errors.New("tray is only supported on Windows")

// ErrAlreadyRunning is returned when a tray for the same envmgr home runs.
var ErrAlreadyRunning = errors.New("the envmgr tray is already running")

// Options configure the tray app.
type Options struct {
	Store *profile.Store // profiles and selection home
	Exe   string         // envmgr binary (Open TUI, Start with Windows)
	Poll  time.Duration  // refresh interval (default 2s)
	Log   io.Writer      // debug log (nil = none)
	// HostsFile is passed to the elevated `hosts apply` (--file) when set: the
	// caller's ENVMGR_HOSTS_FILE, since an elevated process does not inherit
	// this environment. "" = the system hosts file.
	HostsFile string
}

// AutostartValue is the registry value name under HKCU\...\Run.
const AutostartValue = "envmgr-tray"

// MutexName returns the single-instance mutex name for an envmgr home: one
// tray per home (so a test home does not collide with the user's tray).
func MutexName(home string) string {
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(home))))
	return `Local\envmgr-tray-` + hex.EncodeToString(sum[:8])
}

// AutostartCommand is the command line stored for "Start with Windows".
func AutostartCommand(exe string) string { return `"` + exe + `" tray` }

// PowerShellTUICommand is the -Command argument that runs the TUI in a new
// PowerShell window (exe quoted as a PowerShell literal).
func PowerShellTUICommand(exe string) string {
	return "& '" + strings.ReplaceAll(exe, "'", "''") + "' tui"
}

// WTArgs are the Windows Terminal arguments that open a tab running the TUI
// (";" separates wt commands, so it is escaped).
func WTArgs(exe string) []string {
	return []string{"new-tab", "--title", "envmgr", strings.ReplaceAll(exe, ";", `\;`), "tui"}
}

// ElevatedApplyArgs are the envmgr arguments of the elevated window that
// "Apply hosts (asks for administrator)..." starts. An elevated process does
// not inherit this process's environment, so the envmgr home (profiles and
// hosts-applied.json) and a hosts file override (ENVMGR_HOSTS_FILE, "" for
// the system file) are passed explicitly; --pause keeps the window open so
// the user sees the result.
func ElevatedApplyArgs(home, hostsFile, profile string) []string {
	args := []string{"hosts", "apply", profile, "--home", home}
	if hostsFile != "" {
		args = append(args, "--file", hostsFile)
	}
	return append(args, "--pause")
}

// WindowsCommandLine joins args into one Windows command line (the
// lpParameters of ShellExecute), quoting each argument the way the C runtime
// and Go's os.Args parsing read it back (same rules as syscall.EscapeArg).
func WindowsCommandLine(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, escapeArg(a))
	}
	return strings.Join(out, " ")
}

func escapeArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}
