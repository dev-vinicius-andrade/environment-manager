package tray

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func visible(m Model) []string {
	out := []string{}
	for _, s := range m.Slots {
		if s.Visible {
			mark := ""
			if s.Checked {
				mark = "*"
			}
			out = append(out, mark+s.Profile)
		}
	}
	return out
}

func TestBuildCheckedItem(t *testing.T) {
	m := Build(State{Profiles: []string{"prod", "dev", "local"}, Selected: "dev"})
	if got := visible(m); !reflect.DeepEqual(got, []string{"*dev", "local", "prod"}) {
		t.Fatalf("slots = %v", got)
	}
	if m.NoneChecked || !m.HasSelection || m.Tooltip != "envmgr: dev" || m.More != "" || m.Empty {
		t.Fatalf("model = %+v", m)
	}
	n := Build(State{Profiles: []string{"dev"}})
	if !n.NoneChecked || n.HasSelection || n.Tooltip != "envmgr: none" || visible(n)[0] != "dev" {
		t.Fatalf("none: %+v", n)
	}
}

func TestBuildEmptyAndMissing(t *testing.T) {
	e := Build(State{})
	if !e.Empty || len(visible(e)) != 0 || !e.NoneChecked {
		t.Fatalf("empty: %+v", e)
	}
	m := Build(State{Profiles: []string{"a"}, Selected: "gone"})
	if m.HasSelection || m.NoneChecked || m.Tooltip != "envmgr: gone (missing)" || visible(m)[0] != "a" {
		t.Fatalf("missing: %+v", m)
	}
}

func TestBuildCap(t *testing.T) {
	names := []string{}
	for i := 0; i < 100; i++ {
		names = append(names, fmt.Sprintf("p%03d", i))
	}
	m := Build(State{Profiles: names, Selected: "p005"})
	v := visible(m)
	if len(v) != MaxProfiles || v[0] != "p000" || v[5] != "*p005" || m.More != "60 more: use the TUI" {
		t.Fatalf("cap: %v more=%q", v, m.More)
	}
	// A selection beyond the cap takes the last slot so its check mark shows.
	m = Build(State{Profiles: names, Selected: "p099"})
	v = visible(m)
	if len(v) != MaxProfiles || v[MaxProfiles-1] != "*p099" || v[MaxProfiles-2] != "p038" {
		t.Fatalf("selection beyond cap: %v", v)
	}
	// Exactly the cap: no "more" item.
	if m := Build(State{Profiles: names[:MaxProfiles]}); m.More != "" || len(visible(m)) != MaxProfiles {
		t.Fatalf("exact cap: more=%q", m.More)
	}
}

func TestDiff(t *testing.T) {
	st := State{Profiles: []string{"a", "b", "c"}, Selected: "a"}
	m1 := Build(st)
	if d := Diff(m1, Build(st)); len(d) != 0 {
		t.Fatalf("no change produced %v", d)
	}
	// Selection a -> b: two slots, tooltip; icon and none unchanged.
	st.Selected = "b"
	d := Diff(m1, Build(st))
	want := []Change{{0, "slot"}, {1, "slot"}, {-1, "tooltip"}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("switch: %v want %v", d, want)
	}
	// Cleared: slot b, none, tooltip, icon.
	m2 := Build(st)
	st.Selected = ""
	d = Diff(m2, Build(st))
	want = []Change{{1, "slot"}, {-1, "none"}, {-1, "tooltip"}, {-1, "icon"}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("clear: %v want %v", d, want)
	}
	// A profile added in the middle shifts the following slots.
	m3 := Build(st)
	st.Profiles = append(st.Profiles, "ab")
	d = Diff(m3, Build(st))
	want = []Change{{1, "slot"}, {2, "slot"}, {3, "slot"}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("add: %v want %v", d, want)
	}
	// Autostart toggled elsewhere.
	m4 := Build(st)
	st.Autostart = true
	if d := Diff(m4, Build(st)); !reflect.DeepEqual(d, []Change{{-1, "autostart"}}) {
		t.Fatalf("autostart: %v", d)
	}
	if n := len(AllChanges()); n != MaxProfiles+6 {
		t.Fatalf("AllChanges = %d", n)
	}
}

func TestReact(t *testing.T) {
	m := Build(State{Profiles: []string{"a", "b"}, Selected: "a", SelectedEnv: true})
	cases := []struct {
		t    Target
		want Action
	}{
		{Target{Kind: TargetEnv, Slot: 1}, Action{Kind: ActSelect, Profile: "b", Env: true}},
		{Target{Kind: TargetHosts, Slot: 1}, Action{Kind: ActSelect, Profile: "b", Hosts: true}},
		{Target{Kind: TargetHosts, Slot: 0}, Action{Kind: ActSelect, Profile: "a", Env: true, Hosts: true}},
		{Target{Kind: TargetEnv, Slot: 0}, Action{Kind: ActClear}}, // last scope off
		{Target{Kind: TargetApplyHosts, Slot: 1}, Action{Kind: ActApplyHosts, Profile: "b"}},
		{Target{Kind: TargetEnv, Slot: 5}, Action{}}, // hidden slot
		{Target{Kind: TargetHosts, Slot: -1}, Action{}},
		{Target{Kind: TargetApplyHosts, Slot: MaxProfiles}, Action{}},
		{Target{Kind: TargetNone}, Action{Kind: ActClear}},
		{Target{Kind: TargetOpenTUI}, Action{Kind: ActOpenTUI}},
		{Target{Kind: TargetOpenFolder}, Action{Kind: ActOpenFolder}},
		{Target{Kind: TargetAutostart}, Action{Kind: ActSetAutostart, Enable: true}},
		{Target{Kind: TargetQuit}, Action{Kind: ActQuit}},
	}
	for _, c := range cases {
		if got := React(m, c.t); got != c.want {
			t.Errorf("React(%+v) = %+v, want %+v", c.t, got, c.want)
		}
	}
	none := Build(State{Profiles: []string{"a"}, Autostart: true})
	if got := React(none, Target{Kind: TargetNone}); got != (Action{}) {
		t.Errorf("None when nothing selected: %+v", got)
	}
	if got := React(none, Target{Kind: TargetAutostart}); got != (Action{Kind: ActSetAutostart, Enable: false}) {
		t.Errorf("autostart off: %+v", got)
	}
}

