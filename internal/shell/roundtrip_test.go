package shell

// Round trip tests: the emitted code is evaluated by a real shell, the way the
// user would (eval "$(...)", ... | Invoke-Expression, ... | source, a .cmd
// file), and the resulting environment is compared with the expected one.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// runner evaluates a prelude and code chunks in a fresh shell and returns the
// tracked variables that are set afterwards.
type runner func(t *testing.T, chunks []string, names []string) map[string]string

type roundTrip struct {
	kind Kind
	// prelude sets ORIG and KEEP and clears every other tracked name.
	prelude func(clear []string) string
	run     runner
	// emptyIsUnset: setting a variable to "" removes it (Windows shells).
	emptyIsUnset bool
	// accepts filters payloads the shell cannot represent (cmd: ASCII only).
	accepts func(string) bool
}

func runRoundTrip(t *testing.T, rt roundTrip) {
	payloads := []string{}
	for _, p := range injections {
		if rt.accepts == nil || rt.accepts(p) {
			payloads = append(payloads, p)
		}
	}
	pa := profile.Profile{Name: "a", Vars: map[string]string{"ORIG": "a-orig", "SHARED": "a"}}
	for i, p := range payloads {
		pa.Vars[fmt.Sprintf("V%d", i)] = p
	}
	pb := profile.Profile{Name: "b", Vars: map[string]string{"SHARED": payloads[0], "NEWB": "b value"}}

	names := []string{"ORIG", "KEEP", "SHARED", "NEWB", "PWNED", EnvProfile, EnvVars, EnvPinned}
	for n := range pa.Vars {
		if n != "ORIG" && n != "SHARED" {
			names = append(names, n)
		}
	}
	for _, n := range append([]string{}, names...) {
		names = append(names, PrevPrefix+n)
	}
	sort.Strings(names)
	clear := []string{}
	for _, n := range names {
		if n != "ORIG" && n != "KEEP" {
			clear = append(clear, n)
		}
	}

	normalize := func(m map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range m {
			if rt.emptyIsUnset && v == "" {
				continue
			}
			out[k] = v
		}
		return out
	}
	initial := map[string]string{"ORIG": "orig value", "KEEP": "keep"}
	chunks := []string{rt.prelude(clear)}

	env := rt.run(t, chunks, names)
	if !reflect.DeepEqual(env, initial) {
		t.Fatalf("prelude: got %v want %v", env, initial)
	}

	step := func(label string, ops []Op) map[string]string {
		t.Helper()
		code, err := Render(rt.kind, ops)
		if err != nil {
			t.Fatalf("%s: render: %v", label, err)
		}
		want := map[string]string{}
		for k, v := range env {
			want[k] = v
		}
		Apply(want, ops)
		want = normalize(want)
		chunks = append(chunks, code)
		got := rt.run(t, chunks, names)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s:\n got %q\nwant %q\ncode:\n%s", label, got, want, code)
		}
		return got
	}
	opsA, err := UseOps(MapLookup(env), pa)
	if err != nil {
		t.Fatal(err)
	}
	env = step("use a", opsA)
	if env["V0"] != payloads[0] || env[EnvProfile] != "a" {
		t.Fatalf("use a did not apply payload: %q", env)
	}
	opsB, err := UseOps(MapLookup(env), pb)
	if err != nil {
		t.Fatal(err)
	}
	env = step("use b", opsB)
	if env["ORIG"] != "orig value" || env["SHARED"] != payloads[0] || env[EnvProfile] != "b" {
		t.Fatalf("switch a->b wrong: %q", env)
	}
	env = step("deactivate", DeactivateOps(MapLookup(env)))
	if !reflect.DeepEqual(env, initial) {
		t.Fatalf("deactivate: got %q want %q", env, initial)
	}
}

func filterNames(all map[string]string, names []string) map[string]string {
	out := map[string]string{}
	for _, n := range names {
		if v, ok := all[n]; ok {
			out[n] = v
		}
	}
	return out
}

// parseNulEnv parses `env -0` output.
func parseNulEnv(b []byte) map[string]string {
	out := map[string]string{}
	for _, e := range strings.Split(string(b), "\x00") {
		if i := strings.IndexByte(e, '='); i > 0 {
			out[e[:i]] = e[i+1:]
		}
	}
	return out
}

func probe(t *testing.T, path string, stdin string, args ...string) {
	t.Helper()
	cmd := exec.Command(path, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Skipf("%s is not usable: %v %q", path, err, out)
	}
}

func runCaptured(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s failed: %v\nstderr: %s", cmd.Path, err, stderr.String())
	}
	if s := strings.TrimSpace(stderr.String()); s != "" && !onlyPowerShellProgress(s) {
		t.Fatalf("%s wrote to stderr: %s", cmd.Path, s)
	}
	return stdout.Bytes()
}

// onlyPowerShellProgress reports whether s is a CLIXML stream that holds
// nothing but progress records (Windows PowerShell's "Preparing modules for
// first use" when stderr is redirected).
func onlyPowerShellProgress(s string) bool {
	if !strings.HasPrefix(s, "#< CLIXML") {
		return false
	}
	return strings.Count(s, `<Obj S="`) == strings.Count(s, `<Obj S="progress"`)
}

func TestRoundTripBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	probe(t, bash, "echo ok\n", "-s")
	runRoundTrip(t, roundTrip{
		kind: Bash,
		prelude: func(clear []string) string {
			return "export ORIG='orig value' KEEP=keep\nunset " + strings.Join(clear, " ") + "\n"
		},
		run: func(t *testing.T, chunks []string, names []string) map[string]string {
			var s strings.Builder
			s.WriteString("set -u\n" + chunks[0])
			for _, c := range chunks[1:] {
				// Same as eval "$(envmgr use x)": eval the captured output.
				s.WriteString("eval \"$(cat <<'ENVMGR_EOF'\n" + c + "ENVMGR_EOF\n)\"\n")
			}
			s.WriteString("env -0\n")
			cmd := exec.Command(bash, "-s")
			cmd.Stdin = strings.NewReader(s.String())
			return filterNames(parseNulEnv(runCaptured(t, cmd)), names)
		},
	})
}

