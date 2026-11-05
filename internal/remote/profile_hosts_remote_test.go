package remote

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// runLogged starts the child loop with its own hosts file and a log sink.
func (c *child) runLogged(t *testing.T, e *hubEnv, hostsFile string) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	a, err := NewAgent(c.binding, c.store, log.New(buf, "", 0))
	if err != nil {
		t.Fatal(err)
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
	return buf
}

func TestProfileHostsSurvivePushPull(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.invite(t, "kid", RoleReadWrite), "") // no hosts grant: a profile edit needs none
	c.run(t, e)
	hs := []profile.HostEntry{{IP: "10.0.0.5", Names: []string{"api.dev.local"}, Comment: "cluster"}}
	relayOK(t, e, "kid", Job{Op: OpPut, Args: JobArgs{Profile: "dev", Vars: map[string]string{"A": "1"}, Hosts: hs}})
	p, err := c.store.Get("dev")
	if err != nil || !reflect.DeepEqual(p.Hosts, hs) || p.Vars["A"] != "1" {
		t.Fatalf("child store %+v %v", p, err)
	}
	res := relayOK(t, e, "kid", Job{Op: OpGet, Args: JobArgs{Profile: "dev"}})
	if res.Profile == nil || !reflect.DeepEqual(res.Profile.Hosts, hs) {
		t.Fatalf("get %+v", res.Profile)
	}
	// Invalid hosts data in a put is refused at the hub (same validation).
	if _, err := e.admin.Relay("kid", Job{Op: OpPut, Args: JobArgs{Profile: "dev", Hosts: []profile.HostEntry{{IP: "10.0.0.1", Names: []string{"a\n6.6.6.6 bank"}}}}}); err == nil {
		t.Fatal("invalid hosts accepted")
	}
	// An old child ignores the field: decoding a job without it keeps no hosts.
	var j Job
	if err := json.Unmarshal([]byte(`{"op":"put","args":{"profile":"old","vars":{}}}`), &j); err != nil || j.Args.Hosts != nil {
		t.Fatalf("%+v %v", j, err)
	}
}

