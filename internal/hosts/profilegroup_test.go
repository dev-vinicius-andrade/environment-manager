package hosts

import (
	"strings"
	"testing"
)

func TestProfileGroups(t *testing.T) {
	raw := "127.0.0.1 localhost\n" + BeginMarker + "\n# group: dev\n10.0.0.1 a.local\n# group: profile:p1 (disabled)\n# 10.0.0.2 b.local\n" + EndMarker + "\n"
	d, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.AppliedProfiles(); len(got) != 1 || got[0] != "p1" {
		t.Fatalf("applied %v", got)
	}
	if !ValidGroup("profile:p1") || ValidGroup("profile:") || ValidGroup("profile:../x") || ValidGroup("profile:a:b") {
		t.Fatal("ValidGroup")
	}
	if g, ok := ProfileOfGroup(ProfileGroup("dev")); !ok || g != "dev" {
		t.Fatal("ProfileOfGroup")
	}
	// User edits cannot target a profile group.
	if _, err := CheckGroup("profile:p1"); err == nil || !strings.Contains(err.Error(), "hosts apply") {
		t.Fatalf("CheckGroup: %v", err)
	}
	if _, err := d.Add("10.0.0.9", []string{"x.local"}, "profile:p1", false); err == nil {
		t.Fatal("add into a profile group")
	}
	if _, err := d.Remove("b.local", "profile:p1"); err == nil {
		t.Fatal("remove from a profile group")
	}
	// Remove without a group skips profile groups.
	if _, err := d.Remove("b.local", ""); err == nil {
		t.Fatal("remove reached a profile group")
	}
	// Enable/disable works on a profile group.
	if ch, err := d.SetGroupEnabled("profile:p1", true); err != nil || !ch {
		t.Fatalf("enable %v %v", ch, err)
	}
	// Replacing re-enables and swaps the group in place.
	ch, err := d.ApplyProfile("p2", []Entry{{IP: "10.0.0.3", Hostnames: []string{"C.local"}}})
	if err != nil || !ch {
		t.Fatalf("apply %v %v", ch, err)
	}
	if len(d.Groups) != 2 || d.Groups[1].Name != "profile:p2" || d.Groups[1].Entries[0].Hostnames[0] != "c.local" {
		t.Fatalf("groups %+v", d.Groups)
	}
	for _, bad := range [][]Entry{
		{{IP: "10.0.0.3", Hostnames: []string{"localhost"}}},
		{{IP: "10.0.0.3", Hostnames: []string{"a\nb"}}},
		{{IP: "10.0.0.3", Hostnames: []string{"a.local"}}, {IP: "10.0.0.4", Hostnames: []string{"a.local"}}},
		{{IP: "10.0.0.3", Hostnames: []string{"a.local"}}, {IP: "10.0.0.3", Hostnames: []string{"b.local"}}},
	} {
		if _, err := d.ApplyProfile("p3", bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if _, err := d.ApplyProfile("../x", nil); err == nil {
		t.Fatal("bad profile name")
	}
	if !d.RemoveProfileGroups() || len(d.Groups) != 1 || d.RemoveProfileGroups() {
		t.Fatalf("remove %+v", d.Groups)
	}
	out := string(d.Render())
	if !strings.HasPrefix(out, "127.0.0.1 localhost\n"+BeginMarker+"\n") || strings.Contains(out, "profile:") {
		t.Fatalf("%q", out)
	}
}
