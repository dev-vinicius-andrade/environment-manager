package hostsapply

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/selected"
)

func setup(t *testing.T, content string) (*profile.Store, string) {
	t.Helper()
	st := &profile.Store{Dir: t.TempDir()}
	hf := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hf, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, st.SetHost("a", "10.0.0.1", "api.a.local", "db.a.local"))
	must(t, st.SetHost("a", "10.0.0.2", "web.a.local"))
	must(t, st.SetHost("b", "10.0.1.1", "api.b.local"))
	must(t, st.SetVar("b", "B_URL", "x"))
	return st, hf
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyReplaceUnapplyPreservesUnmanaged(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		name := map[string]string{"\n": "LF", "\r\n": "CRLF"}[eol]
		t.Run(name, func(t *testing.T) {
			before := "127.0.0.1 localhost" + eol + "# keep  this   spacing" + eol + eol + "10.9.9.9 printer.lan\t# tab" + eol
			after := eol + "# trailing user line" + eol
			blockUser := hosts.BeginMarker + eol + "# group: mine" + eol + "10.5.5.5 mine.local" + eol + hosts.EndMarker
			original := before + blockUser + after
			st, hf := setup(t, original)

			// Dry run: diff only.
			ch, err := Apply(st, hf, "a", true)
			if err != nil || !ch.Changed || !strings.Contains(ch.Diff, "+# group: profile:a") || ch.Backup != "" {
				t.Fatalf("dry run %+v %v", ch, err)
			}
			if read(t, hf) != original {
				t.Fatal("dry run wrote")
			}
			if s, _ := ReadState(st.Dir); s.Profile != "" {
				t.Fatal("dry run recorded state")
			}

			ch, err = Apply(st, hf, "a", false)
			if err != nil || !ch.Changed || ch.Backup == "" {
				t.Fatalf("apply %+v %v", ch, err)
			}
			got := read(t, hf)
			wantBlock := hosts.BeginMarker + eol + "# Managed by envmgr (envmgr hosts). Lines between BEGIN and END are rewritten by envmgr." + eol +
				"# group: mine" + eol + "10.5.5.5 mine.local" + eol +
				"# group: profile:a" + eol + "10.0.0.1 api.a.local db.a.local" + eol + "10.0.0.2 web.a.local" + eol + hosts.EndMarker
			if got != before+wantBlock+after {
				t.Fatalf("after apply a:\n%q\nwant\n%q", got, before+wantBlock+after)
			}
			if s, _ := ReadState(st.Dir); s.Profile != "a" || s.File == "" {
				t.Fatalf("state %+v", s)
			}
			// Applying again changes nothing and writes no backup.
			if ch, err := Apply(st, hf, "a", false); err != nil || ch.Changed {
				t.Fatalf("re-apply %+v %v", ch, err)
			}

			// B replaces A in place.
			if _, err := Apply(st, hf, "b", false); err != nil {
				t.Fatal(err)
			}
			got = read(t, hf)
			if strings.Contains(got, "profile:a") || strings.Contains(got, "api.a.local") || !strings.Contains(got, "# group: profile:b"+eol+"10.0.1.1 api.b.local"+eol+hosts.EndMarker) {
				t.Fatalf("after apply b: %q", got)
			}
			if !strings.HasPrefix(got, before) || !strings.HasSuffix(got, after) || !strings.Contains(got, "10.5.5.5 mine.local") {
				t.Fatalf("unmanaged or user lines changed: %q", got)
			}
			if s, _ := ReadState(st.Dir); s.Profile != "b" {
				t.Fatalf("state %+v", s)
			}

			_, was, err := Unapply(st.Dir, hf, false)
			if err != nil || len(was) != 1 || was[0] != "b" {
				t.Fatalf("unapply %v %v", was, err)
			}
			got = read(t, hf)
			if strings.Contains(got, "profile:") || !strings.HasPrefix(got, before) || !strings.HasSuffix(got, after) {
				t.Fatalf("after unapply %q", got)
			}
			if s, _ := ReadState(st.Dir); s.Profile != "" {
				t.Fatalf("state after unapply %+v", s)
			}
			// Unapply with nothing applied is a no-op.
			if ch, was, err := Unapply(st.Dir, hf, false); err != nil || ch.Changed || len(was) != 0 {
				t.Fatalf("second unapply %+v %v %v", ch, was, err)
			}
		})
	}
}

func TestApplyNewBlockAndNoFinalNewline(t *testing.T) {
	const original = "127.0.0.1 localhost\r\n10.1.1.1 printer.lan"
	st, hf := setup(t, original)
	must(t, func() error { _, err := Apply(st, hf, "b", false); return err }())
	got := read(t, hf)
	if !strings.HasPrefix(got, original+"\r\n"+hosts.BeginMarker+"\r\n") || strings.HasSuffix(got, "\n") {
		t.Fatalf("%q", got)
	}
	_, _, err := Unapply(st.Dir, hf, false)
	must(t, err)
	if got := read(t, hf); !strings.HasPrefix(got, original+"\r\n") {
		t.Fatalf("%q", got)
	}
}

