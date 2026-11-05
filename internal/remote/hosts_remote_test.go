package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
)

// No test in this package may reach the real hosts file: the default
// resolution (NewAgent) lands on a missing file inside a temp dir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "envmgr-remote-test")
	if err != nil {
		panic(err)
	}
	os.Setenv(hosts.EnvFile, filepath.Join(dir, "no-such-hosts"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const hostsFixture = "127.0.0.1 localhost\r\n# keep this line\r\n10.1.1.1 printer.lan"

func tempHosts(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(p, []byte(hostsFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runHosts starts the child loop with its own temp hosts file.
func (c *child) runHosts(t *testing.T, e *hubEnv, hostsFile string) {
	t.Helper()
	a, err := NewAgent(c.binding, c.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.HostsFile != os.Getenv(hosts.EnvFile) {
		t.Fatalf("agent default hosts file %q ignores %s", a.HostsFile, hosts.EnvFile)
	}
	a.HostsFile = hostsFile
	a.MaxBackoff = 200 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel, c.done = cancel, make(chan error, 1)
	go func() { c.done <- a.Run(ctx) }()
	t.Cleanup(cancel)
	waitFor(t, func() bool {
		st, _ := e.admin.Agents()
		for _, s := range st {
			if s.Name == c.binding.Name && s.Online {
				return true
			}
		}
		return false
	})
}

func (e *hubEnv) inviteCaps(t *testing.T, name string, role Role, caps ...string) string {
	t.Helper()
	code, _, err := CreateJoinCode(e.st, e.rh.Identity.Fingerprint, e.rh.PublicAddr, name, role, 15*time.Minute, caps...)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func boolp(b bool) *bool { return &b }

func TestHostsNotGrantedByDefault(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.invite(t, "kid", RoleReadWrite), "")
	if len(c.binding.Caps) != 0 {
		t.Fatalf("caps granted by default: %v", c.binding.Caps)
	}
	hf := tempHosts(t)
	c.runHosts(t, e, hf)
	for _, j := range []Job{
		{Op: OpHostsList},
		{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local"}}},
		{Op: OpHostsRemove, Args: JobArgs{Hostnames: []string{"a.local"}}},
		{Op: OpHostsGroup, Args: JobArgs{Group: "dev", Enable: boolp(false)}},
	} {
		if _, err := e.admin.Relay("kid", j); err == nil || !strings.Contains(err.Error(), "--allow-hosts") {
			t.Fatalf("%s without grant: %v", j.Op, err)
		}
	}
	// The child refuses on its own too, even if a hub relayed the job.
	res := ExecuteJob(Target{Role: RoleReadWrite, HostsFile: hf}, Job{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local"}}})
	if res.OK || res.Code != CodeNotGranted {
		t.Fatalf("child without grant: %+v", res)
	}
	if res := Execute(nil, RoleReadWrite, Job{Op: OpHostsList}); res.OK || res.Code != CodeNotGranted {
		t.Fatalf("Execute (profiles only): %+v", res)
	}
	if readFile(t, hf) != hostsFixture {
		t.Fatal("hosts file changed")
	}
}

func TestHostsChildRefusesWhenHubGrantsButBindingDoesNot(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.inviteCaps(t, "kid", RoleReadWrite, CapHosts), "")
	c.binding.Caps = nil // the child's own record of the grant wins
	hf := tempHosts(t)
	c.runHosts(t, e, hf)
	res, err := e.admin.Relay("kid", Job{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local"}}})
	if err != nil || res.OK || res.Code != CodeNotGranted {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, hf) != hostsFixture {
		t.Fatal("hosts file changed")
	}
}

func TestHostsGrantedEndToEnd(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.inviteCaps(t, "kid", RoleReadWrite, CapHosts), "")
	if !HasCap(c.binding.Caps, CapHosts) {
		t.Fatalf("binding caps %v", c.binding.Caps)
	}
	if b, err := LoadBinding(c.dir); err != nil || !HasCap(b.Caps, CapHosts) {
		t.Fatalf("persisted binding %+v %v", b, err)
	}
	if rec, _ := e.st.Agent("kid"); !HasCap(rec.Caps, CapHosts) {
		t.Fatalf("agents.json caps %v", rec.Caps)
	}
	hf := tempHosts(t)
	c.runHosts(t, e, hf)
	if st, _ := e.admin.Agents(); len(st) != 1 || !HasCap(st[0].Caps, CapHosts) {
		t.Fatalf("status %+v", st)
	}

	// Dry run: diff back, nothing written.
	res := relayOK(t, e, "kid", Job{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.5", Hostnames: []string{"api.dev.local"}, Group: "dev", DryRun: true}})
	if res.Hosts == nil || !res.Hosts.Changed || !strings.Contains(res.Hosts.Diff, "+10.0.0.5 api.dev.local") || res.Hosts.Backup != "" {
		t.Fatalf("dry run %+v", res.Hosts)
	}
	if readFile(t, hf) != hostsFixture {
		t.Fatal("dry run wrote")
	}

	res = relayOK(t, e, "kid", Job{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.5", Hostnames: []string{"api.dev.local", "db.dev.local"}, Group: "dev"}})
	if !res.Hosts.Changed || res.Hosts.Backup == "" || !samePath(res.Hosts.Path, hf) {
		t.Fatalf("add %+v", res.Hosts)
	}
	got := readFile(t, hf)
	if !strings.HasPrefix(got, hostsFixture+"\r\n"+hosts.BeginMarker+"\r\n") || !strings.Contains(got, "\r\n10.0.0.5 api.dev.local db.dev.local\r\n") {
		t.Fatalf("file %q", got)
	}
	if res := relayOK(t, e, "kid", Job{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.5", Hostnames: []string{"api.dev.local"}, Group: "dev"}}); res.Hosts.Changed {
		t.Fatal("idempotent add changed")
	}
	res = relayOK(t, e, "kid", Job{Op: OpHostsList})
	if g := res.Hosts.Groups; len(g) != 1 || g[0].Name != "dev" || !g[0].Enabled || len(g[0].Entries[0].Hostnames) != 2 {
		t.Fatalf("list %+v", res.Hosts)
	}
	relayOK(t, e, "kid", Job{Op: OpHostsGroup, Args: JobArgs{Group: "dev", Enable: boolp(false)}})
	if !strings.Contains(readFile(t, hf), "# 10.0.0.5 api.dev.local db.dev.local") {
		t.Fatal("group not disabled")
	}
	relayOK(t, e, "kid", Job{Op: OpHostsGroup, Args: JobArgs{Group: "dev", Enable: boolp(true)}})
	relayOK(t, e, "kid", Job{Op: OpHostsRemove, Args: JobArgs{Hostnames: []string{"db.dev.local"}}})
	res = relayOK(t, e, "kid", Job{Op: OpHostsList})
	if h := res.Hosts.Groups[0].Entries[0].Hostnames; len(h) != 1 || h[0] != "api.dev.local" {
		t.Fatalf("after remove %v", h)
	}
	if res, err := e.admin.Relay("kid", Job{Op: OpHostsRemove, Args: JobArgs{Hostnames: []string{"nope.local"}}}); err != nil || res.OK || !res.NotFound {
		t.Fatalf("remove missing %+v %v", res, err)
	}

	// Validation at the hub (same code as local) and critical names on the child.
	for _, j := range []Job{
		{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local\n6.6.6.6 bank.example"}}},
		{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a#b"}}},
		{Op: OpHostsAdd, Args: JobArgs{IP: "fe80::1%eth0", Hostnames: []string{"a.local"}}},
		{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local"}, Group: "../x"}},
		{Op: OpHostsRemove, Args: JobArgs{Hostnames: []string{"a.local", "b.local"}}},
		{Op: OpHostsGroup, Args: JobArgs{Group: "dev"}},
		{Op: "hosts.exec"},
	} {
		if _, err := e.admin.Relay("kid", j); err == nil {
			t.Fatalf("invalid job accepted: %+v", j)
		}
	}
	if res, err := e.admin.Relay("kid", Job{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"localhost"}}}); err != nil || res.OK || !strings.Contains(res.Error, "critical") {
		t.Fatalf("critical name %+v %v", res, err)
	}
	if !strings.HasPrefix(readFile(t, hf), hostsFixture+"\r\n") {
		t.Fatal("unmanaged lines changed")
	}

	// Revoke stops hosts operations.
	if err := e.st.Revoke("kid"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Relay("kid", Job{Op: OpHostsList}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("after revoke: %v", err)
	}
	select {
	case err := <-c.done:
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("loop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child not stopped by revoke")
	}
}

func TestHostsReadOnlyChild(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.inviteCaps(t, "kiosk", RoleReadOnly, CapHosts), "")
	hf := tempHosts(t)
	c.runHosts(t, e, hf)
	if res := relayOK(t, e, "kiosk", Job{Op: OpHostsList}); res.Hosts == nil || !samePath(res.Hosts.Path, hf) {
		t.Fatalf("ro list %+v", res)
	}
	muts := []Job{
		{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local"}}},
		{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local"}, DryRun: true}},
		{Op: OpHostsRemove, Args: JobArgs{Hostnames: []string{"a.local"}}},
		{Op: OpHostsGroup, Args: JobArgs{Group: "dev", Enable: boolp(true)}},
	}
	for _, j := range muts {
		if _, err := e.admin.Relay("kiosk", j); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("ro %s: %v", j.Op, err)
		}
		// And the child refuses by itself.
		if res := ExecuteJob(Target{Role: RoleReadOnly, Caps: []string{CapHosts}, HostsFile: hf}, j); res.OK || !strings.Contains(res.Error, "read-only") {
			t.Fatalf("child ro %s: %+v", j.Op, res)
		}
	}
	if readFile(t, hf) != hostsFixture {
		t.Fatal("read-only child's hosts file changed")
	}
}

func TestHostsPathFromNetworkIgnored(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.inviteCaps(t, "kid", RoleReadWrite, CapHosts), "")
	hf := tempHosts(t)
	c.runHosts(t, e, hf)
	evil := filepath.Join(t.TempDir(), "evil-hosts")

	// The admin API rejects unknown fields such as a path.
	body := map[string]any{"agent": "kid", "job": map[string]any{"op": OpHostsAdd, "args": map[string]any{
		"ip": "10.0.0.1", "hostnames": []string{"a.local"}, "path": evil}}}
	_, err := doJSON(context.Background(), e.admin.http, http.MethodPost, "https://"+e.admin.info.Addr+"/admin/v1/relay", e.admin.info.Token, body, nil)
	if StatusOf(err) != http.StatusBadRequest {
		t.Fatalf("admin relay with path: %v", err)
	}

	// A job carrying a path (e.g. from a modified parent) is decoded without
	// it: the child only ever uses its own file.
	var j Job
	raw := `{"id":"x","op":"hosts.add","args":{"ip":"10.0.0.1","hostnames":["a.local"],"path":` + strconvQuote(evil) + `,"file":` + strconvQuote(evil) + `}}`
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		t.Fatal(err)
	}
	res := ExecuteJob(Target{Role: RoleReadWrite, Caps: []string{CapHosts}, HostsFile: hf}, j)
	if !res.OK || !samePath(res.Hosts.Path, hf) {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(evil); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("network path was used")
	}
	if !strings.Contains(readFile(t, hf), "10.0.0.1 a.local") {
		t.Fatal("own hosts file not edited")
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestHostsPermissionDeniedIsStructured(t *testing.T) {
	hf := tempHosts(t)
	dir := filepath.Dir(hf)
	_ = os.Chmod(hf, 0o444)
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dir, 0o555)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.Chmod(hf, 0o644)
	})
	res := ExecuteJob(Target{Role: RoleReadWrite, Caps: []string{CapHosts}, HostsFile: hf}, Job{Op: OpHostsAdd, Args: JobArgs{IP: "10.0.0.1", Hostnames: []string{"a.local"}}})
	if res.OK {
		t.Skip("this OS/user ignores read-only permissions (running as root?)")
	}
	if res.Code != CodePermissionDenied || !strings.Contains(res.Error, "elevated") {
		t.Fatalf("%+v", res)
	}
}

// samePath compares two paths after resolving symlinks and short names (macOS
// /var -> /private/var, Windows 8.3 temp dirs): the code resolves the path it
// uses, the test builds it from t.TempDir().
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}
