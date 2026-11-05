package hosts

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ihosts "github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// Never touch the real hosts file from these tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "envmgr-hosts-cmd-test")
	if err != nil {
		panic(err)
	}
	os.Setenv(ihosts.EnvFile, filepath.Join(dir, "no-such-hosts"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "envmgr", SilenceErrors: true}
	CreateCommand(&context.EnvContext{}, root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func mustRun(t *testing.T, args ...string) (string, string) {
	t.Helper()
	out, stderr, err := run(t, args...)
	if err != nil {
		t.Fatalf("%v: %v (%s)", args, err, stderr)
	}
	return out, stderr
}

func TestHostsCommands(t *testing.T) {
	const original = "127.0.0.1 localhost\r\n# keep me\r\n"
	p := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ihosts.EnvFile, p)

	if out, _ := mustRun(t, "hosts", "path"); strings.TrimSpace(out) != p {
		t.Fatalf("path %q", out)
	}
	other := filepath.Join(t.TempDir(), "other")
	if out, _ := mustRun(t, "hosts", "path", "--file", other); strings.TrimSpace(out) != other {
		t.Fatalf("path --file %q", out)
	}

	out, stderr := mustRun(t, "hosts", "add", "10.0.0.5", "api.dev.local", "--group", "dev", "--dry-run")
	if !strings.Contains(out, "+10.0.0.5 api.dev.local") || !strings.Contains(stderr, "dry run") {
		t.Fatalf("dry run %q %q", out, stderr)
	}
	if b, _ := os.ReadFile(p); string(b) != original {
		t.Fatal("dry run wrote")
	}
	if out, _ := mustRun(t, "hosts", "add", "10.0.0.5", "web.dev.local", "--show-diff"); !strings.Contains(out, "+10.0.0.5 web.dev.local") {
		t.Fatalf("show-diff %q", out)
	}

	_, stderr = mustRun(t, "hosts", "add", "10.0.0.5", "api.dev.local", "db.dev.local", "--group", "dev")
	if !strings.Contains(stderr, "updated") || !strings.Contains(stderr, ihosts.BackupInfix) {
		t.Fatalf("add %q", stderr)
	}
	if _, stderr = mustRun(t, "hosts", "add", "10.0.0.5", "api.dev.local", "--group", "dev"); !strings.Contains(stderr, "no change") {
		t.Fatalf("idempotent add %q", stderr)
	}
	if _, _, err := run(t, "hosts", "add", "10.0.0.5", "localhost"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("critical name: %v", err)
	}
	if _, _, err := run(t, "hosts", "add", "10.0.0.5", "a#b"); err == nil {
		t.Fatal("bad hostname accepted")
	}
	mustRun(t, "hosts", "group", "disable", "dev")
	out, _ = mustRun(t, "hosts", "list", "--all")
	if !strings.Contains(out, "dev") || !strings.Contains(out, "disabled") || !strings.Contains(out, "api.dev.local db.dev.local") || !strings.Contains(out, "# keep me") {
		t.Fatalf("list %q", out)
	}
	if _, stderr = mustRun(t, "hosts", "add", "10.0.0.6", "x.dev.local", "--group", "dev"); !strings.Contains(stderr, "disabled") {
		t.Fatalf("add to disabled group %q", stderr)
	}
	mustRun(t, "hosts", "group", "enable", "dev")
	mustRun(t, "hosts", "remove", "x.dev.local", "--group", "dev")
	if _, _, err := run(t, "hosts", "group", "toggle", "dev"); err == nil {
		t.Fatal("bad group action accepted")
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), original+ihosts.BeginMarker+"\r\n") || !strings.Contains(string(b), "\r\n10.0.0.5 api.dev.local db.dev.local\r\n") {
		t.Fatalf("file %q", b)
	}

	if out, _ := mustRun(t, "hosts", "backup"); !strings.Contains(out, ihosts.BackupInfix) {
		t.Fatalf("backup %q", out)
	}
	list, _ := mustRun(t, "hosts", "restore", "--list")
	names := strings.Fields(list)
	if len(names) < 2 {
		t.Fatalf("restore --list %q", list)
	}
	mustRun(t, "hosts", "restore", names[0])
	if b, _ := os.ReadFile(p); string(b) != original {
		t.Fatalf("restore oldest: %q", b)
	}
}

func TestHostsApplyUnapplyApplied(t *testing.T) {
	const original = "127.0.0.1 localhost\n# keep me\n"
	home := t.TempDir()
	t.Setenv("ENVMGR_HOME", home)
	st := &profile.Store{Dir: home}
	if err := st.SetHost("a", "10.0.0.1", "a.local"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHost("b", "10.0.0.2", "b.local"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ihosts.EnvFile, p)

	if out, _ := mustRun(t, "hosts", "applied"); out != "" {
		t.Fatalf("applied before: %q", out)
	}
	out, _ := mustRun(t, "hosts", "apply", "a", "--dry-run")
	if !strings.Contains(out, "+# group: profile:a") {
		t.Fatalf("dry run %q", out)
	}
	mustRun(t, "hosts", "apply", "a")
	if out, _ := mustRun(t, "hosts", "applied"); strings.TrimSpace(out) != "a" {
		t.Fatalf("applied %q", out)
	}
	mustRun(t, "hosts", "apply", "b")
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "a.local") || !strings.Contains(string(b), "# group: profile:b\n10.0.0.2 b.local\n") || !strings.HasPrefix(string(b), original) {
		t.Fatalf("file %q", b)
	}
	// User edits cannot reach the profile group.
	if _, _, err := run(t, "hosts", "add", "10.0.0.9", "x.local", "--group", "profile:b"); err == nil {
		t.Fatal("add into profile group accepted")
	}
	if _, _, err := run(t, "hosts", "apply", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown profile: %v", err)
	}
	// A restore behind envmgr's back: applied warns about the mismatch.
	mustRun(t, "hosts", "restore")
	if out, stderr := mustRun(t, "hosts", "applied"); strings.TrimSpace(out) != "b" || !strings.Contains(stderr, "warning") {
		t.Fatalf("mismatch: %q %q", out, stderr)
	}
	mustRun(t, "hosts", "apply", "b")
	mustRun(t, "hosts", "unapply")
	if out, _ := mustRun(t, "hosts", "applied"); out != "" {
		t.Fatalf("applied after unapply: %q", out)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "profile:") || !strings.HasPrefix(string(b), original) {
		t.Fatalf("after unapply %q", b)
	}

	// --home points to another envmgr home (elevated prompts, sudo).
	other := t.TempDir()
	if err := (&profile.Store{Dir: other}).SetHost("c", "10.0.0.3", "c.local"); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "hosts", "apply", "c", "--home", other)
	if out, _ := mustRun(t, "hosts", "applied", "--home", other); strings.TrimSpace(out) != "c" {
		t.Fatalf("--home applied %q", out)
	}
	// --pause waits for Enter (stdin) and still returns.
	root := &cobra.Command{Use: "envmgr", SilenceErrors: true}
	CreateCommand(&context.EnvContext{}, root)
	var o, e bytes.Buffer
	root.SetIn(strings.NewReader("\n"))
	root.SetOut(&o)
	root.SetErr(&e)
	root.SetArgs([]string{"hosts", "unapply", "--home", other, "--pause"})
	if err := root.Execute(); err != nil || !strings.Contains(e.String(), "Press Enter") {
		t.Fatalf("pause: %v %q", err, e.String())
	}
}
