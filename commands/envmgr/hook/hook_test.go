package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ihook "github.com/dev-vinicius-andrade/environment-manager/internal/hook"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func setup(t *testing.T, goos string) string {
	t.Helper()
	home := t.TempDir()
	oldP, oldE := currentParams, currentEnv
	t.Cleanup(func() { currentParams, currentEnv = oldP, oldE })
	currentParams = func() (ihook.Params, error) {
		return ihook.Params{Exe: "/opt/envmgr", DefaultSelection: "/s/selected.json", GOOS: goos}, nil
	}
	currentEnv = func() (ihook.Env, error) {
		return ihook.Env{GOOS: goos, Home: home, Getenv: func(string) string { return "" }}, nil
	}
	return home
}

func execute(args ...string) (string, string, error) {
	root := &cobra.Command{Use: "envmgr"}
	CreateCommand(&context.EnvContext{}, root)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func TestHookPrintsScript(t *testing.T) {
	setup(t, "linux")
	out, _, err := execute("hook", "bash")
	if err != nil || !strings.Contains(out, "__envmgr_hook()") || !strings.Contains(out, "__envmgr_exe='/opt/envmgr'") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	out, _, err = execute("hook", "cmd")
	if err == nil || out != "" || !strings.Contains(err.Error(), "cmd has no prompt hook") {
		t.Fatalf("cmd: out=%q err=%v", out, err)
	}
	if _, _, err := execute("hook", "bash", "--dry-run"); err == nil {
		t.Fatal("--dry-run alone accepted")
	}
}

func TestHookInstallUninstall(t *testing.T) {
	home := setup(t, "linux")
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte("alias ll='ls -l'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute("hook", "--install", "bash", "--dry-run")
	if err != nil || !strings.Contains(out, "installed (dry run): "+rc) || !strings.Contains(out, ihook.BeginMarker) {
		t.Fatalf("dry run: %q %v", out, err)
	}
	if b, _ := os.ReadFile(rc); strings.Contains(string(b), ihook.BeginMarker) {
		t.Fatal("dry run wrote the file")
	}
	if out, _, err = execute("hook", "--install", "bash"); err != nil || out != "installed: "+rc+"\n" {
		t.Fatalf("install: %q %v", out, err)
	}
	if out, _, err = execute("hook", "--install", "bash"); err != nil || out != "already installed: "+rc+"\n" {
		t.Fatalf("second install: %q %v", out, err)
	}
	b, _ := os.ReadFile(rc)
	if strings.Count(string(b), ihook.BeginMarker) != 1 || !strings.HasPrefix(string(b), "alias ll='ls -l'\n") {
		t.Fatalf("content: %q", b)
	}
	if out, _, err = execute("hook", "--uninstall", "bash"); err != nil || out != "removed: "+rc+"\n" {
		t.Fatalf("uninstall: %q %v", out, err)
	}
	if b, _ := os.ReadFile(rc); string(b) != "alias ll='ls -l'\n" {
		t.Fatalf("after uninstall: %q", b)
	}
	if out, _, err = execute("hook", "--uninstall", "bash"); err != nil || out != "not installed: "+rc+"\n" {
		t.Fatalf("second uninstall: %q %v", out, err)
	}
}

func TestHookInstallPowerShellBothProfiles(t *testing.T) {
	home := setup(t, "windows")
	out, _, err := execute("hook", "--install", "powershell")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
	} {
		if !strings.Contains(out, "created: "+p+"\n") {
			t.Errorf("missing %s in %q", p, out)
		}
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "hook powershell | Out-String | Invoke-Expression") {
			t.Errorf("%s: %q %v", p, b, err)
		}
	}
}
