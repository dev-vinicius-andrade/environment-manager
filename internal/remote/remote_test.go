package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
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

type testServer struct {
	addr  string
	id    *Identity
	store *profile.Store
	srv   *Server
}

func startServer(t *testing.T, mutate func(*Config)) *testServer {
	t.Helper()
	home := t.TempDir()
	id, err := LoadOrCreateIdentity(filepath.Join(home, "remote"))
	if err != nil {
		t.Fatal(err)
	}
	store := &profile.Store{Dir: home}
	cfg := Config{Store: store, Token: id.Token}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := NewHTTPServer(s.Handler(), id.Cert)
	go func() { _ = hs.ServeTLS(ln, "", "") }()
	t.Cleanup(func() { _ = hs.Close() })
	return &testServer{addr: ln.Addr().String(), id: id, store: store, srv: s}
}

func (ts *testServer) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(ts.addr, ts.id.Token, ts.id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// raw sends a request through the pinned transport with full control over
// the path, headers and body.
func (ts *testServer) raw(t *testing.T, method, rawPath string, body []byte, token string) (int, string) {
	t.Helper()
	c := ts.client(t)
	req, err := http.NewRequest(method, "https://"+ts.addr+"/", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.URL.Opaque = "//" + ts.addr + rawPath // send the path bytes verbatim
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != status {
		t.Fatalf("want HTTP %d, got %v", status, err)
	}
}

func TestRoundtripTwoStores(t *testing.T) {
	ts := startServer(t, nil)
	c := ts.client(t)

	local := &profile.Store{Dir: t.TempDir()}
	if err := local.Save(profile.Profile{Name: "dev", Vars: map[string]string{"A": "1", "B": "secret two"}}); err != nil {
		t.Fatal(err)
	}

	// push
	p, _ := local.Get("dev")
	if err := c.Put(p); err != nil {
		t.Fatal(err)
	}
	if got, _ := ts.store.Get("dev"); !reflect.DeepEqual(got.Vars, p.Vars) {
		t.Fatalf("server store has %v", got.Vars)
	}

	// list
	names, err := c.List()
	if err != nil || !reflect.DeepEqual(names, []string{"dev"}) {
		t.Fatalf("list = %v, %v", names, err)
	}

	// set / unset
	if err := c.SetVar("dev", "C", "3"); err != nil {
		t.Fatal(err)
	}
	if err := c.UnsetVar("dev", "A"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetVar("newprof", "X", ""); err != nil {
		t.Fatal(err)
	}

	// show / pull into a second local store
	got, err := c.Get("dev")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"B": "secret two", "C": "3"}
	if !reflect.DeepEqual(got.Vars, want) {
		t.Fatalf("get = %v, want %v", got.Vars, want)
	}
	other := &profile.Store{Dir: t.TempDir()}
	if err := other.Save(got); err != nil {
		t.Fatal(err)
	}
	if pulled, _ := other.Get("dev"); !reflect.DeepEqual(pulled.Vars, want) {
		t.Fatalf("pulled = %v", pulled.Vars)
	}

	// delete
	if err := c.Delete("dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("dev"); !IsNotFound(err) {
		t.Fatalf("get after delete: %v", err)
	}
	wantStatus(t, c.Delete("dev"), http.StatusNotFound)
	wantStatus(t, c.UnsetVar("dev", "B"), http.StatusNotFound)
	names, _ = c.List()
	if !reflect.DeepEqual(names, []string{"newprof"}) {
		t.Fatalf("list after delete = %v", names)
	}
}

func TestAuthRequired(t *testing.T) {
	ts := startServer(t, func(c *Config) { c.MaxAuthFailures = 100 })

	other, _ := generateToken()
	c, err := NewClient(ts.addr, other, ts.id.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.List()
	wantStatus(t, err, http.StatusUnauthorized)

	for _, tok := range []string{"", "x", ts.id.Token + "0", strings.ToUpper(ts.id.Token)} {
		status, body := ts.raw(t, http.MethodGet, "/v1/profiles", nil, tok)
		if status != http.StatusUnauthorized {
			t.Fatalf("token %q: status %d", tok, status)
		}
		if strings.TrimSpace(body) != `{"error":"unauthorized"}` {
			t.Fatalf("401 body leaks detail: %s", body)
		}
	}
	// unknown paths are also behind auth
	if status, _ := ts.raw(t, http.MethodGet, "/nope", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated unknown path: %d", status)
	}
}

func TestFingerprintPinning(t *testing.T) {
	ts := startServer(t, nil)
	wrong := strings.Repeat("ab", 32)
	c, err := NewClient(ts.addr, ts.id.Token, wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}
	if err := c.SetVar("dev", "A", "1"); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}
	if names, _ := ts.store.List(); len(names) != 0 {
		t.Fatalf("request reached the server: %v", names)
	}

	// A different server identity is rejected even with a valid token.
	ts2 := startServer(t, nil)
	c2, _ := NewClient(ts2.addr, ts2.id.Token, ts.id.Fingerprint)
	if _, err := c2.List(); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("want fingerprint mismatch, got %v", err)
	}

	if _, err := NewClient(ts.addr, ts.id.Token, ""); err == nil {
		t.Fatal("client without fingerprint must be refused")
	}
	if _, err := NewClient(ts.addr, ts.id.Token, "zz"); err == nil {
		t.Fatal("malformed fingerprint must be refused")
	}
	// Colon-separated uppercase form is accepted.
	var parts []string
	for i := 0; i < len(ts.id.Fingerprint); i += 2 {
		parts = append(parts, strings.ToUpper(ts.id.Fingerprint[i:i+2]))
	}
	c3, err := NewClient(ts.addr, ts.id.Token, strings.Join(parts, ":"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c3.List(); err != nil {
		t.Fatal(err)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	ts := startServer(t, nil)
	tok := ts.id.Token
	cases := []struct{ method, path string }{
		{http.MethodGet, "/v1/profiles/..%2F..%2Fsecret"},
		{http.MethodGet, "/v1/profiles/..%5Csecret"},
		{http.MethodGet, "/v1/profiles/.hidden"},
		{http.MethodDelete, "/v1/profiles/..%2Fremote%2Ftoken"},
		{http.MethodPut, "/v1/profiles/dev/vars/..%2Fx"},
		{http.MethodPut, "/v1/profiles/dev/vars/A-B"},
		{http.MethodDelete, "/v1/profiles/a%2Fb/vars/X"},
	}
	for _, tc := range cases {
		status, _ := ts.raw(t, tc.method, tc.path, []byte(`{"value":"x"}`), tok)
		if status != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, want 400", tc.method, tc.path, status)
		}
	}
	// Unencoded dot segments never reach a handler (mux redirects, client does not follow).
	if status, _ := ts.raw(t, http.MethodGet, "/v1/profiles/../remote/token", nil, tok); status >= 200 && status < 300 {
		t.Errorf("dot-segment path returned %d", status)
	}
	// Body with invalid variable names.
	status, _ := ts.raw(t, http.MethodPut, "/v1/profiles/dev", []byte(`{"vars":{"A;rm -rf":"x"}}`), tok)
	if status != http.StatusBadRequest {
		t.Errorf("invalid var in body: %d", status)
	}
	status, _ = ts.raw(t, http.MethodPut, "/v1/profiles/dev", []byte(`{"name":"other","vars":{}}`), tok)
	if status != http.StatusBadRequest {
		t.Errorf("name mismatch: %d", status)
	}
	status, _ = ts.raw(t, http.MethodPut, "/v1/profiles/dev", []byte(`{"vars":{},"extra":1}`), tok)
	if status != http.StatusBadRequest {
		t.Errorf("unknown field: %d", status)
	}
	if names, _ := ts.store.List(); len(names) != 0 {
		t.Fatalf("invalid requests created profiles: %v", names)
	}

	// Client-side validation.
	c := ts.client(t)
	if _, err := c.Get("../x"); err == nil {
		t.Error("client accepted ../x")
	}
	if err := c.SetVar("dev", "A B", "x"); err == nil {
		t.Error("client accepted invalid var")
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	ts := startServer(t, nil)
	big := strings.Repeat("a", MaxBodyBytes+1)
	body, _ := json.Marshal(map[string]any{"vars": map[string]string{"BIG": big}})
	status, _ := ts.raw(t, http.MethodPut, "/v1/profiles/dev", body, ts.id.Token)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", status)
	}
	body, _ = json.Marshal(map[string]string{"value": big})
	status, _ = ts.raw(t, http.MethodPut, "/v1/profiles/dev/vars/BIG", body, ts.id.Token)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", status)
	}
	// Small valid JSON followed by a large tail.
	body = append([]byte(`{"value":"x"}`), bytes.Repeat([]byte(" "), MaxBodyBytes)...)
	status, _ = ts.raw(t, http.MethodPut, "/v1/profiles/dev/vars/X", body, ts.id.Token)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", status)
	}
	if names, _ := ts.store.List(); len(names) != 0 {
		t.Fatalf("oversized requests created profiles: %v", names)
	}
}

func TestReadOnly(t *testing.T) {
	ts := startServer(t, func(c *Config) { c.ReadOnly = true })
	_ = ts.store.Save(profile.Profile{Name: "dev", Vars: map[string]string{"A": "1"}})
	c := ts.client(t)
	if names, err := c.List(); err != nil || len(names) != 1 {
		t.Fatalf("list: %v %v", names, err)
	}
	if _, err := c.Get("dev"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, c.Put(profile.Profile{Name: "x"}), http.StatusForbidden)
	wantStatus(t, c.SetVar("dev", "A", "2"), http.StatusForbidden)
	wantStatus(t, c.UnsetVar("dev", "A"), http.StatusForbidden)
	wantStatus(t, c.Delete("dev"), http.StatusForbidden)
	if p, _ := ts.store.Get("dev"); p.Vars["A"] != "1" {
		t.Fatal("read-only server was mutated")
	}
	// Auth still comes first.
	if status, _ := ts.raw(t, http.MethodDelete, "/v1/profiles/dev", nil, ""); status != 401 {
		t.Fatalf("unauthenticated read-only mutation: %d", status)
	}
}

func TestAuthRateLimit(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	ts := startServer(t, func(c *Config) { c.MaxAuthFailures = 3; c.now = clock })
	bad, _ := generateToken()
	for i := 0; i < 3; i++ {
		if status, _ := ts.raw(t, http.MethodGet, "/v1/profiles", nil, bad); status != 401 {
			t.Fatalf("attempt %d: %d", i, status)
		}
	}
	// Locked out now, even with the right token.
	if status, _ := ts.raw(t, http.MethodGet, "/v1/profiles", nil, ts.id.Token); status != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", status)
	}
	advance(2 * time.Second)
	if status, _ := ts.raw(t, http.MethodGet, "/v1/profiles", nil, ts.id.Token); status != 200 {
		t.Fatalf("after backoff: %d", status)
	}
	// Backoff grows with consecutive failures.
	l := newAuthLimiter(1, clock)
	l.fail("1.2.3.4")
	d1 := l.blocked("1.2.3.4")
	l.fail("1.2.3.4")
	d2 := l.blocked("1.2.3.4")
	if !(d1 > 0 && d2 > d1) {
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
	if err := WriteProfile(&buf, p, false); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	if !a.CreatedCert || !a.CreatedToken || !validToken(a.Token) || len(a.Fingerprint) != 64 {
		t.Fatalf("first identity: %+v", a)
	}
	b, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.CreatedCert || b.CreatedToken || b.Token != a.Token || b.Fingerprint != a.Fingerprint {
		t.Fatal("identity was not reused")
	}
	if runtime.GOOS != "windows" {
		for _, f := range []string{keyFile, tokenFile} {
			st, err := os.Stat(filepath.Join(dir, f))
			if err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode %v %v", f, st.Mode(), err)
			}
		}
	}
	// Half an identity is an error, not a silent regeneration.
	_ = os.Remove(filepath.Join(dir, keyFile))
	if _, err := LoadOrCreateIdentity(dir); err == nil {
		t.Fatal("expected error with missing key")
	}
}

