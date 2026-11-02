package use

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func setup(t *testing.T) *profile.Store {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	for _, n := range []string{"ENVMGR_PROFILE", "ENVMGR_VARS", "ENVMGR_PREV_MY_URL", "MY_URL"} {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
	return &profile.Store{Dir: dir}
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

func TestUseUnknownProfile(t *testing.T) {
	setup(t)
	out, _, err := execute("use", "nope", "--shell", "bash")
	if err == nil || !strings.Contains(err.Error(), `profile "nope" not found`) {
		t.Fatalf("err = %v", err)
	}
	if out != "" {
		t.Fatalf("stdout not empty: %q", out)
	}
}

func TestUseEmitsCodeOnStdoutOnly(t *testing.T) {
	st := setup(t)
	if err := st.SetVar("dev", "MY_URL", "http://x/'y'"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MY_URL", "orig")
	want := map[string]string{
		"bash":       "export ENVMGR_PREV_MY_URL='orig'\nexport MY_URL='http://x/'\\''y'\\'''\nexport ENVMGR_PROFILE='dev'\nexport ENVMGR_VARS='MY_URL'\n",
		"zsh":        "export ENVMGR_PREV_MY_URL='orig'\nexport MY_URL='http://x/'\\''y'\\'''\nexport ENVMGR_PROFILE='dev'\nexport ENVMGR_VARS='MY_URL'\n",
		"fish":       "set -gx ENVMGR_PREV_MY_URL 'orig'\nset -gx MY_URL 'http://x/\\'y\\''\nset -gx ENVMGR_PROFILE 'dev'\nset -gx ENVMGR_VARS 'MY_URL'\n",
		"powershell": "$env:ENVMGR_PREV_MY_URL = 'orig'\n$env:MY_URL = 'http://x/''y'''\n$env:ENVMGR_PROFILE = 'dev'\n$env:ENVMGR_VARS = 'MY_URL'\n",
		"cmd":        "@set ENVMGR_PREV_MY_URL=orig\n@set MY_URL=http://x/'y'\n@set ENVMGR_PROFILE=dev\n@set ENVMGR_VARS=MY_URL\n",
	}
	for sh, w := range want {
		out, errOut, err := execute("use", "dev", "--shell", sh)
		if err != nil {
			t.Fatalf("%s: %v", sh, err)
		}
		if out != w {
			t.Errorf("%s:\n got %q\nwant %q", sh, out, w)
		}
		if !strings.Contains(errOut, "activated") {
			t.Errorf("%s: stderr = %q", sh, errOut)
		}
	}
}

func TestUseBadShell(t *testing.T) {
	st := setup(t)
	_ = st.SetVar("dev", "MY_URL", "x")
	out, _, err := execute("use", "dev", "--shell", "tcsh")
	if err == nil || out != "" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestUseRejectsReservedVarWithEmptyStdout(t *testing.T) {
	st := setup(t)
	if err := st.Save(profile.Profile{Name: "bad", Vars: map[string]string{"ENVMGR_PROFILE": "x", "A": "1"}}); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute("use", "bad", "--shell", "bash")
	if err == nil || out != "" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestUseCmdRejectsNewlineWithEmptyStdout(t *testing.T) {
	st := setup(t)
	_ = st.SetVar("dev", "MY_URL", "a\nb")
	out, _, err := execute("use", "dev", "--shell", "cmd")
	if err == nil || out != "" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}
