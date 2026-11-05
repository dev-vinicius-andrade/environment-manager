package remote

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

type hubEnv struct {
	st    *HubState
	rh    *RunningHub
	admin *AdminClient
}

func startHub(t *testing.T, mutate func(*HubConfig)) *hubEnv {
	t.Helper()
	st := NewHubState(filepath.Join(t.TempDir(), "parent"))
	cfg := HubConfig{State: st, Label: "parent-test", PollTimeout: 300 * time.Millisecond, RelayTimeout: 5 * time.Second, OfflineAfter: 2 * time.Second}
	if mutate != nil {
		mutate(&cfg)
	}
	rh, err := StartHub(cfg, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rh.Close() })
	admin, err := NewAdminClient(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return &hubEnv{st: st, rh: rh, admin: admin}
}

func (e *hubEnv) invite(t *testing.T, name string, role Role) string {
	t.Helper()
	code, _, err := CreateJoinCode(e.st, e.rh.Identity.Fingerprint, e.rh.PublicAddr, name, role, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

type child struct {
	home    string
	dir     string
	store   *profile.Store
	binding *Binding
	cancel  context.CancelFunc
	done    chan error
}

func joinChild(t *testing.T, code, name string) *child {
	t.Helper()
	home := t.TempDir()
	c := &child{home: home, dir: filepath.Join(home, "agent"), store: &profile.Store{Dir: home}}
	b, err := Join(context.Background(), c.dir, code, name, false)
	if err != nil {
		t.Fatal(err)
	}
	c.binding = b
	return c
}

func (c *child) run(t *testing.T, e *hubEnv) {
	t.Helper()
	a, err := NewAgent(c.binding, c.store, nil)
	if err != nil {
		t.Fatal(err)
	}
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

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func ptr(s string) *string { return &s }

func relayOK(t *testing.T, e *hubEnv, agent string, job Job) Result {
	t.Helper()
	res, err := e.admin.Relay(agent, job)
	if err != nil {
		t.Fatalf("relay %s %+v: %v", agent, job, err)
	}
	if !res.OK {
		t.Fatalf("relay %s %+v: child error %q", agent, job, res.Error)
	}
	return res
}

func TestFullFlowTwoChildren(t *testing.T) {
	e := startHub(t, nil)
	c1 := joinChild(t, e.invite(t, "child1", RoleReadWrite), "")
	c2 := joinChild(t, e.invite(t, "", RoleReadOnly), "laptop") // child proposes its name
	if c1.binding.Name != "child1" || c2.binding.Name != "laptop" || c2.binding.Role != RoleReadOnly || c1.binding.Parent != "parent-test" {
		t.Fatalf("bindings: %+v %+v", c1.binding, c2.binding)
	}
	_ = c2.store.Save(profile.Profile{Name: "x", Vars: map[string]string{"K": "v"}})
	c1.run(t, e)
	c2.run(t, e)

	st, err := e.admin.Agents()
	if err != nil || len(st) != 2 || st[0].Name != "child1" || st[1].Name != "laptop" || !st[0].Online || !st[1].Online || st[1].Role != RoleReadOnly {
		t.Fatalf("agents: %+v %v", st, err)
	}

	relayOK(t, e, "child1", Job{Op: OpPut, Args: JobArgs{Profile: "dev", Vars: map[string]string{"A": "1", "B": "secret"}}})
	if res := relayOK(t, e, "child1", Job{Op: OpList}); !reflect.DeepEqual(res.Profiles, []string{"dev"}) {
		t.Fatalf("list: %v", res.Profiles)
	}
	relayOK(t, e, "child1", Job{Op: OpSet, Args: JobArgs{Profile: "dev", Var: "C", Value: ptr("3")}})
	relayOK(t, e, "child1", Job{Op: OpUnset, Args: JobArgs{Profile: "dev", Var: "A"}})
	res := relayOK(t, e, "child1", Job{Op: OpGet, Args: JobArgs{Profile: "dev"}})
	if want := map[string]string{"B": "secret", "C": "3"}; !reflect.DeepEqual(res.Profile.Vars, want) {
		t.Fatalf("get: %v", res.Profile.Vars)
	}
	if p, _ := c1.store.Get("dev"); p.Vars["C"] != "3" {
		t.Fatalf("child1 store: %v", p.Vars)
	}
	// Isolation: nothing reached child2.
	if names, _ := c2.store.List(); !reflect.DeepEqual(names, []string{"x"}) {
		t.Fatalf("child2 store touched: %v", names)
	}
	relayOK(t, e, "child1", Job{Op: OpDelete, Args: JobArgs{Profile: "dev"}})
	if res, err := e.admin.Relay("child1", Job{Op: OpGet, Args: JobArgs{Profile: "dev"}}); err != nil || res.OK || !res.NotFound {
		t.Fatalf("get after delete: %+v %v", res, err)
	}

	// Read-only child: reads work, mutations refused by the hub.
	if res := relayOK(t, e, "laptop", Job{Op: OpGet, Args: JobArgs{Profile: "x"}}); res.Profile.Vars["K"] != "v" {
		t.Fatalf("ro get: %+v", res)
	}
	for _, j := range []Job{
		{Op: OpPut, Args: JobArgs{Profile: "x", Vars: map[string]string{}}},
		{Op: OpSet, Args: JobArgs{Profile: "x", Var: "K", Value: ptr("2")}},
		{Op: OpUnset, Args: JobArgs{Profile: "x", Var: "K"}},
		{Op: OpDelete, Args: JobArgs{Profile: "x"}},
	} {
		if _, err := e.admin.Relay("laptop", j); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("ro mutation %s: %v", j.Op, err)
		}
	}
	if p, _ := c2.store.Get("x"); p.Vars["K"] != "v" {
		t.Fatal("read-only child was mutated")
	}

	// Unknown agent.
	if _, err := e.admin.Relay("nobody", Job{Op: OpList}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestChildEnforcesReadOnly(t *testing.T) {
	store := &profile.Store{Dir: t.TempDir()}
	_ = store.Save(profile.Profile{Name: "dev", Vars: map[string]string{"A": "1"}})
	for _, j := range []Job{
		{Op: OpPut, Args: JobArgs{Profile: "dev"}},
		{Op: OpSet, Args: JobArgs{Profile: "dev", Var: "A", Value: ptr("2")}},
		{Op: OpUnset, Args: JobArgs{Profile: "dev", Var: "A"}},
		{Op: OpDelete, Args: JobArgs{Profile: "dev"}},
	} {
		if res := Execute(store, RoleReadOnly, j); res.OK || !strings.Contains(res.Error, "read-only") {
			t.Fatalf("%s: %+v", j.Op, res)
		}
	}
	if p, _ := store.Get("dev"); p.Vars["A"] != "1" {
		t.Fatal("mutated")
	}
	if res := Execute(store, RoleReadOnly, Job{Op: OpGet, Args: JobArgs{Profile: "dev"}}); !res.OK {
		t.Fatalf("ro get: %+v", res)
	}
	if res := Execute(store, RoleReadWrite, Job{Op: "exec", Args: JobArgs{Profile: "dev"}}); res.OK || !strings.Contains(res.Error, "unknown operation") {
		t.Fatalf("unknown op: %+v", res)
	}
}

func TestInviteSingleUseAndExpiry(t *testing.T) {
	e := startHub(t, nil)
	code := e.invite(t, "once", RoleReadWrite)
	joinChild(t, code, "")
	home := t.TempDir()
	if _, err := Join(context.Background(), filepath.Join(home, "agent"), code, "", false); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("second join with same code: %v", err)
	}
	if invs, _ := e.st.Invites(); len(invs) != 0 {
		t.Fatalf("invite not consumed: %+v", invs)
	}

	// Server side: an expired invite is rejected with a bare 401.
	code = e.invite(t, "late", RoleReadWrite)
	jc, _ := DecodeJoinCode(code)
	e.st.now = func() time.Time { return time.Now().Add(time.Hour) }
	c, _ := pinnedClient(jc.FP, 5*time.Second)
	var resp EnrollResponse
	_, err := doJSON(context.Background(), c, http.MethodPost, "https://"+jc.Addr+"/v1/enroll", "", enrollRequest{Secret: jc.Secret}, &resp)
	if StatusOf(err) != http.StatusUnauthorized {
		t.Fatalf("expired invite: %v", err)
	}
	e.st.now = time.Now

	// Client side: an expired code is refused before dialing.
	jc.Exp = time.Now().Add(-time.Minute).Unix()
	if _, err := Join(context.Background(), filepath.Join(t.TempDir(), "agent"), jc.Encode(), "", false); !errors.Is(err, ErrInviteExpired) {
		t.Fatalf("expired code: %v", err)
	}

	// Cancelled invite.
	code = e.invite(t, "cancelme", RoleReadWrite)
	if err := e.st.CancelInvite("cancelme"); err != nil {
		t.Fatal(err)
	}
	if _, err := Join(context.Background(), filepath.Join(t.TempDir(), "agent"), code, "", false); err == nil {
		t.Fatal("cancelled invite accepted")
	}
	// Parent-fixed names cannot be overridden by the child.
	c3 := joinChild(t, e.invite(t, "fixed", RoleReadWrite), "other")
	if c3.binding.Name != "fixed" {
		t.Fatalf("name override: %s", c3.binding.Name)
	}
}

func TestWrongFingerprintRefused(t *testing.T) {
	e := startHub(t, nil)
	code := e.invite(t, "c", RoleReadWrite)
	jc, _ := DecodeJoinCode(code)
	good := jc.FP
	jc.FP = strings.Repeat("ab", 32)
	if _, err := Join(context.Background(), filepath.Join(t.TempDir(), "agent"), jc.Encode(), "", false); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}
	// The secret never reached the hub, so the real code still works.
	jc.FP = good
	joinChild(t, jc.Encode(), "")

	// A bound child also refuses a parent presenting another certificate.
	other := startHub(t, nil)
	c := joinChild(t, other.invite(t, "x", RoleReadWrite), "")
	c.binding.Addr = e.rh.PublicAddr // same fingerprint pin, different server
	a, _ := NewAgent(c.binding, c.store, nil)
	if err := a.Run(context.Background()); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("agent loop against other cert: %v", err)
	}
}

func TestAlreadyBound(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.invite(t, "first", RoleReadWrite), "")
	code2 := e.invite(t, "second", RoleReadWrite)
	if _, err := Join(context.Background(), c.dir, code2, "", false); !errors.Is(err, ErrAlreadyBound) {
		t.Fatalf("want ErrAlreadyBound, got %v", err)
	}
	b, err := Join(context.Background(), c.dir, code2, "", true)
	if err != nil || b.Name != "second" {
		t.Fatalf("force join: %+v %v", b, err)
	}
	if lb, _ := LoadBinding(c.dir); lb.Name != "second" {
		t.Fatalf("binding not replaced: %+v", lb)
	}
	// The old identity left the parent (best-effort notify).
	if _, err := e.st.Agent("first"); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("old agent still enrolled: %v", err)
	}
}

