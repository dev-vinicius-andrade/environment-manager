package parent

import (
	"bytes"
	ctx "context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "envmgr", SilenceErrors: true}
	CreateCommand(&context.EnvContext{}, root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, stderr, err := run(t, args...)
	if err != nil {
		t.Fatalf("%v: %v (%s)", args, err, stderr)
	}
	return out
}

func TestParentCommandsEndToEnd(t *testing.T) {
	parentHome := t.TempDir()
	t.Setenv("ENVMGR_HOME", parentHome)
	dir, _ := agent.ParentDir()
	st := agent.NewHubState(dir)

	// Offline invite without a running hub needs an explicit address.
	if _, _, err := run(t, "parent", "invite"); err == nil || !strings.Contains(err.Error(), "--addr-public") {
		t.Fatalf("invite without hub: %v", err)
	}

	rh, err := agent.StartHub(agent.HubConfig{State: st, PollTimeout: 300 * time.Millisecond, RelayTimeout: 5 * time.Second}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rh.Close()

	out := mustRun(t, "parent", "invite", "--name", "kid")
	if !strings.HasPrefix(out, "envmgr join ") {
		t.Fatalf("invite output %q", out)
	}
	code := strings.TrimSpace(strings.TrimPrefix(out, "envmgr join "))
	jc, err := agent.DecodeJoinCode(code)
	if err != nil || jc.Addr != rh.PublicAddr || jc.FP != rh.Identity.Fingerprint || jc.Name != "kid" {
		t.Fatalf("code %+v %v", jc, err)
	}
	mustRun(t, "parent", "invite", "--name", "spare", "--read-only", "--expires", "1h")
	inv := mustRun(t, "parent", "invites")
	if !strings.Contains(inv, "kid") || !strings.Contains(inv, "spare") || strings.Contains(inv, jc.Secret) {
		t.Fatalf("invites output %q", inv)
	}
	mustRun(t, "parent", "cancel", "spare")
	if inv := mustRun(t, "parent", "invites"); strings.Contains(inv, "spare") {
		t.Fatalf("cancelled invite still listed: %q", inv)
	}

	// The child (its own home) joins and runs.
	childHome := t.TempDir()
	childStore := &profile.Store{Dir: childHome}
	b, err := agent.Join(ctx.Background(), filepath.Join(childHome, "agent"), code, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "parent", "profiles", "kid"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline child: %v", err)
	}
	a, _ := agent.NewAgent(b, childStore, nil)
	cctx, cancel := ctx.WithCancel(ctx.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(cctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(mustRun(t, "parent", "agents"), "yes") {
		if time.Now().After(deadline) {
			t.Fatal("child never came online")
		}
		time.Sleep(20 * time.Millisecond)
	}

	local := &profile.Store{Dir: parentHome}
	_ = local.Save(profile.Profile{Name: "dev", Vars: map[string]string{"API_KEY": "topsecret", "URL": "http://x"}})
	mustRun(t, "parent", "push", "kid", "dev")
	if out := mustRun(t, "parent", "profiles", "kid"); out != "dev\n" {
		t.Fatalf("profiles %q", out)
	}
	out = mustRun(t, "parent", "show", "kid", "dev")
	if strings.Contains(out, "topsecret") || !strings.Contains(out, "API_KEY="+agent.MaskedValue) {
		t.Fatalf("show not masked: %q", out)
	}
	if out := mustRun(t, "parent", "show", "kid", "dev", "--reveal"); !strings.Contains(out, "API_KEY=topsecret") {
		t.Fatalf("show --reveal %q", out)
	}
	mustRun(t, "parent", "set", "kid", "dev", "NEW", "v1")
	mustRun(t, "parent", "unset", "kid", "dev", "URL")
	if p, _ := childStore.Get("dev"); p.Vars["NEW"] != "v1" || p.Vars["URL"] != "" {
		t.Fatalf("child store %v", p.Vars)
	}
	if _, _, err := run(t, "parent", "pull", "kid", "dev"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("pull over existing: %v", err)
	}
	mustRun(t, "parent", "pull", "kid", "dev", "--force")
	if p, _ := local.Get("dev"); p.Vars["NEW"] != "v1" {
		t.Fatalf("local after pull %v", p.Vars)
	}
	if _, _, err := run(t, "parent", "show", "kid", "../etc"); err == nil {
		t.Fatal("traversal name accepted")
	}
	mustRun(t, "parent", "delete", "kid", "dev")
	if _, err := childStore.Get("dev"); err != profile.ErrNotFound {
		t.Fatalf("child dev after delete: %v", err)
	}

	mustRun(t, "parent", "revoke", "kid")
	select {
	case err := <-done:
		if err != agent.ErrRevoked {
			t.Fatalf("child loop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child not stopped by revoke")
	}
	if out := mustRun(t, "parent", "agents"); strings.Contains(out, "kid") {
		t.Fatalf("revoked agent listed: %q", out)
	}
}