func TestScopes(t *testing.T) {
	st := State{Profiles: []string{"a", "b"}, Selected: "a", SelectedEnv: true}
	m := Build(st)
	if s := m.Slots[0]; !s.Checked || !s.Env || s.Hosts || m.Tooltip != "envmgr: a" {
		t.Fatalf("env only: %+v %q", s, m.Tooltip)
	}
	if s := m.Slots[1]; s.Checked || s.Env || s.Hosts {
		t.Fatalf("other slot: %+v", s)
	}
	st.SelectedHosts = true
	m2 := Build(st)
	if s := m2.Slots[0]; !s.Env || !s.Hosts || m2.Tooltip != "envmgr: a (env + hosts)" {
		t.Fatalf("both: %+v %q", s, m2.Tooltip)
	}
	if d := Diff(m, m2); !reflect.DeepEqual(d, []Change{{0, "slot"}, {-1, "tooltip"}}) {
		t.Fatalf("diff %v", d)
	}
	st.SelectedEnv, st.HostsApplied = false, "b"
	m3 := Build(st)
	if s := m3.Slots[0]; s.Env || !s.Hosts || !m3.HasSelection || m3.Tooltip != "envmgr: a (hosts only); hosts applied: b" {
		t.Fatalf("hosts only: %+v %q", s, m3.Tooltip)
	}
	// Hosts toggled off from hosts-only clears the selection; React never
	// writes hosts by itself (only ActApplyHosts does, elevated).
	if got := React(m3, Target{Kind: TargetHosts, Slot: 0}); got != (Action{Kind: ActClear}) {
		t.Fatalf("hosts off: %+v", got)
	}
	if !strings.Contains(m3.Summary(), "hosts=true") {
		t.Fatalf("summary %q", m3.Summary())
	}
}

func TestElevatedApplyArgs(t *testing.T) {
	got := ElevatedApplyArgs(`C:\Users\Jo Doe\AppData\Roaming\envmgr`, "", "dev")
	want := []string{"hosts", "apply", "dev", "--home", `C:\Users\Jo Doe\AppData\Roaming\envmgr`, "--pause"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	got = ElevatedApplyArgs(`C:\h`, `C:\tmp\hosts`, "dev")
	if !reflect.DeepEqual(got, []string{"hosts", "apply", "dev", "--home", `C:\h`, "--file", `C:\tmp\hosts`, "--pause"}) {
		t.Fatalf("%q", got)
	}
	cl := WindowsCommandLine([]string{"hosts", "apply", "dev", "--home", `C:\Users\Jo Doe\x\`, "--file", `a"b`, "", `C:\plain\path`})
	if cl != `hosts apply dev --home "C:\Users\Jo Doe\x\\" --file "a\"b" "" C:\plain\path` {
		t.Fatalf("command line %s", cl)
	}
}

func TestIcon(t *testing.T) {
	off, on := Icon(false), Icon(true)
	if bytes.Equal(off, on) {
		t.Fatal("both icons are identical")
	}
	for _, ico := range [][]byte{off, on} {
		le := binary.LittleEndian
		if le.Uint16(ico[0:]) != 0 || le.Uint16(ico[2:]) != 1 || le.Uint16(ico[4:]) != 2 {
			t.Fatalf("bad ICONDIR % x", ico[:6])
		}
		for i, size := range []int{16, 32} {
			e := ico[6+16*i:]
			n, off := int(le.Uint32(e[8:])), int(le.Uint32(e[12:]))
			if int(e[0]) != size || int(e[1]) != size || le.Uint16(e[6:]) != 32 || off+n > len(ico) {
				t.Fatalf("entry %d: % x", i, e[:16])
			}
			want := 40 + size*size*4 + ((size+31)/32)*4*size
			if n != want || le.Uint32(ico[off:]) != 40 || int(le.Uint32(ico[off+8:])) != 2*size {
				t.Fatalf("image %d: size %d want %d", size, n, want)
			}
		}
	}
}

func TestHelpers(t *testing.T) {
	a, b := MutexName(`C:\Users\X\envmgr`), MutexName(`c:\users\x\ENVMGR\`)
	if a != b || !strings.HasPrefix(a, `Local\envmgr-tray-`) || a == MutexName(`C:\other`) {
		t.Fatalf("mutex names: %q %q", a, b)
	}
	if got := AutostartCommand(`C:\Program Files\envmgr\envmgr.exe`); got != `"C:\Program Files\envmgr\envmgr.exe" tray` {
		t.Fatalf("autostart: %q", got)
	}
	if got := PowerShellTUICommand(`C:\O'Neil\envmgr.exe`); got != `& 'C:\O''Neil\envmgr.exe' tui` {
		t.Fatalf("ps: %q", got)
	}
	if got := WTArgs(`C:\a;b\envmgr.exe`); !reflect.DeepEqual(got, []string{"new-tab", "--title", "envmgr", `C:\a\;b\envmgr.exe`, "tui"}) {
		t.Fatalf("wt: %q", got)
	}
}
