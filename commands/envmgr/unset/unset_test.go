package unset

import (
	"bytes"
	"strings"
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

func TestUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ENVMGR_HOME", dir)
	st := &profile.Store{Dir: dir}
	_ = st.SetVar("dev", "A", "1")
	_ = st.SetVar("dev", "B", "2")

	if _, _, err := execute("unset", "dev", "A"); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Get("dev")
	if _, ok := p.Vars["A"]; ok || p.Vars["B"] != "2" {
		t.Fatalf("vars = %v", p.Vars)
	}
	// Missing variable: notice on stderr, exit 0.
	if _, errOut, err := execute("unset", "dev", "A"); err != nil || !strings.Contains(errOut, "not set") {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if _, _, err := execute("unset", "nope", "A"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown profile: %v", err)
	}
	if _, _, err := execute("unset", "dev", "bad name"); err == nil {
		t.Fatal("invalid var accepted")
	}
}
