package current

import (
	"bytes"
	"os"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func execute(args ...string) (string, error) {
	root := &cobra.Command{Use: "envmgr"}
	CreateCommand(&context.EnvContext{}, root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestCurrent(t *testing.T) {
	t.Setenv("ENVMGR_PROFILE", "dev")
	if out, err := execute("current"); err != nil || out != "dev\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	os.Unsetenv("ENVMGR_PROFILE")
	if out, err := execute("current"); err != nil || out != "" {
		t.Fatalf("none active: out=%q err=%v", out, err)
	}
}
