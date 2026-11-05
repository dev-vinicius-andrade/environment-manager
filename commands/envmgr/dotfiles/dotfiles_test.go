package dotfiles

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "envmgr", SilenceErrors: true}
	CreateCommand(&context.EnvContext{}, root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"dotfiles"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func must(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := run(t, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s%s", args, err, out, errOut)
	}
	return out
}

func repo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "nvim"), 0o755)
	os.WriteFile(filepath.Join(d, "nvim", "init.lua"), []byte("-- nvim"), 0o644)
	os.WriteFile(filepath.Join(d, ".zshrc"), []byte("# zsh"), 0o644)
	os.WriteFile(filepath.Join(d, ".dotfiles.config.json"), []byte(`{
  // other tools' keys are ignored
  "packages": {},
  "links": [
    {"src": "nvim", "dest": "~/.config/nvim"},
    {"src": ".zshrc", "dest": "~/.zshrc"}
  ]
}`), 0o644)
	return d
}

func TestRegisterStatusLinkUnlink(t *testing.T) {
	t.Setenv("ENVMGR_HOME", t.TempDir())
	r, home := repo(t), t.TempDir()
	os.WriteFile(filepath.Join(home, ".zshrc"), []byte("# mine"), 0o644)

	must(t, "register", r)
	out := must(t, "status", "--home", home)
	if !strings.Contains(out, "missing") || !strings.Contains(out, "conflict") {
		t.Fatalf("status:\n%s", out)
	}
	if _, _, err := run(t, "status", "--home", home, "--check"); err == nil {
		t.Fatal("--check passed with entries out of place")
	}

	dry := must(t, "link", "--home", home, "--dry-run", "--copy")
	if !strings.Contains(dry, "would-copied") {
		t.Fatalf("dry-run:\n%s", dry)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
		t.Fatal("dry run wrote files")
	}

	must(t, "link", "--home", home, "--copy")
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "nvim", "init.lua")); string(b) != "-- nvim" {
		t.Fatalf("nvim copy: %q", b)
	}
	if _, _, err := run(t, "status", "--home", home, "--check"); err != nil {
		t.Fatalf("status after link: %v", err)
	}
	baks, _ := filepath.Glob(filepath.Join(home, ".zshrc.envmgr-bak-*"))
	if len(baks) != 1 {
		t.Fatalf("backups: %v", baks)
	}

	// copies survive a plain unlink, go with --force, and the backup returns
	must(t, "unlink")
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal("copy removed without --force")
	}
	must(t, "unlink", "--force")
	if b, _ := os.ReadFile(filepath.Join(home, ".zshrc")); string(b) != "# mine" {
		t.Fatalf("not restored: %q", b)
	}
}

func TestNoRepositoryRegistered(t *testing.T) {
	t.Setenv("ENVMGR_HOME", t.TempDir())
	if _, _, err := run(t, "status"); err == nil || !strings.Contains(err.Error(), "no dotfiles repository registered") {
		t.Fatalf("err = %v", err)
	}
}

func TestRegisterRejectsRepoWithoutLinks(t *testing.T) {
	t.Setenv("ENVMGR_HOME", t.TempDir())
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, ".dotfiles.config.json"), []byte(`{"packages":{}}`), 0o644)
	if _, _, err := run(t, "register", d); err == nil {
		t.Fatal("repository without links accepted")
	}
}

func TestCloneLocalRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("ENVMGR_HOME", t.TempDir())
	src := repo(t)
	for _, a := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"}} {
		cmd := exec.Command("git", append([]string{"-c", "safe.directory=*", "-C", src}, a...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", a, err, out)
		}
	}
	home := t.TempDir()
	dest := filepath.Join(t.TempDir(), "cloned")
	must(t, "clone", src, dest, "--home", home)
	if _, err := os.Stat(filepath.Join(dest, "nvim", "init.lua")); err != nil {
		t.Fatal("clone missing files")
	}
	if !strings.Contains(must(t, "status", "--home", home), dest) {
		t.Fatal("clone did not register the repository")
	}
	if _, _, err := run(t, "clone", src, dest, "--home", home); err == nil {
		t.Fatal("clone into a non-empty directory accepted")
	}
}
