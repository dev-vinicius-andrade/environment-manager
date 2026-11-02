// Package hook generates the shell init code that makes a shell follow the
// machine-wide profile selection (internal/selected), and installs it in the
// shell's profile file.
//
// On every prompt the hook does a cheap in-shell check of selected.json
// (PowerShell: last-write time and size; bash/zsh/fish: the file content, read
// with a builtin) against the value cached in the session, and only when it
// changed runs `envmgr sync` and evaluates its output. Pinned sessions
// (ENVMGR_PINNED) skip the check entirely. Nothing is printed when nothing
// changes; the previous exit status is preserved for the user's prompt.
package hook

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/internal/selected"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

// ErrCmdUnsupported is returned for cmd, which has no prompt hook.
var ErrCmdUnsupported = errors.New(`cmd has no prompt hook, so it cannot follow the selection automatically; ` +
	`apply it by hand with: envmgr follow --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd" (or use PowerShell)`)

// Params are the values embedded in the hook script.
type Params struct {
	// Exe is the absolute path of the envmgr binary the hook calls.
	Exe string
	// DefaultSelection is the selected.json path used when ENVMGR_HOME is not
	// set in the shell at prompt time.
	DefaultSelection string
	// GOOS selects path style (forward slashes for POSIX shells on Windows);
	// empty means runtime.GOOS.
	GOOS string
}

// DefaultSelectionPath returns selected.json under the default home that
// applies when ENVMGR_HOME is unset (<user config dir>/envmgr).
func DefaultSelectionPath(userConfigDir string) string {
	return filepath.Join(userConfigDir, "envmgr", selected.FileName)
}

func (p Params) posixPath(s string) string {
	goos := p.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" {
		return strings.ReplaceAll(s, `\`, "/")
	}
	return s
}

// Script returns the init code for shell k.
func Script(k shell.Kind, p Params) (string, error) {
	if p.Exe == "" || p.DefaultSelection == "" {
		return "", errors.New("hook: missing executable or selection path")
	}
	for _, s := range []string{p.Exe, p.DefaultSelection} {
		if strings.ContainsAny(s, "\x00\r\n") {
			return "", fmt.Errorf("hook: path %q contains a control character", s)
		}
	}
	switch k {
	case shell.Bash:
		return bashScript(p), nil
	case shell.Zsh:
		return zshScript(p), nil
	case shell.Fish:
		return fishScript(p), nil
	case shell.PowerShell:
		return powershellScript(p), nil
	case shell.Cmd:
		return "", ErrCmdUnsupported
	}
	return "", fmt.Errorf("unsupported shell %q", k)
}

// posixBody is the hook function body shared by bash and zsh.
func posixBody(k shell.Kind) string {
	return `__envmgr_hook() {
  local __envmgr_ret=$?
  if [ -z "${ENVMGR_PINNED:-}" ] || [ "${ENVMGR_PINNED}" = 0 ]; then
    local __envmgr_f="${ENVMGR_HOME:+$ENVMGR_HOME/selected.json}"
    __envmgr_f="${__envmgr_f:-$__envmgr_default_sel}"
    local __envmgr_cur=""
    if [ -f "$__envmgr_f" ]; then
      { IFS= read -r -d '' __envmgr_cur < "$__envmgr_f"; } 2>/dev/null || true
    fi
    __envmgr_cur="$__envmgr_f|$__envmgr_cur"
    if [ "$__envmgr_cur" != "${__envmgr_last-}" ]; then
      __envmgr_last=$__envmgr_cur
      eval "$("$__envmgr_exe" sync --shell ` + string(k) + `)"
    fi
  fi
  return $__envmgr_ret
}
`
}

func bashScript(p Params) string {
	return `# envmgr hook (bash): follow the machine-wide profile selection (envmgr select / tray).
__envmgr_exe=` + shell.BashQuote(p.posixPath(p.Exe)) + `
__envmgr_default_sel=` + shell.BashQuote(p.posixPath(p.DefaultSelection)) + `
` + posixBody(shell.Bash) + `case ";${PROMPT_COMMAND[*]:-};" in
  *__envmgr_hook*) ;;
  *)
    if [[ "$(declare -p PROMPT_COMMAND 2>/dev/null)" == "declare -a"* ]]; then
      PROMPT_COMMAND=(__envmgr_hook "${PROMPT_COMMAND[@]}")
    else
      PROMPT_COMMAND="__envmgr_hook${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
    fi
    ;;
