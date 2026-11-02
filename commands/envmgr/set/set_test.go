package set

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

func TestSetCreatesProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	if _, _, err := execute("set", "dev", "MY_URL", "http://dev"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute("set", "dev", "FLAGS", "-x --y"); err != nil {
		t.Fatalf("value starting with -: %v", err)
	}
	p, err := (&profile.Store{Dir: dir}).Get("dev")
	if err != nil {
		t.Fatal(err)
	}
	if p.Vars["MY_URL"] != "http://dev" || p.Vars["FLAGS"] != "-x --y" {
		t.Fatalf("vars = %v", p.Vars)
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	t.Setenv("ENVMGR_HOME", t.TempDir())
	for _, args := range [][]string{
		{"set", "dev", "1BAD", "x"},
		{"set", "dev", "A;rm", "x"},
		{"set", "dev", "ENVMGR_PROFILE", "x"},
		{"set", "../evil", "A", "x"},
		{"set", "dev", "A"},
	} {
		if out, _, err := execute(args...); err == nil {
			t.Errorf("%v accepted (stdout %q)", args, out)
		}
	}
}