func TestRemotesFile(t *testing.T) {
	dir := t.TempDir()
	rs, err := LoadRemotes(dir)
	if err != nil || len(rs.Names()) != 0 {
		t.Fatalf("empty load: %v %v", rs, err)
	}
	tok, _ := generateToken()
	fp := strings.ToUpper(strings.Repeat("ab", 32))
	if err := rs.Add(Remote{Name: "b", Address: "10.0.0.2:7777", Token: tok, Fingerprint: fp}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Remote{
		{Name: "../x", Address: "h:1", Token: tok, Fingerprint: fp},
		{Name: "c", Address: "nohost", Token: tok, Fingerprint: fp},
		{Name: "c", Address: "h:99999", Token: tok, Fingerprint: fp},
		{Name: "c", Address: "h:1", Token: "short", Fingerprint: fp},
		{Name: "c", Address: "h:1", Token: tok, Fingerprint: "abc"},
	} {
		if err := rs.Add(bad); err == nil {
			t.Errorf("accepted invalid remote %+v", bad)
		}
	}
	if err := rs.Save(); err != nil {
		t.Fatal(err)
	}
	rs2, err := LoadRemotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := rs2.Get("b")
	if err != nil || r.Token != tok || r.Fingerprint != strings.Repeat("ab", 32) || r.Address != "10.0.0.2:7777" {
		t.Fatalf("reloaded %+v %v", r, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(dir, remotesFile))
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("remotes.json mode %v", st.Mode())
		}
	}
	if err := rs2.Remove("b"); err != nil {
		t.Fatal(err)
	}
	if err := rs2.Remove("b"); !errors.Is(err, ErrRemoteNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
}
