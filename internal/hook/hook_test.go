package hook

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/hook -update to create it)", err)
	}
	want := strings.ReplaceAll(string(b), "\r\n", "\n")
	if got != want {
		t.Errorf("%s differs from %s:\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

var unixParams = Params{Exe: "/opt/env mgr/envmgr", DefaultSelection: "/home/u/.config/envmgr/selected.json", GOOS: "linux"}
var winParams = Params{Exe: `C:\Users\José O'Neil\bin\envmgr.exe`, DefaultSelection: `C:\Users\José O'Neil\AppData\Roaming\envmgr\selected.json`, GOOS: "windows"}

func TestScriptSnapshots(t *testing.T) {
	for _, c := range []struct {
		name string
		k    shell.Kind
		p    Params
	}{
		{"bash", shell.Bash, unixParams},
		{"zsh", shell.Zsh, unixParams},
		{"fish", shell.Fish, unixParams},
		{"powershell", shell.PowerShell, winParams},
		{"bash-windows", shell.Bash, winParams},
	} {
		got, err := Script(c.k, c.p)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		golden(t, "hook-"+c.name, got)
	}
}

func TestScriptQuoting(t *testing.T) {
	ps, _ := Script(shell.PowerShell, winParams)
	for _, r := range ps {
		if r > 0x7e || (r < 0x20 && r != '\n') {
			t.Fatalf("PowerShell hook is not printable ASCII: %q", r)
		}
	}
	if !strings.Contains(ps, `'C:\Users\Jos' + [char]0x00E9 + ' O''Neil\bin\envmgr.exe'`) {
		t.Errorf("exe not quoted as expected:\n%s", ps)
	}
	b, _ := Script(shell.Bash, winParams)
	if !strings.Contains(b, `__envmgr_exe='C:/Users/José O'\''Neil/bin/envmgr.exe'`) {
		t.Errorf("bash exe not quoted as expected:\n%s", b)
	}
	if _, err := Script(shell.Bash, Params{Exe: "/x\n; rm -rf /", DefaultSelection: "/s"}); err == nil {
		t.Error("newline in path accepted")
	}
	if _, err := Script(shell.Cmd, unixParams); !errors.Is(err, ErrCmdUnsupported) {
		t.Errorf("cmd: %v", err)
	}
}

func TestBlocks(t *testing.T) {
	for _, c := range []struct {
		k    shell.Kind
		exe  string
		goos string
		want string
	}{
		{shell.Bash, "/opt/envmgr", "linux", `if [ -x '/opt/envmgr' ]; then eval "$('/opt/envmgr' hook bash)"; fi`},
		{shell.Zsh, "/opt/envmgr", "linux", `if [ -x '/opt/envmgr' ]; then eval "$('/opt/envmgr' hook zsh)"; fi`},
		{shell.Fish, "/opt/envmgr", "linux", "if test -x '/opt/envmgr'\n    '/opt/envmgr' hook fish | source\nend"},
		{shell.PowerShell, `C:\bin\envmgr.exe`, "windows", `if (Test-Path -LiteralPath ('C:\bin\envmgr.exe')) { & ('C:\bin\envmgr.exe') hook powershell | Out-String | Invoke-Expression }`},
		{shell.Bash, `C:\bin\envmgr.exe`, "windows", `if [ -x 'C:/bin/envmgr.exe' ]; then eval "$('C:/bin/envmgr.exe' hook bash)"; fi`},
	} {
		got, err := Block(c.k, c.exe, c.goos)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, BeginMarker+"\n") || !strings.HasSuffix(got, EndMarker+"\n") || !strings.Contains(got, c.want+"\n") {
			t.Errorf("%s block:\n%s\nwant to contain:\n%s", c.k, got, c.want)
		}
	}
	if _, err := Block(shell.Cmd, "x", "windows"); !errors.Is(err, ErrCmdUnsupported) {
		t.Error("cmd block accepted")
	}
}

func TestProfilePaths(t *testing.T) {
	env := func(goos string, vars map[string]string) Env {
		return Env{GOOS: goos, Home: "/home/u", Getenv: func(k string) string { return vars[k] }}
	}
	check := func(k shell.Kind, e Env, want ...string) {
		t.Helper()
		got, err := ProfilePaths(k, e)
		if err != nil {
			t.Fatal(err)
		}
		for i := range want {
			want[i] = filepath.FromSlash(want[i])
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: got %v want %v", k, got, want)
		}
	}
	check(shell.Bash, env("linux", nil), "/home/u/.bashrc")
	check(shell.Zsh, env("linux", nil), "/home/u/.zshrc")
	check(shell.Zsh, env("linux", map[string]string{"ZDOTDIR": "/z"}), "/z/.zshrc")
	check(shell.Fish, env("linux", nil), "/home/u/.config/fish/config.fish")
	check(shell.Fish, env("linux", map[string]string{"XDG_CONFIG_HOME": "/x"}), "/x/fish/config.fish")
	check(shell.PowerShell, env("linux", nil), "/home/u/.config/powershell/Microsoft.PowerShell_profile.ps1")
	check(shell.PowerShell, env("windows", nil),
		"/home/u/Documents/WindowsPowerShell/Microsoft.PowerShell_profile.ps1",
		"/home/u/Documents/PowerShell/Microsoft.PowerShell_profile.ps1")
	w := env("windows", nil)
	w.Documents = "/od/Docs"
	check(shell.PowerShell, w,
		"/od/Docs/WindowsPowerShell/Microsoft.PowerShell_profile.ps1",
		"/od/Docs/PowerShell/Microsoft.PowerShell_profile.ps1")
	if _, err := ProfilePaths(shell.Cmd, w); !errors.Is(err, ErrCmdUnsupported) {
		t.Error("cmd accepted")
	}
}

func TestPlanInstallUninstall(t *testing.T) {
	block, _ := Block(shell.Bash, "/opt/envmgr", "linux")

	// New file.
	c := PlanInstall("/h/.bashrc", "", false, block)
	if c.After != block || !c.Changed || c.Had {
		t.Fatalf("new file: %+v", c)
	}
	// Existing content without trailing newline: kept, block appended after a blank line.
	c = PlanInstall("/h/.bashrc", "alias ll='ls -l'", true, block)
	if c.After != "alias ll='ls -l'\n\n"+block {
		t.Fatalf("append: %q", c.After)
	}
	// Idempotent.
	again := PlanInstall("/h/.bashrc", c.After, true, block)
	if again.Changed || !again.Had {
		t.Fatalf("second install changed the file: %+v", again)
	}
	// A block with another exe is replaced in place; user lines around it stay.
	old, _ := Block(shell.Bash, "/old/envmgr", "linux")
	content := "before\n" + old + "after\n"
	c = PlanInstall("/h/.bashrc", content, true, block)
	if c.After != "before\n"+block+"after\n" || !c.Had {
		t.Fatalf("replace: %q", c.After)
	}
	// Uninstall removes only the block.
	u := PlanUninstall("/h/.bashrc", c.After, true)
	if u.After != "before\nafter\n" || !u.Had || !u.Changed {
		t.Fatalf("uninstall: %q", u.After)
	}
	if u := PlanUninstall("/h/.bashrc", "x\n", true); u.Changed || u.Had {
		t.Fatalf("uninstall without block: %+v", u)
	}
	// CRLF files keep CRLF.
	c = PlanInstall("/h/p.ps1", "Set-Alias g git\r\n", true, block)
	if !strings.Contains(c.After, "\r\n"+BeginMarker+"\r\n") || strings.Count(c.After, "\n") != strings.Count(c.After, "\r\n") {
		t.Fatalf("crlf: %q", c.After)
	}
	if u := PlanUninstall("/h/p.ps1", c.After, true); u.After != "Set-Alias g git\r\n" {
		t.Fatalf("crlf uninstall: %q", u.After)
	}
	// An unterminated block is left alone.
	if u := PlanUninstall("/h/.bashrc", "a\n"+BeginMarker+"\nb\n", true); u.Changed {
		t.Fatalf("unterminated block removed: %q", u.After)
	}
}

func TestApplyWritesAndKeepsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	block, _ := Block(shell.Bash, "/opt/envmgr", "linux")
	p := filepath.Join(dir, "sub", ".bashrc")
	c := PlanInstall(p, "", false, block)
	if err := Apply(c); err != nil {
		t.Fatal(err)
	}
	got, exists, err := ReadFile(p)
	if err != nil || !exists || got != block {
		t.Fatalf("read back: %q %v %v", got, exists, err)
	}
	target := filepath.Join(dir, "real-bashrc")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link-bashrc")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	cur, _, _ := ReadFile(link)
	if err := Apply(PlanInstall(link, cur, true, block)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), BeginMarker) {
		t.Fatalf("target not updated: %q", b)
	}
}
