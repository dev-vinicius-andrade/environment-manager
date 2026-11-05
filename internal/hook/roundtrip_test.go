package hook

// Real round trips: a test build of envmgr (testdata/envmgrtest: the real root
// command plus the selection commands), a temp ENVMGR_HOME, and the hook
// sourced in a real shell whose prompt is then run the way the shell would.
// Each test skips when its shell is not available.

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

var (
	buildOnce sync.Once
	builtExe  string
	buildErr  error
	buildOut  []byte
)

// testBinary builds testdata/envmgrtest once per test run.
func testBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "envmgr-hooktest-")
		if err != nil {
			buildErr = err
			return
		}
		builtExe = filepath.Join(dir, "envmgr")
		if runtime.GOOS == "windows" {
			builtExe += ".exe"
		}
		cmd := exec.Command("go", "build", "-buildvcs=false", "-o", builtExe, "./testdata/envmgrtest")
		buildOut, buildErr = cmd.CombinedOutput()
	})
	if buildErr != nil {
		t.Fatalf("building the test binary: %v\n%s", buildErr, buildOut)
	}
	return builtExe
}

func TestMain(m *testing.M) {
	code := m.Run()
	if builtExe != "" {
		os.RemoveAll(filepath.Dir(builtExe))
	}
	os.Exit(code)
}

