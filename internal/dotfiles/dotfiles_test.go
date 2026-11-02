package dotfiles

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func canSymlink(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	f := filepath.Join(d, "f")
	os.WriteFile(f, nil, 0o600)
	if err := os.Symlink(f, filepath.Join(d, "l")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStripComments(t *testing.T) {
	in := "{\n // line\n \"a\": \"http://x // not a comment\", /* block\n */ \"b\": 1\n}"
	var m map[string]any
	out := StripComments([]byte(in))
	if !strings.Contains(string(out), "http://x // not a comment") {
		t.Fatalf("string content changed: %s", out)
	}
	if strings.Contains(string(out), "line") || strings.Contains(string(out), "block") {
		t.Fatalf("comments kept: %s", out)
	}
	_ = m
}

func TestLoadManifestIgnoresOtherKeysAndComments(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, ManifestFile), `{
  "packages": { "go": { // pkg
    "debian": {"name": "golang"} } },
  "links": [ {"src": "nvim", "dest": "~/.config/nvim"} ]
}`)
	m, err := LoadManifest(d)
	if err != nil || len(m.Links) != 1 || m.Links[0].Src != "nvim" {
		t.Fatalf("m=%+v err=%v", m, err)
	}
}

func TestValidate(t *testing.T) {
	bad := map[string]Manifest{
		"empty":     {},
		"no dest":   {Links: []Link{{Src: "a"}}},
		"absolute":  {Links: []Link{{Src: "/etc/passwd", Dest: "~/x"}}},
		"escape":    {Links: []Link{{Src: "../x", Dest: "~/x"}}},
		"dot":       {Links: []Link{{Src: ".", Dest: "~/x"}}},
		"duplicate": {Links: []Link{{Src: "a", Dest: "~/x"}, {Src: "b", Dest: "~/x"}}},
	}
	for name, m := range bad {
		if m.Validate() == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := (Manifest{Links: []Link{{Src: "a/b", Dest: "~/x"}}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExpandDest(t *testing.T) {
	home := t.TempDir()
	env := func(k string) string {
		if k == "XDG" {
			return filepath.Join(home, "xdg")
		}
		return ""
	}
	for in, want := range map[string]string{
		"~":                   home,
		"~/.zshrc":            filepath.Join(home, ".zshrc"),
		"$XDG/nvim":           filepath.Join(home, "xdg", "nvim"),
		"${HOME}/.config/x":   filepath.Join(home, ".config", "x"),
		"$USERPROFILE/.tmux":  filepath.Join(home, ".tmux"),
		`~\Documents\profile`: filepath.Join(home, "Documents", "profile"),
	} {
		got, err := ExpandDest(in, home, env)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if runtime.GOOS != "windows" && strings.Contains(in, `\`) {
			continue // a backslash is a name character on unix
		}
		if got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
	if _, err := ExpandDest("$NOPE/x", home, env); err == nil {
		t.Error("undefined variable accepted")
	}
	if _, err := ExpandDest("relative/x", home, env); err == nil {
		t.Error("relative dest accepted")
	}
}

// fixture builds a repo with two entries and returns repo, home and manifest.
func fixture(t *testing.T) (string, string, Manifest) {
	repo, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(repo, "nvim", "init.lua"), "-- nvim")
	write(t, filepath.Join(repo, ".zshrc"), "# zsh")
	m := Manifest{Links: []Link{
		{Src: "nvim", Dest: "~/.config/nvim"},
		{Src: ".zshrc", Dest: "~/.zshrc"},
		{Src: "hypr", Dest: "~/.config/hypr", OS: []string{"plan9"}},
	}}
	return repo, home, m
}

func plan(t *testing.T, repo, home string, m Manifest, a Applied) []Item {
	t.Helper()
	items, err := BuildPlan(repo, m, runtime.GOOS, home, func(string) string { return "" }, a)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestLinkBackupIdempotentUnlinkRestore(t *testing.T) {
	canSymlink(t)
	repo, home, m := fixture(t)
	write(t, filepath.Join(home, ".zshrc"), "# mine") // conflict
	var applied Applied
	fixed := func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }

	items := plan(t, repo, home, m, applied)
	want := []State{StateMissing, StateConflict, StateSkipped}
	for i, it := range items {
		if it.State != want[i] {
			t.Fatalf("item %d state %s, want %s", i, it.State, want[i])
		}
	}

	// dry run changes nothing
	Apply(items, &applied, Options{DryRun: true, Now: fixed})
	if _, err := os.Lstat(filepath.Join(home, ".config")); err == nil {
		t.Fatal("dry run created files")
	}

	for _, a := range Apply(items, &applied, Options{Now: fixed}) {
		if a.Err != nil {
			t.Fatalf("%s: %v", a.Item.Link.Src, a.Err)
		}
	}
	bak := filepath.Join(home, ".zshrc.envmgr-bak-20261102-120000")
	if b, err := os.ReadFile(bak); err != nil || string(b) != "# mine" {
		t.Fatalf("backup: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".zshrc")); string(b) != "# zsh" {
		t.Fatalf("link content %q", b)
	}
	if len(applied.Links) != 2 {
		t.Fatalf("applied %+v", applied)
	}

	// second run: everything ok, no new backups
	items = plan(t, repo, home, m, applied)
	if items[0].State != StateOK || items[1].State != StateOK {
		t.Fatalf("not idempotent: %+v", items)
	}
	for _, a := range Apply(items, &applied, Options{Now: fixed}) {
		if a.Op != "ok" && a.Op != "skipped" {
			t.Fatalf("op %s on second run", a.Op)
		}
	}

	// unlink restores the user's file and removes only the links
	for _, a := range Unlink(&applied, true, false, Options{}) {
		if a.Err != nil {
			t.Fatalf("unlink: %v", a.Err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".zshrc")); string(b) != "# mine" {
		t.Fatalf("not restored: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "nvim")); err == nil {
		t.Fatal("nvim link left behind")
	}
	if _, err := os.Stat(filepath.Join(repo, "nvim", "init.lua")); err != nil {
		t.Fatal("unlink touched the repository")
	}
	if len(applied.Links) != 0 {
		t.Fatalf("record not cleared: %+v", applied)
	}
}

func TestWrongLinkIsReplacedWithBackup(t *testing.T) {
	canSymlink(t)
	repo, home, m := fixture(t)
	other := filepath.Join(t.TempDir(), "other")
	write(t, other, "x")
	os.MkdirAll(filepath.Join(home, ".config"), 0o755)
	if err := os.Symlink(other, filepath.Join(home, ".config", "nvim")); err != nil {
		t.Fatal(err)
	}
	var applied Applied
	items := plan(t, repo, home, m, applied)
	if items[0].State != StateWrongLink {
		t.Fatalf("state %s", items[0].State)
	}
	acts := Apply(items, &applied, Options{})
	if acts[0].Err != nil || acts[0].Backup == "" {
		t.Fatalf("%+v", acts[0])
	}
	if _, err := os.Lstat(acts[0].Backup); err != nil {
		t.Fatal("old link not preserved")
	}
}

func TestUnlinkLeavesAlteredDestination(t *testing.T) {
	canSymlink(t)
	repo, home, m := fixture(t)
	var applied Applied
	Apply(plan(t, repo, home, m, applied), &applied, Options{})
	dest := filepath.Join(home, ".zshrc")
	os.Remove(dest)
	write(t, dest, "replaced by the user")
	acts := Unlink(&applied, true, false, Options{})
	kept := false
	for _, a := range acts {
		if a.Item.Dest == dest {
			kept = a.Op == "kept"
		}
	}
	if b, _ := os.ReadFile(dest); string(b) != "replaced by the user" || !kept {
		t.Fatalf("user's file was touched (kept=%v, content %q)", kept, b)
	}
}

func TestCopyMode(t *testing.T) {
	repo, home, m := fixture(t)
	var applied Applied
	acts := Apply(plan(t, repo, home, m, applied), &applied, Options{Copy: true})
	for _, a := range acts {
		if a.Err != nil {
			t.Fatal(a.Err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "nvim", "init.lua")); string(b) != "-- nvim" {
		t.Fatalf("copy content %q", b)
	}
	if fi, _ := os.Lstat(filepath.Join(home, ".zshrc")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("copy mode made a symlink")
	}
	if s := plan(t, repo, home, m, applied)[1].State; s != StateCopied {
		t.Fatalf("state %s", s)
	}
	// copies survive unlink without --force
	for _, a := range Unlink(&applied, true, false, Options{}) {
		if a.Op != "kept" {
			t.Fatalf("op %s", a.Op)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal("copy removed without --force")
	}
	for _, a := range Unlink(&applied, true, true, Options{}) {
		if a.Err != nil {
			t.Fatal(a.Err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
		t.Fatal("copy kept with --force")
	}
}

func TestMissingSource(t *testing.T) {
	repo, home, _ := fixture(t)
	m := Manifest{Links: []Link{{Src: "nope", Dest: "~/x"}}}
	var applied Applied
	items := plan(t, repo, home, m, applied)
	if items[0].State != StateSrcGone {
		t.Fatalf("state %s", items[0].State)
	}
	if a := Apply(items, &applied, Options{}); a[0].Err == nil {
		t.Fatal("missing source not reported")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, ok, err := s.LoadConfig(); ok || err != nil {
		t.Fatalf("empty store: %v %v", ok, err)
	}
	if err := s.SaveConfig(Config{Dir: "/x", Source: "git@h:r.git"}); err != nil {
		t.Fatal(err)
	}
	if c, ok, _ := s.LoadConfig(); !ok || c.Dir != "/x" {
		t.Fatalf("%+v %v", c, ok)
	}
	a := Applied{}
	a.set(AppliedLink{Dest: "/d", Mode: "symlink"})
	a.set(AppliedLink{Dest: "/d", Mode: "copy"})
	if err := s.SaveApplied(a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LoadApplied(); len(got.Links) != 1 || got.Links[0].Mode != "copy" {
		t.Fatalf("%+v", got)
	}
}
