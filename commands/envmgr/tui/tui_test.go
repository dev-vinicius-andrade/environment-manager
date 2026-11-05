package tui

import (
	"bytes"
	stdcontext "context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
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

// execute runs "envmgr <args>" with keys as the terminal input; stdout and
// stderr are captured separately (stderr receives the rendered UI).
func execute(t *testing.T, keys string, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "envmgr"}
	CreateCommand(&context.EnvContext{}, root)
	var out, errb bytes.Buffer
	root.SetIn(strings.NewReader(keys))
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 20*time.Second)
	defer cancel()
	err := root.ExecuteContext(ctx)
	return out.String(), errb.String(), err
}

func TestActivateWritesOnlyShellCodeToStdout(t *testing.T) {
	st := setup(t)
	if err := st.SetVar("dev", "MY_URL", "http://x/'y'"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MY_URL", "orig")
	p, _ := st.Get("dev")
	for _, kind := range []shell.Kind{shell.Bash, shell.PowerShell} {
		want, err := shell.Use(kind, shell.OSLookup, p)
		if err != nil {
			t.Fatal(err)
		}
		out, errOut, err := execute(t, "\r", "tui", "--shell", string(kind))
		if err != nil {
			t.Fatalf("%s: %v (stderr %q)", kind, err, errOut)
		}
		if out != want {
			t.Fatalf("%s: stdout\n got %q\nwant %q", kind, out, want)
		}
		if !strings.Contains(errOut, `activated profile "dev"`) {
			t.Fatalf("%s: stderr lacks confirmation: %q", kind, errOut)
		}
		if strings.Contains(errOut, "http://x") {
			t.Fatalf("%s: value leaked to the UI while masked", kind)
		}
	}
}

func TestQuitWritesNothingToStdout(t *testing.T) {
	st := setup(t)
	_ = st.SetVar("dev", "MY_URL", "x")
	// One key per run: Bubble Tea groups several runes read at once into a
	// single (paste-like) key message.
	for _, keys := range []string{"q", "\x1b", "\x03"} {
		out, _, err := execute(t, keys, "tui", "--shell", "bash")
		if err != nil {
			t.Fatalf("%q: %v", keys, err)
		}
		if out != "" {
			t.Fatalf("%q: stdout %q", keys, out)
		}
	}
}

func TestBadShellFails(t *testing.T) {
	setup(t)
	out, _, err := execute(t, "q", "tui", "--shell", "tcsh")
	if err == nil || out != "" {
		t.Fatalf("err=%v out=%q", err, out)
	}
}