// setupHome creates a temp ENVMGR_HOME with profiles a, b, c (URL=aa/bb/cc).
func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	st := &profile.Store{Dir: home}
	for _, n := range []string{"a", "b", "c"} {
		if err := st.Save(profile.Profile{Name: n, Vars: map[string]string{"URL": n + n}}); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// cleanEnv is the test process environment without envmgr state, URL or ENVMGR_HOME.
func cleanEnv() []string {
	out := []string{}
	for _, e := range os.Environ() {
		u := strings.ToUpper(e)
		if strings.HasPrefix(u, "ENVMGR_") || strings.HasPrefix(u, "URL=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// expected lines printed by both round-trip scripts.
var wantSteps = []string{
	"0:orig::", // nothing selected yet
	"1:aa:a:",  // select a -> next prompt applies a, not pinned
	"2:bb:b:",  // select b -> switches
	"3:cc:c:1", // use c (pinned) then select a -> stays on c
	"4:aa:a:",  // follow -> back to the selection, unpinned
	"5:orig::", // select --none -> deactivated
	"6:bb:b:",  // pin with use b, deactivate (unpins), select b -> applied
}

func checkSteps(t *testing.T, out string) {
	t.Helper()
	got := []string{}
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if len(l) > 1 && l[0] >= '0' && l[0] <= '9' && l[1] == ':' {
			got = append(got, l)
		}
	}
	if strings.Join(got, "\n") != strings.Join(wantSteps, "\n") {
		t.Fatalf("steps:\n got %q\nwant %q\nfull output:\n%s", got, wantSteps, out)
	}
}

func findBash(t *testing.T) string {
	t.Helper()
	cands := []string{}
	if runtime.GOOS == "windows" {
		// Prefer Git for Windows' bash; System32\bash.exe is the WSL launcher,
		// which cannot run the Windows test binary by its Windows path.
		for _, p := range []string{`C:\Program Files\Git\bin\bash.exe`, `C:\Program Files\Git\usr\bin\bash.exe`} {
			cands = append(cands, p)
		}
	}
	if p, err := exec.LookPath("bash"); err == nil {
		cands = append(cands, p)
	}
	for _, p := range cands {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		out, err := exec.Command(p, "-c", "uname -s").Output()
		if err != nil {
			continue
		}
		u := strings.ToUpper(strings.TrimSpace(string(out)))
		if runtime.GOOS == "windows" && !(strings.Contains(u, "MINGW") || strings.Contains(u, "MSYS") || strings.Contains(u, "CYGWIN")) {
			continue
		}
		return p
	}
	t.Skip("no usable bash")
	return ""
}

func TestRoundTripBashHook(t *testing.T) {
	bash := findBash(t)
	exe := filepath.ToSlash(testBinary(t))
	home := filepath.ToSlash(setupHome(t))
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	E := q(exe)
	script := `set -u
export ENVMGR_HOME=` + q(home) + `
export URL=orig
__mine_count=0
__mine() { __mine_count=$((__mine_count+1)); }
PROMPT_COMMAND=__mine
# Sourced twice on purpose: must be harmless.
eval "$(` + E + ` hook bash)"
eval "$(` + E + ` hook bash)"
echo "PC=$PROMPT_COMMAND"
prompt() { eval "$PROMPT_COMMAND"; }
s() { echo "$1:$URL:${ENVMGR_PROFILE-}:${ENVMGR_PINNED-}"; }
prompt; s 0
` + E + ` select a 2>/dev/null; prompt; s 1
echo "QUIET-BEGIN"; prompt 2>&1; echo "QUIET-END"
false; __envmgr_hook; echo "status=$?"
` + E + ` select b 2>/dev/null; prompt; s 2
eval "$(` + E + ` use c --shell bash 2>/dev/null)"; ` + E + ` select a 2>/dev/null; prompt; s 3
eval "$(` + E + ` follow --shell bash 2>/dev/null)"; prompt; s 4
` + E + ` select --none 2>/dev/null; prompt; s 5
eval "$(` + E + ` use b --shell bash 2>/dev/null)"; eval "$(` + E + ` deactivate --shell bash 2>/dev/null)"; ` + E + ` select b 2>/dev/null; prompt; s 6
echo "mine=$__mine_count"
`
	cmd := exec.Command(bash, "--norc", "--noprofile", "-s")
	cmd.Env = cleanEnv()
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	checkSteps(t, out)
	if !strings.Contains(out, "PC=__envmgr_hook;__mine\n") {
		t.Errorf("PROMPT_COMMAND not preserved or hook added twice:\n%s", out)
	}
	if !strings.Contains(out, "QUIET-BEGIN\nQUIET-END\n") {
		t.Errorf("unchanged prompt printed something:\n%s", out)
	}
	if !strings.Contains(out, "status=1\n") {
		t.Errorf("hook did not preserve $?:\n%s", out)
	}
	if !strings.Contains(out, "mine=8\n") {
		t.Errorf("existing PROMPT_COMMAND not run on every prompt:\n%s", out)
	}
	// stderr only carries sync's one-line notices.
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if l != "" && !strings.HasPrefix(l, "envmgr: activated ") && !strings.HasPrefix(l, "envmgr: deactivated ") {
			t.Errorf("unexpected stderr line: %q", l)
		}
	}
}

func findPowerShell(t *testing.T) string {
	t.Helper()
	for _, n := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skip("powershell/pwsh not on PATH")
	return ""
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func TestRoundTripPowerShellHook(t *testing.T) {
	ps := findPowerShell(t)
	exe := testBinary(t)
	home := setupHome(t)
	E := psQuote(exe)
	script := `$ErrorActionPreference = 'Continue'
$env:ENVMGR_HOME = ` + psQuote(home) + `
$env:URL = 'orig'
$global:mine = 0
function global:prompt { $global:mine++; 'MYPROMPT> ' }
& ` + E + ` hook powershell | Out-String | Invoke-Expression
& ` + E + ` hook powershell | Out-String | Invoke-Expression
function s($n) { "${n}:$($env:URL):$($env:ENVMGR_PROFILE):$($env:ENVMGR_PINNED)" }
$p = prompt; "P0=$p"; s 0
& ` + E + ` select a 2>$null; $null = prompt; s 1
$global:LASTEXITCODE = 7; $p = prompt; "UNCHANGED=$p|lec=$LASTEXITCODE"
& ` + E + ` select b 2>$null; $null = prompt; s 2
& ` + E + ` use c --shell powershell 2>$null | Invoke-Expression; & ` + E + ` select a 2>$null; $null = prompt; s 3
& ` + E + ` follow --shell powershell 2>$null | Invoke-Expression; $null = prompt; s 4
& ` + E + ` select --none 2>$null; $null = prompt; s 5
& ` + E + ` use b --shell powershell 2>$null | Invoke-Expression; & ` + E + ` deactivate --shell powershell 2>$null | Invoke-Expression; & ` + E + ` select b 2>$null; $null = prompt; s 6
"mine=$global:mine"
`
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, r := range u {
		b[2*i], b[2*i+1] = byte(r), byte(r>>8)
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("powershell: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	out := strings.ReplaceAll(stdout.String(), "\r\n", "\n")
	checkSteps(t, out)
	if !strings.Contains(out, "P0=MYPROMPT> \n") || !strings.Contains(out, "UNCHANGED=MYPROMPT> |lec=7\n") {
		t.Errorf("prompt output or LASTEXITCODE not preserved:\n%s", out)
	}
	if !strings.Contains(out, "mine=8\n") {
		t.Errorf("original prompt not called once per prompt (hook wrapped twice?):\n%s", out)
	}
}