func TestRevokeStopsChild(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.invite(t, "victim", RoleReadWrite), "")
	c.run(t, e)
	if err := e.st.Revoke("victim"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-c.done:
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("loop ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child kept running after revoke")
	}
	a, _ := NewAgent(c.binding, c.store, nil)
	if _, err := a.Ping(context.Background()); !errors.Is(err, ErrRevoked) {
		t.Fatalf("ping after revoke: %v", err)
	}
}

func TestRevokeEndsOpenLongPoll(t *testing.T) {
	e := startHub(t, func(c *HubConfig) { c.PollTimeout = time.Minute })
	c := joinChild(t, e.invite(t, "held", RoleReadWrite), "")
	c.run(t, e)
	time.Sleep(100 * time.Millisecond) // let the long-poll open
	start := time.Now()
	if err := e.st.Revoke("held"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-c.done:
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("loop ended with %v", err)
		}
		if d := time.Since(start); d > revokeCheckInterval+2*time.Second {
			t.Fatalf("revoke took %v", d)
		}
	case <-time.After(revokeCheckInterval + 3*time.Second):
		t.Fatal("open long-poll survived revoke")
	}
}

func TestOfflineChild(t *testing.T) {
	e := startHub(t, nil)
	joinChild(t, e.invite(t, "sleepy", RoleReadWrite), "") // enrolled, loop not running
	start := time.Now()
	_, err := e.admin.Relay("sleepy", Job{Op: OpList})
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline relay: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("offline error was not fast")
	}
	if st, _ := e.admin.Agents(); len(st) != 1 || st[0].Online {
		t.Fatalf("status: %+v", st)
	}
}

