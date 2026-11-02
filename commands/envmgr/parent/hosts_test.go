package parent

import (
	ctx "context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
)

// Never touch the real hosts file from these tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "envmgr-parent-test")
	if err != nil {
		panic(err)
	}
	os.Setenv(hosts.EnvFile, filepath.Join(dir, "no-such-hosts"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func startChild(t *testing.T, code, hostsFile string) {
	t.Helper()
	home := t.TempDir()
	b, err := agent.Join(ctx.Background(), filepath.Join(home, "agent"), code, "", false)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := agent.NewAgent(b, &profile.Store{Dir: home}, nil)
	a.HostsFile = hostsFile
	cctx, cancel := ctx.WithCancel(ctx.Background())
	t.Cleanup(cancel)
	go func() { _ = a.Run(cctx) }()
}

func joinCode(t *testing.T, args ...string) string {
	t.Helper()
	out := mustRun(t, append([]string{"parent", "invite"}, args...)...)
	return strings.TrimSpace(strings.TrimPrefix(out, "envmgr join "))
}

func TestParentHostsEndToEnd(t *testing.T) {
	t.Setenv("ENVMGR_HOME", t.TempDir())
	dir, _ := agent.ParentDir()
	rh, err := agent.StartHub(agent.HubConfig{State: agent.NewHubState(dir), PollTimeout: 300 * time.Millisecond, RelayTimeout: 5 * time.Second}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rh.Close()

	_, stderr, err := run(t, "parent", "invite", "--name", "kid", "--allow-hosts")
	if err != nil || !strings.Contains(stderr, "caps hosts") || !strings.Contains(stderr, "warning") {
		t.Fatalf("invite --allow-hosts: %v %q", err, stderr)
	}
	_ = mustRun(t, "parent", "cancel", "kid")
	kidCode := joinCode(t, "--name", "kid", "--allow-hosts")
	plainCode := joinCode(t, "--name", "plain")
	if inv := mustRun(t, "parent", "invites"); !strings.Contains(inv, "CAPS") || !strings.Contains(inv, "hosts") {
		t.Fatalf("invites %q", inv)
	}

	const original = "127.0.0.1 localhost\n# keep\n"
	hf := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hf, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	plainHosts := filepath.Join(t.TempDir(), "hosts")
	_ = os.WriteFile(plainHosts, []byte(original), 0o644)
	startChild(t, kidCode, hf)
	startChild(t, plainCode, plainHosts)
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(mustRun(t, "parent", "agents"), "yes") < 2 {
		if time.Now().After(deadline) {
			t.Fatal("children never came online")
		}
		time.Sleep(20 * time.Millisecond)
	}
	agents := mustRun(t, "parent", "agents")
	if !strings.Contains(agents, "CAPS") {
		t.Fatalf("agents %q", agents)
	}
	for _, line := range strings.Split(agents, "\n") {
		f := strings.Fields(line)
		if len(f) > 2 && f[0] == "kid" && f[2] != "hosts" || len(f) > 2 && f[0] == "plain" && f[2] != "-" {
			t.Fatalf("caps column: %q", line)
		}
	}

	if _, _, err := run(t, "parent", "hosts", "plain", "list"); err == nil || !strings.Contains(err.Error(), "--allow-hosts") {
		t.Fatalf("plain child: %v", err)
	}

	out, stderr, err := run(t, "parent", "hosts", "kid", "add", "10.0.0.5", "api.dev.local", "--group", "dev", "--dry-run")
	if err != nil || !strings.Contains(out, "+10.0.0.5 api.dev.local") || !strings.Contains(stderr, "dry run") {
		t.Fatalf("dry run: %v %q %q", err, out, stderr)
	}
	if b, _ := os.ReadFile(hf); string(b) != original {
		t.Fatal("dry run wrote")
	}
	if _, stderr, err = run(t, "parent", "hosts", "kid", "add", "10.0.0.5", "api.dev.local", "db.dev.local", "--group", "dev"); err != nil || !strings.Contains(stderr, "updated") {
		t.Fatalf("add: %v %q", err, stderr)
	}
	out = mustRun(t, "parent", "hosts", "kid", "list")
	if !strings.Contains(out, "dev") || !strings.Contains(out, "api.dev.local db.dev.local") {
		t.Fatalf("list %q", out)
	}
	mustRun(t, "parent", "hosts", "kid", "group", "disable", "dev")
	if out := mustRun(t, "parent", "hosts", "kid", "list"); !strings.Contains(out, "disabled") {
		t.Fatalf("list after disable %q", out)
	}
	mustRun(t, "parent", "hosts", "kid", "group", "enable", "dev")
	mustRun(t, "parent", "hosts", "kid", "remove", "db.dev.local", "--group", "dev")
	b, _ := os.ReadFile(hf)
	if !strings.HasPrefix(string(b), original+hosts.BeginMarker+"\n") || !strings.Contains(string(b), "\n10.0.0.5 api.dev.local\n") {
		t.Fatalf("child hosts file %q", b)
	}
	for _, bad := range [][]string{
		{"parent", "hosts", "kid", "bogus"},
		{"parent", "hosts", "kid", "add", "10.0.0.1"},
		{"parent", "hosts", "kid", "add", "10.0.0.1", "a#b"},
		{"parent", "hosts", "kid", "add", "10.0.0.1", "localhost"},
		{"parent", "hosts", "kid", "group", "toggle", "dev"},
		{"parent", "hosts", "kid", "list", "--dry-run"},
	} {
		if _, _, err := run(t, bad...); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}

	// A profile with hosts: push (a profile edit), show both sections, apply
	// the child's copy, unapply.
	local, _ := profile.NewStore()
	if err := local.SetVar("web", "TOKEN", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := local.SetHost("web", "10.0.0.7", "web.dev.local"); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, "parent", "push", "plain", "web"); err != nil || !strings.Contains(stderr, "1 hosts entries") {
		t.Fatalf("push to a child without the grant: %v %q", err, stderr)
	}
	mustRun(t, "parent", "push", "kid", "web")
	if out := mustRun(t, "parent", "show", "kid", "web"); out != "# variables\nTOKEN=********\n# hosts\n10.0.0.7 web.dev.local\n" {
		t.Fatalf("show %q", out)
	}
	if _, _, err := run(t, "parent", "hosts", "plain", "apply-profile", "web"); err == nil || !strings.Contains(err.Error(), "--allow-hosts") {
		t.Fatalf("apply-profile without grant: %v", err)
	}
	out, _, err = run(t, "parent", "hosts", "kid", "apply-profile", "web", "--dry-run")
	if err != nil || !strings.Contains(out, "+10.0.0.7 web.dev.local") {
		t.Fatalf("apply-profile dry run: %v %q", err, out)
	}
	if _, stderr, err := run(t, "parent", "hosts", "kid", "apply-profile", "web"); err != nil || !strings.Contains(stderr, `hosts of profile "web" applied`) {
		t.Fatalf("apply-profile: %v %q", err, stderr)
	}
	if b, _ := os.ReadFile(hf); !strings.Contains(string(b), "# group: profile:web\n10.0.0.7 web.dev.local\n") || !strings.HasPrefix(string(b), original) {
		t.Fatalf("after apply-profile %q", b)
	}
	if _, _, err := run(t, "parent", "hosts", "kid", "apply-profile", "nope"); err == nil {
		t.Fatal("unknown child profile accepted")
	}
	if _, stderr, err := run(t, "parent", "hosts", "kid", "unapply"); err != nil || !strings.Contains(stderr, `removed the hosts of profile "web"`) {
		t.Fatalf("unapply: %v %q", err, stderr)
	}
	if b, _ := os.ReadFile(hf); strings.Contains(string(b), "profile:web") {
		t.Fatalf("after unapply %q", b)
	}
	if _, _, err := run(t, "parent", "hosts", "kid", "apply-profile"); err == nil {
		t.Fatal("apply-profile without a name accepted")
	}

	mustRun(t, "parent", "revoke", "kid")
	if _, _, err := run(t, "parent", "hosts", "kid", "list"); err == nil {
		t.Fatal("hosts op after revoke")
	}
}