func TestRoundTripFish(t *testing.T) {
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish not on PATH")
	}
	probe(t, fish, "echo ok\n", "--no-config")
	runRoundTrip(t, roundTrip{
		kind: Fish,
		prelude: func(clear []string) string {
			return "set -gx ORIG 'orig value'\nset -gx KEEP keep\nset -e " + strings.Join(clear, " ") + "\ntrue\n"
		},
		run: func(t *testing.T, chunks []string, names []string) map[string]string {
			dir := t.TempDir()
			var s strings.Builder
			s.WriteString(chunks[0])
			for i, c := range chunks[1:] {
				f := filepath.Join(dir, fmt.Sprintf("step%d.fish", i))
				if err := os.WriteFile(f, []byte(c), 0o600); err != nil {
					t.Fatal(err)
				}
				// Same as envmgr use x | source.
				s.WriteString("cat " + FishQuote(f) + " | source\n")
			}
			s.WriteString("command env -0\n")
			cmd := exec.Command(fish, "--no-config")
			cmd.Stdin = strings.NewReader(s.String())
			return filterNames(parseNulEnv(runCaptured(t, cmd)), names)
		},
	})
}

func findPowerShell() (string, bool) {
	for _, n := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}

func psEncoded(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestRoundTripPowerShell(t *testing.T) {
	ps, ok := findPowerShell()
	if !ok {
		t.Skip("powershell/pwsh not on PATH")
	}
	runRoundTrip(t, roundTrip{
		kind:         PowerShell,
		emptyIsUnset: runtime.GOOS == "windows",
		prelude: func(clear []string) string {
			var s strings.Builder
			s.WriteString("$env:ORIG = 'orig value'\n$env:KEEP = 'keep'\n")
			for _, n := range clear {
				s.WriteString("$env:" + n + " = $null\n")
			}
			return s.String()
		},
		run: func(t *testing.T, chunks []string, names []string) map[string]string {
			dir := t.TempDir()
			var s strings.Builder
			s.WriteString("$ErrorActionPreference = 'Stop'\n$ProgressPreference = 'SilentlyContinue'\n" + chunks[0])
			for i, c := range chunks[1:] {
				f := filepath.Join(dir, fmt.Sprintf("step%d.ps1", i))
				if err := os.WriteFile(f, []byte(c), 0o600); err != nil {
					t.Fatal(err)
				}
				// Same as envmgr use x | Invoke-Expression: one line at a time.
				s.WriteString("Get-Content -LiteralPath '" + strings.ReplaceAll(f, "'", "''") + "' | Invoke-Expression\n")
			}
			s.WriteString("foreach ($n in @('" + strings.Join(names, "','") + "')) {\n" +
				"  $v = [Environment]::GetEnvironmentVariable($n)\n" +
				"  if ($null -ne $v) { $n + '=' + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($v)) }\n" +
				"}\n")
			cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-EncodedCommand", psEncoded(s.String()))
			out := map[string]string{}
			for _, l := range strings.Split(string(runCaptured(t, cmd)), "\n") {
				l = strings.TrimSpace(l)
				if i := strings.IndexByte(l, '='); i > 0 {
					v, err := base64.StdEncoding.DecodeString(l[i+1:])
					if err != nil {
						t.Fatalf("bad dump line %q", l)
					}
					out[l[:i]] = string(v)
				}
			}
			return out
		},
	})
}

func TestRoundTripCmd(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd only on windows")
	}
	cmdExe, err := exec.LookPath("cmd")
	if err != nil {
		t.Skip("cmd not on PATH")
	}
	runRoundTrip(t, roundTrip{
		kind:         Cmd,
		emptyIsUnset: true,
		accepts:      func(s string) bool { _, err := CmdQuote(s); return err == nil },
		prelude: func(clear []string) string {
			var s strings.Builder
			s.WriteString("@set ORIG=orig value\r\n@set KEEP=keep\r\n")
			for _, n := range clear {
				s.WriteString("@set " + n + "=\r\n")
			}
			return s.String()
		},
		run: func(t *testing.T, chunks []string, names []string) map[string]string {
			dir := t.TempDir()
			var s strings.Builder
			s.WriteString("@echo off\r\n" + chunks[0])
			for i, c := range chunks[1:] {
				f := filepath.Join(dir, fmt.Sprintf("step%d.cmd", i))
				if err := os.WriteFile(f, []byte(c), 0o600); err != nil {
					t.Fatal(err)
				}
				// Same as saving envmgr use x --shell cmd to a file and running it.
				s.WriteString("call \"" + f + "\"\r\n")
			}
			s.WriteString("set\r\n")
			main := filepath.Join(dir, "main.cmd")
			if err := os.WriteFile(main, []byte(s.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(cmdExe, "/d", "/c", main)
			all := map[string]string{}
			for _, l := range strings.Split(string(runCaptured(t, cmd)), "\n") {
				l = strings.TrimRight(l, "\r")
				if i := strings.IndexByte(l, '='); i > 0 {
					all[strings.ToUpper(l[:i])] = l[i+1:]
				}
			}
			upper := make([]string, len(names))
			for i, n := range names {
				upper[i] = strings.ToUpper(n)
			}
			return filterNames(all, upper)
		},
	})
}
