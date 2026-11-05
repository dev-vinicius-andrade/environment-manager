package hook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

// Markers delimit the block that --install adds and --uninstall removes.
const (
	BeginMarker = "# >>> envmgr hook >>>"
	EndMarker   = "# <<< envmgr hook <<<"
)

// Env gives the locations used to find profile files (injectable for tests).
type Env struct {
	GOOS      string
	Home      string              // user home directory
	Documents string              // Windows "Documents" known folder ("" = Home\Documents)
	Getenv    func(string) string // environment lookup (ZDOTDIR, XDG_CONFIG_HOME)
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// ProfilePaths returns the profile files the hook is installed in for shell k.
// PowerShell on Windows has two: Windows PowerShell 5.1 and PowerShell 7+.
func ProfilePaths(k shell.Kind, e Env) ([]string, error) {
	if e.Home == "" {
		return nil, errors.New("cannot find the home directory")
	}
	xdg := e.getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(e.Home, ".config")
	}
	switch k {
	case shell.Bash:
		return []string{filepath.Join(e.Home, ".bashrc")}, nil
	case shell.Zsh:
		dir := e.getenv("ZDOTDIR")
		if dir == "" {
			dir = e.Home
		}
		return []string{filepath.Join(dir, ".zshrc")}, nil
	case shell.Fish:
		return []string{filepath.Join(xdg, "fish", "config.fish")}, nil
	case shell.PowerShell:
		if e.GOOS == "windows" {
			docs := e.Documents
			if docs == "" {
				docs = filepath.Join(e.Home, "Documents")
			}
			return []string{
				filepath.Join(docs, "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
				filepath.Join(docs, "PowerShell", "Microsoft.PowerShell_profile.ps1"),
			}, nil
		}
		return []string{filepath.Join(xdg, "powershell", "Microsoft.PowerShell_profile.ps1")}, nil
	case shell.Cmd:
		return nil, ErrCmdUnsupported
	}
	return nil, fmt.Errorf("unsupported shell %q", k)
}

// Block returns the marked block added to the profile file of shell k: it runs
// `<exe> hook <shell>` at shell start (only when exe still exists) and evaluates it.
func Block(k shell.Kind, exe, goos string) (string, error) {
	if strings.ContainsAny(exe, "\x00\r\n") {
		return "", fmt.Errorf("executable path %q contains a control character", exe)
	}
	p := Params{GOOS: goos}
	header := BeginMarker + "\n# Added by \"envmgr hook --install " + string(k) + "\"; remove with \"envmgr hook --uninstall " + string(k) + "\".\n"
	var body string
	switch k {
	case shell.Bash, shell.Zsh:
		q := shell.BashQuote(p.posixPath(exe))
		body = "if [ -x " + q + " ]; then eval \"$(" + q + " hook " + string(k) + ")\"; fi\n"
	case shell.Fish:
		q := shell.FishQuote(p.posixPath(exe))
		body = "if test -x " + q + "\n    " + q + " hook fish | source\nend\n"
	case shell.PowerShell:
		// ASCII-only expression: Windows PowerShell reads BOM-less files as ANSI.
		q := shell.PowerShellQuote(exe)
		body = "if (Test-Path -LiteralPath (" + q + ")) { & (" + q + ") hook powershell | Out-String | Invoke-Expression }\n"
	case shell.Cmd:
		return "", ErrCmdUnsupported
	default:
		return "", fmt.Errorf("unsupported shell %q", k)
	}
	return header + body + EndMarker + "\n", nil
}

// Change is the result of planning an install or uninstall on one file.
type Change struct {
	Path    string
	Before  string // current content ("" when the file does not exist)
	After   string // content to write
	Exists  bool   // the file exists
	Changed bool   // After differs from Before
	Had     bool   // the file already had an envmgr block
}

// removeBlock strips every marked block (markers included) from content.
func removeBlock(content string) (string, bool) {
	had := false
	for {
		i := strings.Index(content, BeginMarker)
		if i < 0 {
			return content, had
		}
		rest := content[i:]
		j := strings.Index(rest, EndMarker)
		if j < 0 {
			// Unterminated block: leave the file alone rather than guess.
			return content, had
		}
		end := i + j + len(EndMarker)
		// Swallow the line break after the end marker.
		if strings.HasPrefix(content[end:], "\r\n") {
			end += 2
		} else if strings.HasPrefix(content[end:], "\n") {
			end++
		}
		start := i
		// Also drop the blank line PlanInstall put before the block.
		if strings.HasSuffix(content[:start], "\r\n\r\n") {
			start -= 2
		} else if strings.HasSuffix(content[:start], "\n\n") {
			start--
		}
		content = content[:start] + content[end:]
		had = true
	}
}

// PlanInstall computes the new content of a profile file with block installed
// (replacing an existing envmgr block in place, so it is idempotent).
func PlanInstall(path, current string, exists bool, block string) Change {
	c := Change{Path: path, Before: current, Exists: exists}
	nl := "\n"
	if strings.Contains(current, "\r\n") || (!exists && strings.HasSuffix(strings.ToLower(path), ".ps1") && runtime.GOOS == "windows") {
		nl = "\r\n"
	}
	blk := strings.ReplaceAll(block, "\n", nl)
	if i := strings.Index(current, BeginMarker); i >= 0 && strings.Contains(current[i:], EndMarker) {
		c.Had = true
		j := i + strings.Index(current[i:], EndMarker) + len(EndMarker)
		if strings.HasPrefix(current[j:], "\r\n") {
			j += 2
		} else if strings.HasPrefix(current[j:], "\n") {
			j++
		}
		rest, _ := removeBlock(current[j:])
		c.After = current[:i] + blk + rest
	} else {
		after := current
		if after != "" && !strings.HasSuffix(after, "\n") {
			after += nl
		}
		if after != "" {
			after += nl
		}
		c.After = after + blk
	}
	c.Changed = c.After != c.Before
	return c
}

// PlanUninstall computes the content of a profile file without the envmgr block.
func PlanUninstall(path, current string, exists bool) Change {
	after, had := removeBlock(current)
	return Change{Path: path, Before: current, After: after, Exists: exists, Had: had, Changed: after != current}
}

// ReadFile returns a file's content and whether it exists.
func ReadFile(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// Apply writes c.After to c.Path (creating parent directories), keeping the
// file mode of an existing file. It does nothing when nothing changed.
func Apply(c Change) error {
	if !c.Changed {
		return nil
	}
	target := c.Path
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(c.Path); err == nil {
		mode = fi.Mode().Perm()
		// Write through a symlinked profile (dotfile managers) instead of
		// replacing the link with a regular file.
		if real, err := filepath.EvalSymlinks(c.Path); err == nil {
			target = real
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".envmgr-tmp"
	if err := os.WriteFile(tmp, []byte(c.After), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// CurrentEnv returns the Env of this process.
func CurrentEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{GOOS: runtime.GOOS, Home: home, Documents: documentsDir(), Getenv: os.Getenv}, nil
}

// CurrentParams returns the hook Params for this binary and user.
func CurrentParams() (Params, error) {
	exe, err := os.Executable()
	if err != nil {
		return Params{}, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Params{}, err
	}
	return Params{Exe: exe, DefaultSelection: DefaultSelectionPath(cfg)}, nil
}
