// Package shell turns profile activation into shell code that the user's shell
// evaluates (a child process cannot change its parent's environment).
//
// Supported shells: bash, zsh (same syntax as bash), fish, powershell and cmd.
// Activation state lives in the shell's own environment:
//
//	ENVMGR_PROFILE        name of the active profile
//	ENVMGR_VARS           comma separated names of the variables the profile set
//	ENVMGR_PREV_<VAR>     value <VAR> had before activation (absent = it was unset)
package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Kind identifies a target shell syntax.
type Kind string

const (
	Bash       Kind = "bash"
	Zsh        Kind = "zsh"
	Fish       Kind = "fish"
	PowerShell Kind = "powershell"
	Cmd        Kind = "cmd"
)

// Names lists the accepted values of the --shell flag.
var Names = []string{"bash", "zsh", "fish", "powershell", "pwsh", "cmd"}

// FlagUsage is the help text for the --shell flag.
const FlagUsage = "target shell: bash, zsh, fish, powershell (pwsh) or cmd (default: auto-detect)"

// Parse converts a --shell value to a Kind.
func Parse(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bash", "sh":
		return Bash, nil
	case "zsh":
		return Zsh, nil
	case "fish":
		return Fish, nil
	case "powershell", "pwsh", "ps":
		return PowerShell, nil
	case "cmd", "cmd.exe":
		return Cmd, nil
	}
	return "", fmt.Errorf("unsupported shell %q (supported: %s)", s, strings.Join(Names, ", "))
}

// Resolve returns the Kind for a --shell flag value, auto-detecting when empty.
func Resolve(flag string) (Kind, error) {
	if strings.TrimSpace(flag) == "" {
		return Detect(), nil
	}
	return Parse(flag)
}

// Detect guesses the user's shell: powershell on Windows, otherwise from $SHELL
// (fish, zsh), falling back to bash.
func Detect() Kind { return detect(runtime.GOOS, os.Getenv("SHELL")) }

func detect(goos, shellEnv string) Kind {
	if goos == "windows" {
		return PowerShell
	}
	switch strings.TrimSuffix(filepath.Base(shellEnv), ".exe") {
	case "fish":
		return Fish
	case "zsh":
		return Zsh
	}
	return Bash
}