esac
`
}

func zshScript(p Params) string {
	return `# envmgr hook (zsh): follow the machine-wide profile selection (envmgr select / tray).
__envmgr_exe=` + shell.BashQuote(p.posixPath(p.Exe)) + `
__envmgr_default_sel=` + shell.BashQuote(p.posixPath(p.DefaultSelection)) + `
` + posixBody(shell.Zsh) + `autoload -Uz add-zsh-hook
add-zsh-hook precmd __envmgr_hook
`
}

func fishScript(p Params) string {
	return `# envmgr hook (fish): follow the machine-wide profile selection (envmgr select / tray).
set -g __envmgr_exe ` + shell.FishQuote(p.posixPath(p.Exe)) + `
set -g __envmgr_default_sel ` + shell.FishQuote(p.posixPath(p.DefaultSelection)) + `
function __envmgr_hook --on-event fish_prompt
    if set -q ENVMGR_PINNED; and test -n "$ENVMGR_PINNED"; and test "$ENVMGR_PINNED" != 0
        return
    end
    set -l f $__envmgr_default_sel
    if set -q ENVMGR_HOME; and test -n "$ENVMGR_HOME"
        set f "$ENVMGR_HOME/selected.json"
    end
    set -l cur ""
    if test -f "$f"
        read -z cur < "$f" 2>/dev/null
    end
    set cur "$f|$cur"
    if not set -q __envmgr_last; or test "$cur" != "$__envmgr_last"
        set -g __envmgr_last $cur
        $__envmgr_exe sync --shell fish | source
    end
end
`
}

// powershellScript is pure ASCII (paths go through PowerShellQuote) because
// Windows PowerShell decodes native output with the OEM code page. It must be
// evaluated as a whole: envmgr hook powershell | Out-String | Invoke-Expression.
func powershellScript(p Params) string {
	return `# envmgr hook (PowerShell): follow the machine-wide profile selection (envmgr select / tray).
$global:__envmgr_exe = ` + shell.PowerShellQuote(p.Exe) + `
$global:__envmgr_default_sel = ` + shell.PowerShellQuote(p.DefaultSelection) + `
if (-not (Test-Path Variable:global:__envmgr_last)) { $global:__envmgr_last = $null }
function global:__envmgr_hook {
    if ($env:ENVMGR_PINNED -and $env:ENVMGR_PINNED -ne '0') { return }
    $f = $global:__envmgr_default_sel
    if ($env:ENVMGR_HOME) { $f = [System.IO.Path]::Combine($env:ENVMGR_HOME, 'selected.json') }
    $key = $f + '|missing'
    try {
        $i = [System.IO.FileInfo]::new($f)
        if ($i.Exists) { $key = $f + '|' + $i.LastWriteTimeUtc.Ticks + '|' + $i.Length }
    } catch { }
    if ($key -ne $global:__envmgr_last) {
        $global:__envmgr_last = $key
        $code = & $global:__envmgr_exe sync --shell powershell
        foreach ($line in @($code)) { if ($line) { Invoke-Expression $line } }
    }
}
$__envmgr_prev = Get-Command prompt -CommandType Function -ErrorAction SilentlyContinue
if (-not $__envmgr_prev -or $__envmgr_prev.ScriptBlock.ToString() -notmatch '__envmgr_hook') {
    if ($__envmgr_prev) {
        $global:__envmgr_orig_prompt = $__envmgr_prev.ScriptBlock
    } else {
        $global:__envmgr_orig_prompt = { "PS $($executionContext.SessionState.Path.CurrentLocation)$('>' * ($nestedPromptLevel + 1)) " }
    }
    function global:prompt {
        $__envmgr_ok = $?
        $__envmgr_lec = $global:LASTEXITCODE
        try { __envmgr_hook } catch { Write-Host "envmgr hook: $_" -ForegroundColor Red }
        $global:LASTEXITCODE = $__envmgr_lec
        if (-not $__envmgr_ok) { Write-Error '' -ErrorAction Ignore }
        & $global:__envmgr_orig_prompt
    }
}
Remove-Variable __envmgr_prev -ErrorAction SilentlyContinue
`
}
