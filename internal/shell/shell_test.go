package shell

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

func TestParse(t *testing.T) {
	cases := map[string]Kind{
		"bash": Bash, "sh": Bash, "ZSH": Zsh, "fish": Fish,
		"powershell": PowerShell, "pwsh": PowerShell, "cmd": Cmd, " cmd.exe ": Cmd,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Parse("tcsh"); err == nil {
		t.Error("Parse(tcsh) should fail")
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		goos, shell string
		want        Kind
	}{
		{"windows", "/usr/bin/bash", PowerShell},
		{"windows", "", PowerShell},
		{"linux", "/usr/bin/fish", Fish},
		{"darwin", "/bin/zsh", Zsh},
		{"linux", "/bin/bash", Bash},
		{"linux", "", Bash},
	}
	for _, c := range cases {
		if got := detect(c.goos, c.shell); got != c.want {
			t.Errorf("detect(%q,%q) = %q, want %q", c.goos, c.shell, got, c.want)
		}
	}
}

func TestReadStateDropsInvalidNames(t *testing.T) {
	env := map[string]string{
		EnvProfile: "dev",
		EnvVars:    "B,A,,A,bad name,$(rm -rf /),ENVMGR_PROFILE,envmgr_x,C_1",
	}
	st := ReadState(MapLookup(env))
	want := []string{"A", "B", "C_1"}
	if st.Profile != "dev" || !reflect.DeepEqual(st.Vars, want) {
		t.Fatalf("ReadState = %+v, want dev %v", st, want)
	}
}

func copyEnv(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func use(t *testing.T, env map[string]string, p profile.Profile) map[string]string {
	t.Helper()
	ops, err := UseOps(MapLookup(env), p)
	if err != nil {
		t.Fatal(err)
	}
	next := copyEnv(env)
	Apply(next, ops)
	return next
}

func deactivate(env map[string]string) map[string]string {
	next := copyEnv(env)
	Apply(next, DeactivateOps(MapLookup(env)))
	return next
}

func TestUseSwitchDeactivate(t *testing.T) {
	a := profile.Profile{Name: "a", Vars: map[string]string{"ORIG": "a-orig", "ONLY_A": "x", "SHARED": "a", "EMPTY_ORIG": "a"}}
	b := profile.Profile{Name: "b", Vars: map[string]string{"SHARED": "b", "ONLY_B": "y"}}
	initial := map[string]string{"ORIG": "original", "KEEP": "keep", "EMPTY_ORIG": ""}

	e1 := use(t, initial, a)
	wantE1 := map[string]string{
		"ORIG": "a-orig", "ONLY_A": "x", "SHARED": "a", "EMPTY_ORIG": "a", "KEEP": "keep",
		"ENVMGR_PREV_ORIG": "original", "ENVMGR_PREV_EMPTY_ORIG": "",
		EnvProfile: "a", EnvVars: "EMPTY_ORIG,ONLY_A,ORIG,SHARED",
	}
	if !reflect.DeepEqual(e1, wantE1) {
		t.Fatalf("after use a:\n got %v\nwant %v", e1, wantE1)
	}

	// Re-activating the same profile must keep the true original as backup.
	if again := use(t, e1, a); !reflect.DeepEqual(again, wantE1) {
		t.Fatalf("use a twice:\n got %v\nwant %v", again, wantE1)
	}

	e2 := use(t, e1, b)
	wantE2 := map[string]string{
		"ORIG": "original", "EMPTY_ORIG": "", "KEEP": "keep",
		"SHARED": "b", "ONLY_B": "y",
		EnvProfile: "b", EnvVars: "ONLY_B,SHARED",
	}
	if !reflect.DeepEqual(e2, wantE2) {
		t.Fatalf("after switch to b:\n got %v\nwant %v", e2, wantE2)
	}

	e3 := deactivate(e2)
	if !reflect.DeepEqual(e3, initial) {
		t.Fatalf("after deactivate:\n got %v\nwant %v", e3, initial)
	}

	// Deactivate straight from a also restores the initial environment.
	if got := deactivate(e1); !reflect.DeepEqual(got, initial) {
		t.Fatalf("deactivate from a:\n got %v\nwant %v", got, initial)
	}
	// Nothing active: no ops.
	if ops := DeactivateOps(MapLookup(initial)); len(ops) != 0 {
		t.Fatalf("deactivate with nothing active = %v", ops)
	}
}

func TestUseEmptyProfileClearsVars(t *testing.T) {
	a := profile.Profile{Name: "a", Vars: map[string]string{"X": "1"}}
	empty := profile.Profile{Name: "empty", Vars: map[string]string{}}
	e := use(t, use(t, map[string]string{}, a), empty)
	want := map[string]string{EnvProfile: "empty"}
	if !reflect.DeepEqual(e, want) {
		t.Fatalf("got %v want %v", e, want)
	}
}

func TestUseRejectsReservedAndInvalid(t *testing.T) {
	for _, n := range []string{"ENVMGR_PROFILE", "ENVMGR_PREV_X", "envmgr_vars"} {
		_, err := UseOps(MapLookup(nil), profile.Profile{Name: "p", Vars: map[string]string{n: "x"}})
		if err == nil {
			t.Errorf("UseOps accepted reserved %q", n)
		}
	}
	_, err := UseOps(MapLookup(nil), profile.Profile{Name: "p", Vars: map[string]string{"A;rm": "x"}})
	if err == nil {
		t.Error("UseOps accepted invalid var name")
	}
}

func TestRenderRejectsBadNamesAndNUL(t *testing.T) {
	for _, k := range []Kind{Bash, Zsh, Fish, PowerShell, Cmd} {
		if _, err := Render(k, []Op{{Name: "A=B; rm", Value: "x"}}); err == nil {
			t.Errorf("%s: invalid name accepted", k)
		}
		if _, err := Render(k, []Op{{Name: "A", Value: "x\x00y"}}); err == nil {
			t.Errorf("%s: NUL accepted", k)
		}
	}
	if _, err := Render("tcsh", nil); err == nil {
		t.Error("unknown shell accepted")
	}
}

func TestRenderExact(t *testing.T) {
	ops := []Op{
		{Name: "A", Value: `it's "x" $HOME ` + "`id` \\ ;&|"},
		{Name: "B", Unset: true},
		{Name: "C", Value: ""},
	}
	cases := map[Kind]string{
		Bash: "export A='it'\\''s \"x\" $HOME `id` \\ ;&|'\nunset B\nexport C=''\n",
		Zsh:  "export A='it'\\''s \"x\" $HOME `id` \\ ;&|'\nunset B\nexport C=''\n",
		Fish: "set -gx A 'it\\'s \"x\" $HOME `id` \\\\ ;&|'\nset -e B\nset -gx C ''\n",
		PowerShell: "$env:A = 'it''s \"x\" $HOME `id` \\ ;&|'\n" +
			"$env:B = $null\n$env:C = ''\n",
		Cmd: "@set A=it's ^\"x^\" $HOME `id` \\ ;^&^|\n@set B=\n@set C=\n",
	}
	for k, want := range cases {
		got, err := Render(k, ops)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if got != want {
			t.Errorf("%s:\n got %q\nwant %q", k, got, want)
		}
	}
}

func TestPowerShellQuote(t *testing.T) {
	cases := map[string]string{
		"":           "''",
		"a'b":        "'a''b'",
		"a\nb":       "'a' + [char]0x000A + 'b'",
		"\n":         "'' + [char]0x000A",
		"\u2019x":    "'' + [char]0x2019 + 'x'",
		"caf\u00e9":  "'caf' + [char]0x00E9",
		"\U0001F600": "'' + [char]0xD83D + [char]0xDE00",
		"$(calc)`n":  "'$(calc)`n'",
	}
	for in, want := range cases {
		if got := PowerShellQuote(in); got != want {
			t.Errorf("PowerShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCmdQuote(t *testing.T) {
	got, err := CmdQuote(`%PATH% "&calc&" ^ (x) <a >b |c !x!` + "\t")
	if err != nil {
		t.Fatal(err)
	}
	want := `%%PATH%% ^"^&calc^&^" ^^ ^(x^) ^<a ^>b ^|c !x!` + "\t"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	for _, bad := range []string{"a\nb", "a\rb", "caf\u00e9", "\x1a"} {
		if _, err := CmdQuote(bad); err == nil {
			t.Errorf("CmdQuote(%q) accepted", bad)
		}
	}
}

// injection payloads shared by the round trip tests.
var injections = []string{
	`'; export PWNED=1; echo '`,
	`$(export PWNED=1)`,
	"`export PWNED=1`",
	`"; export PWNED=1; "`,
	`\'; set -gx PWNED 1; echo \'`,
	`'; $env:PWNED='1'; '`,
	`$($env:PWNED='1')`,
	"x\n$env:PWNED='1'\n'",
	"\u2019; $env:PWNED='1'; \u2019",
	"a\nexport PWNED=1\nb",
	`"&set PWNED=1&"`,
	`%PATH% ^& set PWNED=1`,
	`C:\path\ends\with\`,
	"spaces  and\ttabs ",
	"caf\u00e9 \u2713 \U0001F600",
	"",
}

// Every rendered line must be a single statement of the expected form: the
// value can never terminate the statement early.
func TestRenderInjectionStaysOneStatementPerOp(t *testing.T) {
	for _, k := range []Kind{Bash, Fish, PowerShell} {
		for _, v := range injections {
			out, err := Render(k, []Op{{Name: "V", Value: v}})
			if err != nil {
				t.Fatalf("%s %q: %v", k, v, err)
			}
			if k == PowerShell && strings.Count(out, "\n") != 1 {
				t.Errorf("powershell output for %q spans lines: %q", v, out)
			}
			if k == PowerShell {
				for _, r := range out {
					if r > 0x7e {
						t.Errorf("powershell output for %q is not ASCII: %q", v, out)
						break
					}
				}
			}
		}
	}
}
