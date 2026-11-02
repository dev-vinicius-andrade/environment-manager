package remote

import (
	"bytes"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "envmgr", SilenceErrors: true}
	CreateCommand(&context.EnvContext{}, root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := run(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return out
}

func TestRemoteCommandsEndToEnd(t *testing.T) {
	// Machine B (server) has its own home; machine A (client) uses ENVMGR_HOME.
	serverHome, clientHome := t.TempDir(), t.TempDir()
	t.Setenv("ENVMGR_HOME", clientHome)

	id, err := agent.LoadOrCreateIdentity(filepath.Join(serverHome, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	serverStore := &profile.Store{Dir: serverHome}
	srv, err := agent.NewServer(agent.Config{Store: serverStore, Token: id.Token})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := agent.NewHTTPServer(srv.Handler(), id.Cert)
	go func() { _ = hs.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = hs.Close() })

	local := &profile.Store{Dir: clientHome}
	if err := local.Save(profile.Profile{Name: "dev", Vars: map[string]string{"API_KEY": "topsecret", "URL": "http://x"}}); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "remote", "add", "b", ln.Addr().String(), "--token", id.Token, "--fingerprint", id.Fingerprint)
	if _, err := run(t, "remote", "add", "b", ln.Addr().String(), "--token", id.Token, "--fingerprint", id.Fingerprint); err == nil {
		t.Fatal("duplicate add without --force succeeded")
	}
	if out := mustRun(t, "remote", "list"); !strings.Contains(out, "b\t") || strings.Contains(out, id.Token) {
		t.Fatalf("list output %q", out)
	}

	mustRun(t, "remote", "push", "b", "dev")
	if out := mustRun(t, "remote", "profiles", "b"); out != "dev\n" {
		t.Fatalf("profiles = %q", out)
	}
	out := mustRun(t, "remote", "show", "b", "dev")
	if strings.Contains(out, "topsecret") || !strings.Contains(out, "API_KEY="+agent.MaskedValue) {
		t.Fatalf("show not masked: %q", out)
	}
	if out := mustRun(t, "remote", "show", "b", "dev", "--reveal"); !strings.Contains(out, "API_KEY=topsecret") {
		t.Fatalf("show --reveal: %q", out)
	}

	mustRun(t, "remote", "set", "b", "dev", "NEW_VAR", "v1")
	mustRun(t, "remote", "unset", "b", "dev", "URL")
	if p, _ := serverStore.Get("dev"); p.Vars["NEW_VAR"] != "v1" || p.Vars["URL"] != "" {
		t.Fatalf("server profile %v", p.Vars)
	}

	if _, err := run(t, "remote", "pull", "b", "dev"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("pull over existing local profile: %v", err)
	}
	mustRun(t, "remote", "pull", "b", "dev", "--force")
	if p, _ := local.Get("dev"); p.Vars["NEW_VAR"] != "v1" {
		t.Fatalf("local after pull %v", p.Vars)
	}
	_ = serverStore.Save(profile.Profile{Name: "only-remote", Vars: map[string]string{"Z": "1"}})
	mustRun(t, "remote", "pull", "b", "only-remote")
	if _, err := local.Get("only-remote"); err != nil {
		t.Fatal(err)
	}

	mustRun(t, "remote", "delete", "b", "dev")
	if _, err := serverStore.Get("dev"); err != profile.ErrNotFound {
		t.Fatalf("server dev after delete: %v", err)
	}

	// Wrong fingerprint is refused.
	wrong := strings.Repeat("00", 32)
	mustRun(t, "remote", "add", "evil", ln.Addr().String(), "--token", id.Token, "--fingerprint", wrong)
	if _, err := run(t, "remote", "profiles", "evil"); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("pinned mismatch not refused: %v", err)
	}

	mustRun(t, "remote", "remove", "b")
	if _, err := run(t, "remote", "profiles", "b"); err == nil {
		t.Fatal("removed remote still usable")
	}
}