func TestAdminAPIProtected(t *testing.T) {
	e := startHub(t, nil)
	if !IsLoopbackAddr(e.rh.AdminAddr) {
		t.Fatalf("admin listener not loopback: %s", e.rh.AdminAddr)
	}
	c, _ := pinnedClient(e.rh.Identity.Fingerprint, 5*time.Second)
	for _, tok := range []string{"", "wrong", strings.Repeat("0", 64)} {
		_, err := doJSON(context.Background(), c, http.MethodGet, "https://"+e.rh.AdminAddr+"/admin/v1/agents", tok, nil, nil)
		if StatusOf(err) != http.StatusUnauthorized {
			t.Fatalf("admin with token %q: %v", tok, err)
		}
	}
	// The admin API is not served on the public listener.
	_, err := doJSON(context.Background(), c, http.MethodGet, "https://"+e.rh.PublicAddr+"/admin/v1/agents", e.admin.Info().Token, nil, nil)
	if StatusOf(err) != http.StatusNotFound {
		t.Fatalf("admin on public listener: %v", err)
	}
	// Non-loopback peers are refused even with the token.
	h := e.rh.Hub.AdminHandler("tok")
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/agents", nil)
	req.RemoteAddr = "10.0.0.9:5555"
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback admin: %d", rec.Code)
	}
	// admin.json is removed on shutdown.
	_ = e.rh.Close()
	if _, err := ReadAdminInfo(e.st.Dir()); !errors.Is(err, ErrHubNotRunning) {
		t.Fatalf("admin.json after close: %v", err)
	}
}

