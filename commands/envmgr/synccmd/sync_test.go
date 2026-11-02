package synccmd_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/follow"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/selectcmd"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/selected"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/synccmd"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func setup(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ENVMGR_HOME", home)
	for _, k := range []string{"ENVMGR_PROFILE", "ENVMGR_VARS", "ENVMGR_PINNED", "URL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	st := &profile.Store{Dir: home}
	for _, n := range []string{"a", "b"} {
		if err := st.SetVar(n, "URL", n+n); err != nil {
			t.Fatal(err)
		}
	}
}

func execute(args ...string) (string, string, error) {
	root := &cobra.Command{Use: "envmgr"}
	ctx := &context.EnvContext{}
	selectcmd.CreateCommand(ctx, root)
	selected.CreateCommand(ctx, root)
	synccmd.CreateCommand(ctx, root)
	follow.CreateCommand(ctx, root)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func TestSelectSelectedSyncFollow(t *testing.T) {
	setup(t)
	if out, _, err := execute("selected"); err != nil || out != "" {
		t.Fatalf("selected (none): %q %v", out, err)
	}
	if out, _, err := execute("sync", "--shell", "bash"); err != nil || out != "" {
		t.Fatalf("sync with nothing selected: %q %v", out, err)
	}
	if _, _, err := execute("select", "nope"); err == nil || !strings.Contains(err.Error(), `profile "nope" not found`) {
		t.Fatalf("select missing: %v", err)
	}
	if _, _, err := execute("select"); err == nil {
		t.Fatal("select without args accepted")
	}
	if _, _, err := execute("select", "a", "--none"); err == nil {
		t.Fatal("select a --none accepted")
	}
	if out, _, err := execute("select", "a"); err != nil || out != "" {
		t.Fatalf("select a: %q %v", out, err)
	}
	if out, _, err := execute("selected"); err != nil || out != "a\n" {
		t.Fatalf("selected: %q %v", out, err)
	}
	out, errOut, err := execute("sync", "--shell", "bash")
	if err != nil || !strings.Contains(out, "export URL='aa'\n") || !strings.Contains(out, "unset ENVMGR_PINNED\n") || strings.Contains(out, "envmgr:") {
		t.Fatalf("sync: %q %v", out, err)
	}
	if !strings.Contains(errOut, `activated profile "a"`) {
		t.Fatalf("sync stderr: %q", errOut)
	}
	// Session already on a: nothing at all.
	t.Setenv("ENVMGR_PROFILE", "a")
	t.Setenv("ENVMGR_VARS", "URL")
	t.Setenv("URL", "aa")
	if out, errOut, err := execute("sync", "--shell", "powershell"); err != nil || out != "" || errOut != "" {
		t.Fatalf("unchanged: %q %q %v", out, errOut, err)
	}
	// Pinned: nothing even when the selection differs; follow switches and unpins.
	t.Setenv("ENVMGR_PINNED", "1")
	if _, _, err := execute("select", "b"); err != nil {
		t.Fatal(err)
	}
	if out, _, err := execute("sync", "--shell", "powershell"); err != nil || out != "" {
		t.Fatalf("pinned: %q %v", out, err)
	}
	out, _, err = execute("follow", "--shell", "powershell")
	if err != nil || !strings.Contains(out, "$env:URL = 'bb'\n") || !strings.Contains(out, "$env:ENVMGR_PINNED = $null\n") {
		t.Fatalf("follow: %q %v", out, err)
	}
	// select --none: a following session deactivates.
	os.Unsetenv("ENVMGR_PINNED")
	if _, _, err := execute("select", "--none"); err != nil {
		t.Fatal(err)
	}
	if out, _, err := execute("selected"); err != nil || out != "" {
		t.Fatalf("selected after --none: %q %v", out, err)
	}
	out, _, err = execute("sync", "--shell", "fish")
	if err != nil || !strings.Contains(out, "set -e URL\n") || !strings.Contains(out, "set -e ENVMGR_PROFILE\n") {
		t.Fatalf("sync after --none: %q %v", out, err)
	}
	if out, _, err := execute("sync", "--shell", "nosuch"); err == nil || out != "" {
		t.Fatal("bad shell accepted")
	}
}

func TestSelectScopesAndSyncHintOnce(t *testing.T) {
	setup(t)
	// Env only (default): no hint.
	if _, errOut, err := execute("select", "a"); err != nil || strings.Contains(errOut, "hosts apply") {
		t.Fatalf("%v %q", err, errOut)
	}
	if out, errOut, _ := execute("selected"); strings.TrimSpace(out) != "a" || !strings.Contains(errOut, "scopes: env") {
		t.Fatalf("selected %q %q", out, errOut)
	}
	if _, errOut, _ := execute("sync", "--shell", "bash"); strings.Contains(errOut, "hosts apply") {
		t.Fatalf("hint without hosts scope: %q", errOut)
	}

	// --hosts: env and hosts; select itself says the hosts are not applied.
	_, errOut, err := execute("select", "a", "--hosts")
	if err != nil || !strings.Contains(errOut, `envmgr hosts apply a`) {
		t.Fatalf("select --hosts: %v %q", err, errOut)
	}
	if _, errOut, _ := execute("selected"); !strings.Contains(errOut, "scopes: env, hosts") {
		t.Fatalf("selected scopes %q", errOut)
	}
	out, errOut, err := execute("sync", "--shell", "bash")
	if err != nil || !strings.Contains(out, "export URL='aa'") || strings.Count(errOut, "envmgr hosts apply a") != 1 {
		t.Fatalf("first sync: %v out=%q stderr=%q", err, out, errOut)
	}
	// Same selection: the hint is not repeated (the hook may run sync often).
	if _, errOut, _ := execute("sync", "--shell", "bash"); strings.Contains(errOut, "hosts apply") {
		t.Fatalf("hint repeated: %q", errOut)
	}
	// A new selection change shows it again, once.
	if _, _, err := execute("select", "b", "--only", "hosts"); err != nil {
		t.Fatal(err)
	}
	out, errOut, _ = execute("sync", "--shell", "bash")
	if out != "" || !strings.Contains(errOut, "envmgr hosts apply b") {
		t.Fatalf("hosts only: out=%q stderr=%q", out, errOut)
	}
	if _, errOut, _ := execute("sync", "--shell", "bash"); strings.Contains(errOut, "hosts apply") {
		t.Fatalf("hint repeated: %q", errOut)
	}

	for _, bad := range [][]string{
		{"select", "a", "--hosts", "--only", "env"},
		{"select", "a", "--env", "--hosts"},
		{"select", "--none", "--hosts"},
		{"select", "a", "--only", "nope"},
	} {
		if _, _, err := execute(bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if _, _, err := execute("select", "a", "--env"); err != nil {
		t.Fatal(err)
	}
}
