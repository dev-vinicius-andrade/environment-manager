package deactivate

import (
	"bytes"
	"os"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func clearEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		t.Setenv(n, "")
		os.Unsetenv(n)
	}
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

func TestDeactivateNothingActive(t *testing.T) {
	clearEnv(t, "ENVMGR_PROFILE", "ENVMGR_VARS")
	out, errOut, err := execute("deactivate", "--shell", "bash")
	if err != nil || out != "" || errOut == "" {
		t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
	}
}

func TestDeactivateRestoresAndUnsets(t *testing.T) {
	// NEW did not exist before activation (no backup); OLD had "orig value".
	clearEnv(t, "ENVMGR_PREV_NEW")
	t.Setenv("ENVMGR_PROFILE", "dev")
	t.Setenv("ENVMGR_VARS", "OLD,NEW,bad;name")
	t.Setenv("ENVMGR_PREV_OLD", "orig value")
	t.Setenv("OLD", "dev value")
	t.Setenv("NEW", "dev only")

	want := map[string]string{
		"bash": "unset NEW\nunset ENVMGR_PREV_NEW\n" +
			"export OLD='orig value'\nunset ENVMGR_PREV_OLD\n" +
			"unset ENVMGR_PROFILE\nunset ENVMGR_VARS\n",
		"powershell": "$env:NEW = $null\n$env:ENVMGR_PREV_NEW = $null\n" +
			"$env:OLD = 'orig value'\n$env:ENVMGR_PREV_OLD = $null\n" +
			"$env:ENVMGR_PROFILE = $null\n$env:ENVMGR_VARS = $null\n",
		"fish": "set -e NEW\nset -e ENVMGR_PREV_NEW\n" +
			"set -gx OLD 'orig value'\nset -e ENVMGR_PREV_OLD\n" +
			"set -e ENVMGR_PROFILE\nset -e ENVMGR_VARS\n",
		"cmd": "@set NEW=\n@set ENVMGR_PREV_NEW=\n" +
			"@set OLD=orig value\n@set ENVMGR_PREV_OLD=\n" +
			"@set ENVMGR_PROFILE=\n@set ENVMGR_VARS=\n",
	}
	for sh, w := range want {
		out, _, err := execute("deactivate", "--shell", sh)
		if err != nil {
			t.Fatalf("%s: %v", sh, err)
		}
		if out != w {
			t.Errorf("%s:\n got %q\nwant %q", sh, out, w)
		}
	}
}