func TestNoRawSecretsOnDisk(t *testing.T) {
	e := startHub(t, nil)
	var secrets []string
	codes := []string{e.invite(t, "a1", RoleReadWrite), e.invite(t, "a2", RoleReadOnly), e.invite(t, "pending", RoleReadWrite)}
	for _, code := range codes {
		jc, _ := DecodeJoinCode(code)
		secrets = append(secrets, jc.Secret)
	}
	c1 := joinChild(t, codes[0], "")
	c2 := joinChild(t, codes[1], "")
	c1.run(t, e)
	secrets = append(secrets, c1.binding.Credential, c2.binding.Credential)

	files := 0
	_ = filepath.Walk(e.st.Dir(), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		files++
		for _, s := range secrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s contains a raw secret", filepath.Base(p))
			}
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 && filepath.Ext(p) != ".crt" {
			t.Errorf("%s mode %v", filepath.Base(p), info.Mode().Perm())
		}
		return nil
	})
	if files < 5 { // crt, key, admin, agents, invites, seen
		t.Fatalf("only %d state files", files)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(c1.dir, bindingFile))
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("parent.json mode %v", st.Mode().Perm())
		}
	}
}

func TestChildrenIsolated(t *testing.T) {
	e := startHub(t, nil)
	c1 := joinChild(t, e.invite(t, "one", RoleReadWrite), "")
	c2 := joinChild(t, e.invite(t, "two", RoleReadWrite), "")
	hub := e.rh.Hub
	pj := &pendingJob{job: Job{ID: randomID(16), Op: OpList}, agent: "two", done: make(chan Result, 1)}
	hub.mu.Lock()
	hub.jobs[pj.job.ID] = pj
	hub.mu.Unlock()
	c, _ := pinnedClient(e.rh.Identity.Fingerprint, 5*time.Second)
	url := "https://" + e.rh.PublicAddr + "/v1/agent/result/" + pj.job.ID
	if _, err := doJSON(context.Background(), c, http.MethodPost, url, c1.binding.Credential, Result{OK: true, Profiles: []string{"forged"}}, nil); StatusOf(err) != http.StatusNotFound {
		t.Fatalf("child1 answered child2's job: %v", err)
	}
	if _, err := doJSON(context.Background(), c, http.MethodPost, url, c2.binding.Credential, Result{OK: true}, nil); err != nil {
		t.Fatalf("child2 answering own job: %v", err)
	}
	if res := <-pj.done; len(res.Profiles) != 0 {
		t.Fatalf("forged result delivered: %+v", res)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.invite(t, "c", RoleReadWrite), "")
	c.run(t, e)
	bad := []Job{
		{Op: OpGet, Args: JobArgs{Profile: "../../secret"}},
		{Op: OpGet, Args: JobArgs{Profile: `..\secret`}},
		{Op: OpDelete, Args: JobArgs{Profile: "a/b"}},
		{Op: OpPut, Args: JobArgs{Profile: ".hidden"}},
		{Op: OpPut, Args: JobArgs{Profile: "ok", Vars: map[string]string{"A;rm": "x"}}},
		{Op: OpSet, Args: JobArgs{Profile: "ok", Var: "../x", Value: ptr("x")}},
		{Op: OpUnset, Args: JobArgs{Profile: "ok", Var: "A-B"}},
		{Op: "exec", Args: JobArgs{Profile: "ok"}},
	}
	for _, j := range bad {
		if _, err := e.admin.Relay("c", j); err == nil {
			t.Errorf("hub relayed %+v", j)
		}
		if res := Execute(c.store, RoleReadWrite, j); res.OK {
			t.Errorf("child executed %+v", j)
		}
	}
	if _, err := e.admin.Relay("../c", Job{Op: OpList}); err == nil {
		t.Error("traversal agent name accepted")
	}
	if names, _ := c.store.List(); len(names) != 0 {
		t.Fatalf("store changed: %v", names)
	}
	if _, err := DecodeJoinCode("not-a-code"); err == nil {
		t.Error("garbage join code accepted")
	}
}

