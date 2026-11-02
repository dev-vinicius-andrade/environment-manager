package list

import (
	"bytes"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

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

func TestListEmpty(t *testing.T) {
	t.Setenv("ENVMGR_HOME", t.TempDir())
	out, errOut, err := execute("list")
	if err != nil || out != "" || errOut == "" {
		t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
	}
}

func TestListMarksActive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	st := &profile.Store{Dir: dir}
	for _, n := range []string{"local", "dev", "test1"} {
		if err := st.SetVar(n, "A", "1"); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ENVMGR_PROFILE", "local")
	out, _, err := execute("list")
	if err != nil {
		t.Fatal(err)
	}
	if want := "  dev\n* local\n  test1\n"; out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}
