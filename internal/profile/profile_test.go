package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Dir: t.TempDir()}
}

func writeRaw(t *testing.T, s *Store, name, content string) {
	t.Helper()
	dir := filepath.Join(s.Dir, "profiles")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOldProfileFileLoadsUnchanged(t *testing.T) {
	s := newStore(t)
	old := "{\n  \"name\": \"dev\",\n  \"vars\": {\n    \"MY_URL\": \"http://x\"\n  }\n}"
	writeRaw(t, s, "dev", old)
	p, err := s.Get("dev")
	if err != nil {
		t.Fatal(err)
	}
	if p.Vars["MY_URL"] != "http://x" || p.Hosts != nil {
		t.Fatalf("%+v", p)
	}
	// Saving a profile without hosts writes the same layout (no "hosts" key).
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir, "profiles", "dev.json"))
	if string(b) != old {
		t.Fatalf("re-saved old profile changed:\n%s", b)
	}
}

func TestRoundTripWithHosts(t *testing.T) {
	s := newStore(t)
	in := Profile{Name: "dev", Vars: map[string]string{"A": "1"}, Hosts: []HostEntry{
		{IP: "10.0.0.5", Names: []string{"API.dev.local", "db.dev.local", "api.dev.local"}, Comment: "dev cluster"},
		{IP: "2001:DB8::1", Names: []string{"v6.dev.local"}},
	}}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("dev")
	if err != nil {
		t.Fatal(err)
	}
	want := []HostEntry{
		{IP: "10.0.0.5", Names: []string{"api.dev.local", "db.dev.local"}, Comment: "dev cluster"},
		{IP: "2001:db8::1", Names: []string{"v6.dev.local"}},
	}
	if !reflect.DeepEqual(got.Hosts, want) || got.Vars["A"] != "1" {
		t.Fatalf("got %+v", got)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(s.Dir, "profiles", "dev.json"))
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode())
		}
	}
	// Hosts only, no vars: valid.
	if err := s.Save(Profile{Name: "net", Hosts: []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}}}}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Get("net"); err != nil || len(p.Vars) != 0 || len(p.Hosts) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	// Neither vars nor hosts: valid.
	if err := s.Save(Profile{Name: "empty"}); err != nil {
		t.Fatal(err)
	}
}

func TestHostsValidationTable(t *testing.T) {
	many := []string{}
	for i := 0; i < 33; i++ {
		many = append(many, fmt.Sprintf("h%d.local", i))
	}
	cases := []struct {
		name string
		e    []HostEntry
	}{
		{"newline in name", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local\n6.6.6.6 bank.example"}}}},
		{"hash in name", []HostEntry{{IP: "10.0.0.1", Names: []string{"a#b"}}}},
		{"space in name", []HostEntry{{IP: "10.0.0.1", Names: []string{"a b"}}}},
		{"no names", []HostEntry{{IP: "10.0.0.1"}}},
		{"too many names", []HostEntry{{IP: "10.0.0.1", Names: many}}},
		{"bad ip", []HostEntry{{IP: "10.0.0.256", Names: []string{"a.local"}}}},
		{"ip with newline", []HostEntry{{IP: "10.0.0.1\n6.6.6.6", Names: []string{"a.local"}}}},
		{"zone id", []HostEntry{{IP: "fe80::1%eth0", Names: []string{"a.local"}}}},
		{"leading zeros", []HostEntry{{IP: "010.0.0.1", Names: []string{"a.local"}}}},
		{"comment newline", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}, Comment: "x\n10.6.6.6 evil"}}},
		{"comment hash", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}, Comment: "x # y"}}},
		{"comment escape", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}, Comment: "x\x1b[31m"}}},
		{"comment bidi", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}, Comment: "x‮y"}}},
		{"comment too long", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}, Comment: strings.Repeat("x", 129)}}},
		{"duplicate ip", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}}, {IP: "10.0.0.1", Names: []string{"b.local"}}}},
		{"duplicate name", []HostEntry{{IP: "10.0.0.1", Names: []string{"a.local"}}, {IP: "10.0.0.2", Names: []string{"A.local"}}}},
	}
	for _, c := range cases {
		p := Profile{Name: "x", Hosts: c.e}
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
		if err := newStore(t).Save(p); err == nil {
			t.Errorf("%s: saved", c.name)
		}
	}
	tooMany := make([]HostEntry, MaxHostEntries+1)
	for i := range tooMany {
		tooMany[i] = HostEntry{IP: fmt.Sprintf("10.0.%d.%d", i/250, i%250+1), Names: []string{fmt.Sprintf("h%d.local", i)}}
	}
	if err := (Profile{Name: "x", Hosts: tooMany}).Validate(); err == nil {
		t.Error("too many entries accepted")
	}
	if err := (Profile{Name: "x", Hosts: tooMany[:MaxHostEntries]}).Validate(); err != nil {
		t.Errorf("max entries refused: %v", err)
	}
	// A hand-edited file with an invalid entry does not load.
	s := newStore(t)
	writeRaw(t, s, "bad", `{"name":"bad","vars":{},"hosts":[{"ip":"10.0.0.1","names":["a.local\n6.6.6.6 bank"]}]}`)
	if _, err := s.Get("bad"); err == nil {
		t.Fatal("invalid hosts loaded")
	}
}

func TestStoreHostHelpers(t *testing.T) {
	s := newStore(t)
	if err := s.SetHost("dev", "10.0.0.5", "api.dev.local", "db.dev.local"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHostEntry("dev", HostEntry{IP: "10.0.0.6", Names: []string{"db.dev.local"}, Comment: "moved"}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get("dev")
	want := []HostEntry{{IP: "10.0.0.5", Names: []string{"api.dev.local"}}, {IP: "10.0.0.6", Names: []string{"db.dev.local"}, Comment: "moved"}}
	if !reflect.DeepEqual(p.Hosts, want) {
		t.Fatalf("after move %+v", p.Hosts)
	}
	if err := s.SetHost("dev", "10.0.0.5", "web.dev.local"); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.Get("dev"); !reflect.DeepEqual(p.Hosts[0].Names, []string{"api.dev.local", "web.dev.local"}) {
		t.Fatalf("merge %+v", p.Hosts)
	}
	if err := s.SetHost("dev", "10.0.0.5", "localhost"); err == nil || !strings.Contains(err.Error(), "critical") {
		t.Fatalf("critical: %v", err)
	}
	if err := s.SetHost("dev", "10.0.0.5", "a\nb"); err == nil {
		t.Fatal("bad name accepted")
	}
	if err := s.UnsetHost("dev", "db.dev.local"); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.Get("dev"); len(p.Hosts) != 1 {
		t.Fatalf("empty entry not dropped %+v", p.Hosts)
	}
	if err := s.UnsetHost("dev", "nope.local"); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.UnsetHost("nope", "a.local"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing profile: %v", err)
	}
	// Vars helpers still work next to hosts.
	if err := s.SetVar("dev", "A", "1"); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.Get("dev"); p.Vars["A"] != "1" || len(p.Hosts) != 1 {
		t.Fatalf("%+v", p)
	}
	if err := p.RemoveHostIP("10.0.0.5"); err != nil || len(p.Hosts) != 0 {
		t.Fatalf("remove ip %v %+v", err, p.Hosts)
	}
}