func TestBodyLimit(t *testing.T) {
	e := startHub(t, nil)
	c, _ := pinnedClient(e.rh.Identity.Fingerprint, 10*time.Second)
	big := strings.Repeat("a", MaxBodyBytes+1)
	_, err := doJSON(context.Background(), c, http.MethodPost, "https://"+e.rh.PublicAddr+"/v1/enroll", "", enrollRequest{Secret: big}, nil)
	if StatusOf(err) != http.StatusRequestEntityTooLarge {
		t.Fatalf("enroll oversized: %v", err)
	}
	_, err = e.admin.Relay("x", Job{Op: OpPut, Args: JobArgs{Profile: "p", Vars: map[string]string{"BIG": big}}})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("admin oversized: %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	e := startHub(t, func(c *HubConfig) { c.MaxAuthFailures = 3; c.now = clock })
	code := e.invite(t, "c", RoleReadWrite)
	jc, _ := DecodeJoinCode(code)
	c, _ := pinnedClient(jc.FP, 5*time.Second)
	url := "https://" + jc.Addr + "/v1/enroll"
	for i := 0; i < 3; i++ {
		_, err := doJSON(context.Background(), c, http.MethodPost, url, "", enrollRequest{Secret: strings.Repeat("0", 64)}, nil)
		if StatusOf(err) != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := Join(context.Background(), filepath.Join(t.TempDir(), "agent"), code, "", false); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("want lockout, got %v", err)
	}
	// Bad agent credentials count too, and the lockout expires.
	mu.Lock()
	now = now.Add(2 * time.Second)
	mu.Unlock()
	joinChild(t, code, "")

	l := newAuthLimiter(1, clock)
	l.fail("1.2.3.4")
	d1 := l.blocked("1.2.3.4")
	l.fail("1.2.3.4")
	if d2 := l.blocked("1.2.3.4"); !(d1 > 0 && d2 > d1) {
		t.Fatalf("backoff did not grow: %v %v", d1, d2)
	}
	for i := 0; i < 50; i++ {
		l.fail("1.2.3.4")
	}
	if d := l.blocked("1.2.3.4"); d > maxLockout {
		t.Fatalf("lockout %v exceeds cap", d)
	}
}

func TestMaskedOutput(t *testing.T) {
	p := profile.Profile{Name: "dev", Vars: map[string]string{"TOKEN": "s3cr3t", "A": "1"}}
	var buf bytes.Buffer
	_ = WriteProfile(&buf, p, false)
	if strings.Contains(buf.String(), "s3cr3t") || buf.String() != "A="+MaskedValue+"\nTOKEN="+MaskedValue+"\n" {
		t.Fatalf("masked output = %q", buf.String())
	}
	buf.Reset()
	_ = WriteProfile(&buf, p, true)
	if buf.String() != "A=1\nTOKEN=s3cr3t\n" {
		t.Fatalf("revealed output = %q", buf.String())
	}
}

func TestIdentityReused(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateIdentity(dir)
	if err != nil || !a.CreatedCert || len(a.Fingerprint) != 64 {
		t.Fatalf("first: %+v %v", a, err)
	}
	b, err := LoadOrCreateIdentity(dir)
	if err != nil || b.CreatedCert || b.Fingerprint != a.Fingerprint {
		t.Fatal("identity not reused")
	}
	_ = os.Remove(filepath.Join(dir, keyFile))
	if _, err := LoadOrCreateIdentity(dir); err == nil {
		t.Fatal("expected error with missing key")
	}
}