func TestApplyErrors(t *testing.T) {
	st, hf := setup(t, "127.0.0.1 localhost\n")
	if _, err := Apply(st, hf, "nope", false); !errors.Is(err, profile.ErrNotFound) {
		t.Fatalf("unknown profile: %v", err)
	}
	// A critical name in a hand-edited profile is refused at apply time.
	must(t, os.WriteFile(filepath.Join(st.Dir, "profiles", "evil.json"),
		[]byte(`{"name":"evil","vars":{},"hosts":[{"ip":"10.6.6.6","names":["localhost"]}]}`), 0o600))
	var ce *hosts.CriticalNameError
	if _, err := Apply(st, hf, "evil", false); !errors.As(err, &ce) {
		t.Fatalf("critical: %v", err)
	}
	if read(t, hf) != "127.0.0.1 localhost\n" {
		t.Fatal("file changed")
	}
	if _, err := Apply(st, filepath.Join(t.TempDir(), "missing"), "a", false); err == nil {
		t.Fatal("missing hosts file accepted")
	}
	// A profile without hosts entries only removes the previous profile group.
	must(t, st.SetVar("envonly", "A", "1"))
	must(t, func() error { _, err := Apply(st, hf, "a", false); return err }())
	must(t, func() error { _, err := Apply(st, hf, "envonly", false); return err }())
	if got := read(t, hf); strings.Contains(got, "profile:") {
		t.Fatalf("%q", got)
	}
	if s, _ := ReadState(st.Dir); s.Profile != "envonly" {
		t.Fatalf("%+v", s)
	}
}

func TestApplyPermissionDenied(t *testing.T) {
	st, hf := setup(t, "127.0.0.1 localhost\n")
	dir := filepath.Dir(hf)
	_ = os.Chmod(hf, 0o444)
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, 0o555)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.Chmod(hf, 0o644)
	})
	_, err := Apply(st, hf, "a", false)
	if err == nil {
		t.Skip("this OS/user ignores read-only permissions")
	}
	var pe *hosts.PermissionError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "elevated") {
		t.Fatalf("%v", err)
	}
	if s, _ := ReadState(st.Dir); s.Profile != "" {
		t.Fatal("failed apply recorded state")
	}
}

func TestStateFile(t *testing.T) {
	home := t.TempDir()
	if s, err := ReadState(home); err != nil || s.Profile != "" {
		t.Fatalf("%+v %v", s, err)
	}
	must(t, writeState(home, State{Profile: "dev", File: "x"}))
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(StatePath(home)); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode())
		}
	}
	must(t, os.WriteFile(StatePath(home), []byte(`{"profile":"../x"}`), 0o600))
	if _, err := ReadState(home); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestParseScope(t *testing.T) {
	cases := []struct {
		hosts bool
		only  string
		want  Scope
		err   bool
	}{
		{false, "", Scope{Env: true}, false},
		{true, "", Scope{Env: true, Hosts: true}, false},
		{false, "env", Scope{Env: true}, false},
		{false, "hosts", Scope{Hosts: true}, false},
		{false, "all", Scope{Env: true, Hosts: true}, false},
		{true, "hosts", Scope{}, true},
		{false, "both", Scope{}, true},
	}
	for _, c := range cases {
		got, err := ParseScope(c.hosts, c.only)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("ParseScope(%v, %q) = %+v, %v", c.hosts, c.only, got, err)
		}
	}
}

func TestSyncHintOncePerSelection(t *testing.T) {
	st, hf := setup(t, "127.0.0.1 localhost\n")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sel := selected.Selection{Profile: "a", Env: true, Hosts: true, UpdatedAt: at}
	h := SyncHint(st.Dir, sel)
	if !strings.Contains(h, `envmgr hosts apply a`) || !strings.Contains(h, "elevated") {
		t.Fatalf("hint %q", h)
	}
	if h := SyncHint(st.Dir, sel); h != "" {
		t.Fatalf("hint repeated: %q", h)
	}
	sel.UpdatedAt = at.Add(time.Second) // a new selection change
	if h := SyncHint(st.Dir, sel); h == "" {
		t.Fatal("no hint after a selection change")
	}
	if h := SyncHint(st.Dir, selected.Selection{Profile: "a", Env: true, UpdatedAt: at.Add(2 * time.Second)}); h != "" {
		t.Fatalf("hint without hosts scope: %q", h)
	}
	// Already applied: no hint.
	must(t, func() error { _, err := Apply(st, hf, "a", false); return err }())
	sel.UpdatedAt = at.Add(3 * time.Second)
	if h := SyncHint(st.Dir, sel); h != "" {
		t.Fatalf("hint while applied: %q", h)
	}
}
