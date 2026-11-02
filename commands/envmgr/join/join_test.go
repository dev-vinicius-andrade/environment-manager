package join

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcmd "github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/agent"
	remote "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "envmgr", SilenceErrors: true}
	CreateCommand(&context.EnvContext{}, root)
	agentcmd.CreateCommand(&context.EnvContext{}, root)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestJoinStatusLeave(t *testing.T) {
	st := remote.NewHubState(filepath.Join(t.TempDir(), "parent"))
	rh, err := remote.StartHub(remote.HubConfig{State: st, Label: "hub1"}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rh.Close()
	t.Setenv("ENVMGR_HOME", t.TempDir()) // the child's home

	if _, err := run(t, "agent", "status"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("status before join: %v", err)
	}
	code1, _, _ := remote.CreateJoinCode(st, rh.Identity.Fingerprint, rh.PublicAddr, "", remote.RoleReadOnly, time.Minute)
	if _, err := run(t, "join", code1, "--name", "box", "--no-run"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "agent", "status")
	if err != nil || !strings.Contains(out, "hub1") || !strings.Contains(out, "box") || !strings.Contains(out, "role:        ro") || !strings.Contains(out, "connected:   yes") {
		t.Fatalf("status: %q %v", out, err)
	}
	if strings.Contains(out, "credential\":") {
		t.Fatal("status printed the credential")
	}
	b, _ := func() (*remote.Binding, error) { d, _ := remote.AgentDir(); return remote.LoadBinding(d) }()
	if strings.Contains(out, b.Credential) {
		t.Fatal("status printed the credential")
	}

	// Bound to exactly one parent.
	code2, _, _ := remote.CreateJoinCode(st, rh.Identity.Fingerprint, rh.PublicAddr, "other", remote.RoleReadWrite, time.Minute)
	if _, err := run(t, "join", code2, "--no-run"); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("second join: %v", err)
	}
	// Reusing a consumed code fails even with --force.
	if _, err := run(t, "join", code1, "--no-run", "--force"); err == nil {
		t.Fatal("consumed code accepted")
	}

	if _, err := run(t, "agent", "leave"); err != nil {
		t.Fatal(err)
	}
	if agents, _ := st.Agents(); len(agents) != 0 {
		t.Fatalf("parent still has %+v", agents)
	}
	if _, err := run(t, "agent", "run"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("run after leave: %v", err)
	}
}
