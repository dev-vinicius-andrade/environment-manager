package selected

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

func newSelector(t *testing.T, profiles map[string]map[string]string) *Selector {
	t.Helper()
	st := &profile.Store{Dir: t.TempDir()}
	for name, vars := range profiles {
		if err := st.Save(profile.Profile{Name: name, Vars: vars}); err != nil {
			t.Fatal(err)
		}
	}
	return For(st)
}

func TestGetMissingIsNone(t *testing.T) {
	s := newSelector(t, nil)
	sel, err := s.Get()
	if err != nil || sel.Profile != "" {
		t.Fatalf("Get = %+v, %v", sel, err)
	}
}

func TestSetGetClear(t *testing.T) {
	s := newSelector(t, map[string]map[string]string{"dev": {"A": "1"}})
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	if err := s.Set("dev"); err != nil {
		t.Fatal(err)
	}
	sel, err := s.Get()
	if err != nil || sel.Profile != "dev" || !sel.UpdatedAt.Equal(fixed) {
		t.Fatalf("Get = %+v, %v", sel, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if sel, err := s.Get(); err != nil || sel.Profile != "" {
		t.Fatalf("after Clear: %+v, %v", sel, err)
	}
	// The file is still there after Clear (watchers see a change, not a removal).
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatal(err)
	}
}

func TestSetValidation(t *testing.T) {
	s := newSelector(t, map[string]map[string]string{"dev": {}})
	for _, bad := range []string{"", "../x", "a b", "-x", "a;rm", strings.Repeat("a", 65)} {
		if err := s.Set(bad); err == nil || !strings.Contains(err.Error(), "invalid profile name") {
			t.Errorf("Set(%q) err = %v", bad, err)
		}
	}
	if err := s.Set("missing"); err == nil || !strings.Contains(err.Error(), `profile "missing" not found`) {
		t.Fatalf("missing profile: %v", err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("failed Set wrote the file: %v", err)
	}
}

func TestGetRejectsTamperedFile(t *testing.T) {
	s := newSelector(t, nil)
	if err := os.WriteFile(s.Path(), []byte(`{"profile":"x'; rm -rf /"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(); err == nil || !strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(s.Path(), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(); err == nil {
		t.Fatal("corrupt file accepted")
	}
}

func TestWriteIsAtomic(t *testing.T) {
	s := newSelector(t, map[string]map[string]string{"a": {}, "b": {}})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 1)
	// A reader keeps parsing the file while writers replace it: it must never
	// see a partial file.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := s.Get(); err != nil {
				select {
				case errs <- err:
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 100; i++ {
		name := "a"
		if i%2 == 1 {
			name = "b"
		}
		if err := s.Set(name); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-errs:
		t.Fatalf("reader saw a partial file: %v", err)
	default:
	}
	ents, _ := os.ReadDir(filepath.Dir(s.Path()))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(s.Path())
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", fi.Mode(), err)
		}
	}
}

func TestDecideTable(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want Action
		prof string
		note bool
	}{
		{"pinned ignores selection", Input{Selection: "b", Exists: true, Current: "a", Active: true, Pinned: true}, NoChange, "", false},
		{"pinned ignores cleared selection", Input{Current: "a", Active: true, Pinned: true}, NoChange, "", false},
		{"unchanged", Input{Selection: "a", Exists: true, Current: "a", Active: true}, NoChange, "", false},
		{"switch", Input{Selection: "b", Exists: true, Current: "a", Active: true}, Activate, "b", false},
		{"first activation", Input{Selection: "a", Exists: true}, Activate, "a", false},
		{"cleared with active", Input{Current: "a", Active: true}, Deactivate, "", false},
		{"cleared with nothing active", Input{}, NoChange, "", false},
		{"deleted selection, active", Input{Selection: "a", Current: "a", Active: true}, Deactivate, "", true},
		{"deleted selection, nothing active", Input{Selection: "a"}, NoChange, "", true},
	}
	for _, c := range cases {
		d := Decide(c.in)
		if d.Action != c.want || d.Profile != c.prof || (d.Note != "") != c.note {
			t.Errorf("%s: got %+v, want %v %q note=%v", c.name, d, c.want, c.prof, c.note)
		}
	}
}

var allShells = []shell.Kind{shell.Bash, shell.Zsh, shell.Fish, shell.PowerShell, shell.Cmd}

// simulate runs Sync against env and applies the resulting ops to a copy of env.
func simulate(t *testing.T, s *Selector, env map[string]string, follow bool) (map[string]string, Result) {
	t.Helper()
	var first Result
	var out map[string]string
	for i, k := range allShells {
		res, err := s.Sync(k, shell.MapLookup(env), follow)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if i == 0 {
			first = res
		} else if res.Decision != first.Decision || (res.Code == "") != (first.Code == "") {
			t.Fatalf("%s decided %+v (code %q), bash decided %+v", k, res.Decision, res.Code, first.Decision)
		}
	}
	// Recompute the ops the same way Sync does to apply them.
	out = map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	lookup := shell.MapLookup(env)
	switch first.Decision.Action {
	case Activate:
		p, err := s.Profiles.Get(first.Decision.Profile)
		if err != nil {
			t.Fatal(err)
		}
		ops, err := shell.FollowUseOps(lookup, p)
		if err != nil {
			t.Fatal(err)
		}
		shell.Apply(out, ops)
	case Deactivate:
		shell.Apply(out, shell.DeactivateOps(lookup))
	default:
		if follow {
			shell.Apply(out, shell.UnpinOps(lookup))
		}
	}
	return out, first
}

func use(t *testing.T, s *Selector, env map[string]string, name string) map[string]string {
	t.Helper()
	p, err := s.Profiles.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := shell.UseOps(shell.MapLookup(env), p)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	shell.Apply(out, ops)
	return out
}

func TestSyncScenario(t *testing.T) {
	s := newSelector(t, map[string]map[string]string{
		"a": {"URL": "a"}, "b": {"URL": "b"}, "c": {"URL": "c", "ONLY_C": "1"},
	})
	env := map[string]string{"URL": "orig"}

	// Nothing selected yet: nothing to do.
	env, res := simulate(t, s, env, false)
	if res.Code != "" || res.Decision.Action != NoChange {
		t.Fatalf("no selection: %+v", res)
	}

	// select a -> first activation, not pinned.
	must(t, s.Set("a"))
	env, res = simulate(t, s, env, false)
	if res.Decision.Action != Activate || env["URL"] != "a" || env[shell.EnvProfile] != "a" {
		t.Fatalf("select a: %+v env=%v", res, env)
	}
	if _, ok := env[shell.EnvPinned]; ok {
		t.Fatalf("sync pinned the session: %v", env)
	}
	// Same selection again: nothing.
	if _, res = simulate(t, s, env, false); res.Code != "" {
		t.Fatalf("unchanged emitted %q", res.Code)
	}

	// select b -> switch.
	must(t, s.Set("b"))
	env, _ = simulate(t, s, env, false)
	if env["URL"] != "b" || env[shell.EnvProfile] != "b" || env["ENVMGR_PREV_URL"] != "orig" {
		t.Fatalf("switch to b: %v", env)
	}

	// use c manually -> pinned; selection changes are ignored.
	env = use(t, s, env, "c")
	if env[shell.EnvPinned] != "1" {
		t.Fatalf("use did not pin: %v", env)
	}
	must(t, s.Set("a"))
	if env2, res := simulate(t, s, env, false); res.Code != "" || !reflect.DeepEqual(env2, env) {
		t.Fatalf("pinned session changed: %+v", res)
	}
	must(t, s.Clear())
	if _, res := simulate(t, s, env, false); res.Code != "" {
		t.Fatalf("pinned session deactivated: %+v", res)
	}

	// follow with no selection: deactivates and unpins.
	env, res = simulate(t, s, env, true)
	if res.Decision.Action != Deactivate || env["URL"] != "orig" || len(env) != 1 {
		t.Fatalf("follow with no selection: %+v env=%v", res, env)
	}

	// pin on c again, select a, follow -> a, unpinned.
	env = use(t, s, env, "c")
	must(t, s.Set("a"))
	env, _ = simulate(t, s, env, true)
	if env["URL"] != "a" || env["ONLY_C"] != "" || env[shell.EnvProfile] != "a" {
		t.Fatalf("follow to a: %v", env)
	}
	if _, ok := env[shell.EnvPinned]; ok {
		t.Fatalf("follow kept the pin: %v", env)
	}

	// follow when already on the selection only clears the pin.
	env = use(t, s, env, "a")
	env, res = simulate(t, s, env, true)
	if res.Decision.Action != NoChange || env[shell.EnvPinned] != "" || env[shell.EnvProfile] != "a" {
		t.Fatalf("follow on same profile: %+v %v", res, env)
	}
	if _, ok := env[shell.EnvPinned]; ok {
		t.Fatalf("pin not cleared: %v", env)
	}

	// The selected profile is deleted -> deactivate with a note.
	must(t, s.Profiles.Delete("a"))
	env, res = simulate(t, s, env, false)
	if res.Decision.Action != Deactivate || res.Decision.Note == "" || env["URL"] != "orig" {
		t.Fatalf("deleted profile: %+v env=%v", res, env)
	}

	// select --none with a profile active (not pinned) -> deactivate.
	must(t, s.Set("b"))
	env, _ = simulate(t, s, env, false)
	must(t, s.Clear())
	env, res = simulate(t, s, env, false)
	if res.Decision.Action != Deactivate || env["URL"] != "orig" || len(env) != 1 {
		t.Fatalf("cleared: %+v env=%v", res, env)
	}
}

func TestSyncRenderErrorLeavesNoCode(t *testing.T) {
	s := newSelector(t, map[string]map[string]string{"nl": {"X": "a\nb"}})
	must(t, s.Set("nl"))
	res, err := s.Sync(shell.Cmd, shell.MapLookup(map[string]string{}), false)
	if err == nil || res.Code != "" {
		t.Fatalf("cmd with newline: %+v %v", res, err)
	}
	// Other shells are fine.
	if res, err := s.Sync(shell.PowerShell, shell.MapLookup(map[string]string{}), false); err != nil || res.Code == "" {
		t.Fatalf("powershell: %+v %v", res, err)
	}
}

func TestSyncEmitsNoPinSetForAllShells(t *testing.T) {
	s := newSelector(t, map[string]map[string]string{"a": {"X": "1"}})
	must(t, s.Set("a"))
	want := map[shell.Kind]string{
		shell.Bash:       "unset ENVMGR_PINNED\n",
		shell.Zsh:        "unset ENVMGR_PINNED\n",
		shell.Fish:       "set -e ENVMGR_PINNED\n",
		shell.PowerShell: "$env:ENVMGR_PINNED = $null\n",
		shell.Cmd:        "@set ENVMGR_PINNED=\n",
	}
	for k, w := range want {
		res, err := s.Sync(k, shell.MapLookup(map[string]string{}), false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(res.Code, w) || strings.Contains(res.Code, "ENVMGR_PINNED=1") || strings.Contains(res.Code, "ENVMGR_PINNED '1'") || strings.Contains(res.Code, "ENVMGR_PINNED = '1'") || strings.Contains(res.Code, "ENVMGR_PINNED='1'") {
			t.Errorf("%s: %q", k, res.Code)
		}
		use, _ := shell.Use(k, shell.MapLookup(map[string]string{}), profile.Profile{Name: "a", Vars: map[string]string{"X": "1"}})
		if !strings.Contains(use, "ENVMGR_PINNED") || strings.HasSuffix(use, w) {
			t.Errorf("%s: use does not pin: %q", k, use)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestScopesAndOldFileCompat(t *testing.T) {
	s := newSelector(t, map[string]map[string]string{"a": {"URL": "a"}})
	// A file written before scopes existed means environment only.
	must(t, os.WriteFile(s.Path(), []byte(`{"profile":"a","updatedAt":"2026-01-02T03:04:05Z"}`), 0o600))
	sel, err := s.Get()
	if err != nil || sel.Profile != "a" || !sel.Env || sel.Hosts || sel.EnvProfile() != "a" || sel.HostsProfile() != "" {
		t.Fatalf("old file: %+v %v", sel, err)
	}
	// An old "none" file has no scopes.
	must(t, os.WriteFile(s.Path(), []byte(`{"profile":"","updatedAt":"2026-01-02T03:04:05Z"}`), 0o600))
	if sel, _ := s.Get(); sel.Env || sel.Hosts || sel.EnvProfile() != "" {
		t.Fatalf("old none: %+v", sel)
	}

	must(t, s.SetScoped("a", Scopes{Env: true, Hosts: true}))
	if sel, _ := s.Get(); !sel.Env || !sel.Hosts || sel.HostsProfile() != "a" {
		t.Fatalf("both: %+v", sel)
	}
	b, _ := os.ReadFile(s.Path())
	if !strings.Contains(string(b), `"env": true`) || !strings.Contains(string(b), `"hosts": true`) {
		t.Fatalf("file %s", b)
	}
	if err := s.SetScoped("a", Scopes{}); err == nil {
		t.Fatal("empty scopes accepted")
	}
	if err := s.SetScoped("missing", Scopes{Hosts: true}); err == nil {
		t.Fatal("missing profile accepted")
	}

	// Hosts only: following shells treat it as no selection (sync never
	// touches hosts) and deactivate a profile they got from the selection.
	env := map[string]string{"URL": "orig"}
	must(t, s.Set("a"))
	env, _ = simulate(t, s, env, false)
	if env["URL"] != "a" {
		t.Fatalf("env scope: %v", env)
	}
	must(t, s.SetScoped("a", Scopes{Hosts: true}))
	env, res := simulate(t, s, env, false)
	if res.Decision.Action != Deactivate || env["URL"] != "orig" || !res.Selection.Hosts {
		t.Fatalf("hosts only: %+v env=%v", res, env)
	}
	if _, res := simulate(t, s, env, false); res.Code != "" {
		t.Fatalf("hosts only emitted code: %q", res.Code)
	}
}
