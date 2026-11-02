package profile

import (
	"bytes"
	"strings"
	"testing"

	store "github.com/dev-vinicius-andrade/environment-manager/internal/profile"
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

func TestShowAndDelete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	st := &store.Store{Dir: dir}
	_ = st.SetVar("dev", "TOKEN", "secret-value")
	_ = st.SetVar("dev", "API", "http://dev")

	out, _, err := execute("profile", "show", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if out != "API=********\nTOKEN=********\n" || strings.Contains(out, "secret") {
		t.Fatalf("masked show = %q", out)
	}
	out, _, err = execute("profile", "show", "dev", "--reveal")
	if err != nil || out != "API=http://dev\nTOKEN=secret-value\n" {
		t.Fatalf("reveal: out=%q err=%v", out, err)
	}
	if out, _, err := execute("profile", "show", "nope"); err == nil || out != "" {
		t.Fatalf("show unknown: out=%q err=%v", out, err)
	}

	if _, _, err := execute("profile", "delete", "dev"); err != nil {
		t.Fatal(err)
	}
	if names, _ := st.List(); len(names) != 0 {
		t.Fatalf("profiles left: %v", names)
	}
	if _, _, err := execute("profile", "delete", "dev"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("delete unknown: %v", err)
	}
}