func TestApplyProfileRemote(t *testing.T) {
	e := startHub(t, nil)
	c := joinChild(t, e.inviteCaps(t, "kid", RoleReadWrite, CapHosts), "")
	hf := tempHosts(t)
	logs := c.runLogged(t, e, hf)

	// The child's own profile has 10.0.0.5; the parent's idea of it is irrelevant.
	if err := c.store.SetHost("dev", "10.0.0.5", "api.dev.local"); err != nil {
		t.Fatal(err)
	}
	res := relayOK(t, e, "kid", Job{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev", DryRun: true}})
	if !res.Hosts.Changed || !strings.Contains(res.Hosts.Diff, "+10.0.0.5 api.dev.local") || readFile(t, hf) != hostsFixture {
		t.Fatalf("dry run %+v", res.Hosts)
	}
	res = relayOK(t, e, "kid", Job{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev"}})
	got := readFile(t, hf)
	if !res.Hosts.Changed || res.Hosts.Backup == "" || !strings.HasPrefix(got, hostsFixture+"\r\n") || !strings.Contains(got, "# group: profile:dev\r\n10.0.0.5 api.dev.local\r\n") {
		t.Fatalf("apply %+v %q", res.Hosts, got)
	}
	if st, _ := hostsapply.ReadState(c.store.Dir); st.Profile != "dev" {
		t.Fatalf("child state %+v", st)
	}
	// Audit line on the child (parent label, op, profile, ok).
	waitFor(t, func() bool {
		l := logs.String()
		return strings.Contains(l, `parent="parent-test" op=hosts.apply-profile profile="dev"`) && strings.Contains(l, "dryRun=false ok=true")
	})

	// Unknown profile on the child.
	if res, err := e.admin.Relay("kid", Job{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "nope"}}); err != nil || res.OK || !res.NotFound {
		t.Fatalf("unknown profile %+v %v", res, err)
	}
	// Network-supplied hosts data is refused at the hub and at the child.
	evil := []Job{
		{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev", Hosts: []profile.HostEntry{{IP: "6.6.6.6", Names: []string{"bank.example"}}}}},
		{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev", IP: "6.6.6.6", Hostnames: []string{"bank.example"}}},
		{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev", Group: "default"}},
		{Op: OpHostsUnapply, Args: JobArgs{Profile: "dev"}},
		{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "../x"}},
	}
	for _, j := range evil {
		if _, err := e.admin.Relay("kid", j); err == nil {
			t.Fatalf("hub accepted %+v", j)
		}
		if res := ExecuteJob(Target{Store: c.store, Role: RoleReadWrite, Caps: []string{CapHosts}, HostsFile: hf}, j); res.OK {
			t.Fatalf("child accepted %+v", j)
		}
	}
	if strings.Contains(readFile(t, hf), "6.6.6.6") {
		t.Fatal("network hosts data was written")
	}
	// A job decoded with extra unknown fields still only uses the child's profile.
	var j Job
	raw := `{"op":"hosts.apply-profile","args":{"profile":"dev","entries":[{"ip":"6.6.6.6","names":["bank.example"]}],"path":"/etc/evil"}}`
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		t.Fatal(err)
	}
	if res := ExecuteJob(Target{Store: c.store, Role: RoleReadWrite, Caps: []string{CapHosts}, HostsFile: hf}, j); !res.OK || strings.Contains(readFile(t, hf), "6.6.6.6") {
		t.Fatalf("%+v", res)
	}

	// Unapply.
	res = relayOK(t, e, "kid", Job{Op: OpHostsUnapply})
	if !res.Hosts.Changed || !reflect.DeepEqual(res.Hosts.Applied, []string{"dev"}) || strings.Contains(readFile(t, hf), "profile:dev") {
		t.Fatalf("unapply %+v", res.Hosts)
	}
}

func TestApplyProfileRefused(t *testing.T) {
	e := startHub(t, nil)
	// Without the hosts grant.
	c := joinChild(t, e.invite(t, "kid", RoleReadWrite), "")
	hf := tempHosts(t)
	c.runHosts(t, e, hf)
	if err := c.store.SetHost("dev", "10.0.0.5", "api.dev.local"); err != nil {
		t.Fatal(err)
	}
	for _, j := range []Job{{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev"}}, {Op: OpHostsUnapply}} {
		if _, err := e.admin.Relay("kid", j); err == nil || !strings.Contains(err.Error(), "--allow-hosts") {
			t.Fatalf("%s without grant: %v", j.Op, err)
		}
		if res := ExecuteJob(Target{Store: c.store, Role: RoleReadWrite, HostsFile: hf}, j); res.OK || res.Code != CodeNotGranted {
			t.Fatalf("child without grant: %+v", res)
		}
	}
	// Read-only child with the grant.
	ro := joinChild(t, e.inviteCaps(t, "kiosk", RoleReadOnly, CapHosts), "")
	hf2 := tempHosts(t)
	ro.runHosts(t, e, hf2)
	if err := ro.store.SetHost("dev", "10.0.0.5", "api.dev.local"); err != nil {
		t.Fatal(err)
	}
	for _, j := range []Job{{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev"}}, {Op: OpHostsApplyProfile, Args: JobArgs{Profile: "dev", DryRun: true}}, {Op: OpHostsUnapply}} {
		if _, err := e.admin.Relay("kiosk", j); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("ro %s: %v", j.Op, err)
		}
		if res := ExecuteJob(Target{Store: ro.store, Role: RoleReadOnly, Caps: []string{CapHosts}, HostsFile: hf2}, j); res.OK || !strings.Contains(res.Error, "read-only") {
			t.Fatalf("child ro: %+v", res)
		}
	}
	if readFile(t, hf) != hostsFixture || readFile(t, hf2) != hostsFixture {
		t.Fatal("hosts file changed")
	}
	// Critical names in the child's own profile are refused remotely.
	if err := profileWithCritical(ro.store); err != nil {
		t.Fatal(err)
	}
	if res := ExecuteJob(Target{Store: ro.store, Role: RoleReadWrite, Caps: []string{CapHosts}, HostsFile: hf2}, Job{Op: OpHostsApplyProfile, Args: JobArgs{Profile: "evil"}}); res.OK || !strings.Contains(res.Error, "critical") {
		t.Fatalf("critical %+v", res)
	}
}

// profileWithCritical writes a hand-edited profile mapping localhost.
func profileWithCritical(st *profile.Store) error {
	return st.Save(profile.Profile{Name: "evil", Hosts: []profile.HostEntry{{IP: "10.6.6.6", Names: []string{"localhost"}}}})
}
