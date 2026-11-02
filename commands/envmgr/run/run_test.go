package run

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// TestHelperProcess is the child started by the run tests, not a real test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("ENVMGR_TEST_HELPER") != "1" {
		return
	}
	fmt.Printf("RUN_VAR=%s|KEEP=%s\n", os.Getenv("RUN_VAR"), os.Getenv("KEEP"))
	code := 0
	fmt.Sscanf(os.Getenv("RUN_EXIT"), "%d", &code)
	os.Exit(code)
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

func TestRunPassesVarsAndExitCode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	t.Setenv("ENVMGR_TEST_HELPER", "1")
	t.Setenv("KEEP", "inherited")
	t.Setenv("RUN_VAR", "from parent")
	if err := (&profile.Store{Dir: dir}).SetVar("dev", "RUN_VAR", "it's $x"); err != nil {
		t.Fatal(err)
	}
	got := -1
	exit = func(c int) { got = c }
	t.Cleanup(func() { exit = os.Exit })

	for _, tc := range []struct {
		code string
		dash bool
	}{{"0", true}, {"3", true}, {"5", false}} {
		t.Setenv("RUN_EXIT", tc.code)
		got = -1
		args := []string{"run", "dev"}
		if tc.dash {
			args = append(args, "--")
		}
		args = append(args, os.Args[0], "-test.run=^TestHelperProcess$")
		out, _, err := execute(args...)
		if err != nil {
			t.Fatalf("exit %s: %v", tc.code, err)
		}
		if !strings.Contains(out, "RUN_VAR=it's $x|KEEP=inherited") {
			t.Fatalf("child output = %q", out)
		}
		want := -1 // exit not called on success
		if tc.code != "0" {
			fmt.Sscanf(tc.code, "%d", &want)
		}
		if got != want {
			t.Fatalf("exit code = %d, want %d", got, want)
		}
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	_ = (&profile.Store{Dir: dir}).SetVar("dev", "A", "1")
	if out, _, err := execute("run", "nope", "--", "x"); err == nil || out != "" || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown profile: out=%q err=%v", out, err)
	}
	if _, _, err := execute("run", "dev", "--"); err == nil {
		t.Fatal("missing command accepted")
	}
	if _, _, err := execute("run", "dev", "--", "envmgr-no-such-binary-xyz"); err == nil {
		t.Fatal("missing binary accepted")
	}
}
