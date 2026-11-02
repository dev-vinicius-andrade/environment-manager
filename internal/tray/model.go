// Package tray holds the Windows system tray app (app_windows.go) and its
// platform-independent parts: the menu model, the reaction to clicks and the
// icon bytes. The model is pure so it can be tested without a tray.
//
// The tray only writes the machine-wide selection (internal/selected); shells
// pick it up through the hook. It never touches other processes' environments
// or the registry's environment keys, and needs no admin rights. It never
// writes the hosts file either: "Apply hosts (asks for administrator)..."
// starts `envmgr hosts apply <profile>` through an explicit UAC prompt.
package tray

import (
	"fmt"
	"sort"
)

// MaxProfiles is how many profiles the menu lists; the rest are reachable
// from the TUI ("N more: use the TUI").
const MaxProfiles = 40

// State is what the tray reads on every poll.
type State struct {
	Profiles      []string // profile names (any order)
	Selected      string   // selected profile, "" = none
	SelectedEnv   bool     // the selection includes the environment scope
	SelectedHosts bool     // the selection includes the hosts scope
	HostsApplied  string   // profile whose hosts are applied (hosts-applied.json), "" = none
	Autostart     bool     // "Start with Windows" is enabled
}

// Slot is one profile entry in the menu (a fixed pool of MaxProfiles items
// that are shown, hidden and retitled as the profile list changes). Each
// profile item has a submenu: "Environment" and "Hosts" check items (the
// selected scopes) and "Apply hosts (asks for administrator)...".
type Slot struct {
	Visible bool
	Profile string
	Checked bool // the profile is selected (any scope)
	Env     bool // "Environment" is checked
	Hosts   bool // "Hosts" is checked
}

// Model is the full menu state.
type Model struct {
	Slots        [MaxProfiles]Slot
	More         string // title of the disabled "more" item, "" = hidden
	Empty        bool   // no profiles at all: show the "no profiles" item
	NoneChecked  bool   // "None (deactivate)" is checked
	Autostart    bool   // "Start with Windows" is checked
	Tooltip      string // icon tooltip
	HasSelection bool   // icon look: true = a profile is selected
}

// Fixed menu item titles.
const (
	TitleNone       = "None (deactivate)"
	TitleEmpty      = "No profiles (create one with: envmgr set)"
	TitleEnv        = "Environment"
	TitleHosts      = "Hosts"
	TitleApplyHosts = "Apply hosts (asks for administrator)..."
	TitleOpenTUI    = "Open TUI"
	TitleOpenFolder = "Open profiles folder"
	TitleAutostart  = "Start with Windows"
	TitleQuit       = "Quit"
)

// Build computes the menu model for a state. Profiles are sorted; the
// selected profile is always listed (in place of the last slot when it would
// fall beyond the cap) so its check mark is visible.
func Build(st State) Model {
	names := append([]string(nil), st.Profiles...)
	sort.Strings(names)
	exists := false
	for _, n := range names {
		if n == st.Selected {
			exists = true
			break
		}
	}
	shown := names
	hidden := 0
	if len(names) > MaxProfiles {
		shown = append([]string(nil), names[:MaxProfiles]...)
		hidden = len(names) - MaxProfiles
		if exists {
			in := false
			for _, n := range shown {
				if n == st.Selected {
					in = true
					break
				}
			}
			if !in {
				shown[MaxProfiles-1] = st.Selected
			}
		}
	}
	m := Model{Autostart: st.Autostart, Empty: len(names) == 0}
	for i, n := range shown {
		sel := st.Selected != "" && n == st.Selected
		m.Slots[i] = Slot{Visible: true, Profile: n, Checked: sel, Env: sel && st.SelectedEnv, Hosts: sel && st.SelectedHosts}
	}
	if hidden > 0 {
		m.More = fmt.Sprintf("%d more: use the TUI", hidden)
	}
	switch {
	case st.Selected == "":
		m.NoneChecked = true
		m.Tooltip = "envmgr: none"
	case !exists:
		m.Tooltip = "envmgr: " + st.Selected + " (missing)"
	default:
		m.HasSelection = true
		m.Tooltip = "envmgr: " + st.Selected
		switch {
		case st.SelectedEnv && st.SelectedHosts:
			m.Tooltip += " (env + hosts)"
		case st.SelectedHosts && !st.SelectedEnv:
			m.Tooltip += " (hosts only)"
		}
	}
	if st.HostsApplied != "" {
		m.Tooltip += "; hosts applied: " + st.HostsApplied
	}
	return m
}

// Change names one element of the menu that differs between two models.
type Change struct {
	Slot int    // slot index, or -1 for a non-slot element
	What string // "slot", "more", "empty", "none", "autostart", "tooltip", "icon"
}

// Diff lists what must be updated to go from prev to next (nothing when the
// models are equal), so a poll that finds no change touches no menu item.
func Diff(prev, next Model) []Change {
	var out []Change
	for i := range next.Slots {
		if prev.Slots[i] != next.Slots[i] {
			out = append(out, Change{Slot: i, What: "slot"})
		}
	}
	add := func(cond bool, what string) {
		if cond {
			out = append(out, Change{Slot: -1, What: what})
		}
	}
	add(prev.More != next.More, "more")
	add(prev.Empty != next.Empty, "empty")
	add(prev.NoneChecked != next.NoneChecked, "none")
	add(prev.Autostart != next.Autostart, "autostart")
	add(prev.Tooltip != next.Tooltip, "tooltip")
	add(prev.HasSelection != next.HasSelection, "icon")
	return out
}

// AllChanges lists every element (used to push the first model in full).
func AllChanges() []Change {
	out := make([]Change, 0, MaxProfiles+6)
	for i := 0; i < MaxProfiles; i++ {
		out = append(out, Change{Slot: i, What: "slot"})
	}
	for _, w := range []string{"more", "empty", "none", "autostart", "tooltip", "icon"} {
		out = append(out, Change{Slot: -1, What: w})
	}
	return out
}

// TargetKind identifies a clicked menu item.
type TargetKind int

const (
	TargetEnv        TargetKind = iota // "Environment" in a profile's submenu
	TargetHosts                        // "Hosts" in a profile's submenu
	TargetApplyHosts                   // "Apply hosts (asks for administrator)..."
	TargetNone
	TargetOpenTUI
	TargetOpenFolder
	TargetAutostart
	TargetQuit
)

// Target is a clicked item (Slot is the profile slot of the submenu targets).
type Target struct {
	Kind TargetKind
	Slot int
}

// ActionKind is what the app must do after a click.
type ActionKind int

const (
	ActNothing    ActionKind = iota
	ActSelect                // selected.SetScoped(Profile, {Env, Hosts})
	ActClear                 // selected.Clear()
	ActApplyHosts            // run `envmgr hosts apply Profile` elevated (explicit UAC prompt)
	ActOpenTUI
	ActOpenFolder
	ActSetAutostart // enable when Enable, else disable
	ActQuit
)

// Action is the reaction to a click.
type Action struct {
	Kind    ActionKind
	Profile string
	Enable  bool // ActSetAutostart
	Env     bool // ActSelect: environment scope
	Hosts   bool // ActSelect: hosts scope
}

// React maps a click on the menu shown for m to an action. "Environment" and
// "Hosts" toggle that scope of the profile (another profile starts from no
// scope); turning the last scope off clears the selection. "Hosts" only
// records the choice: writing the hosts file needs administrator rights, so
// it happens only through "Apply hosts (asks for administrator)...". "None"
// when nothing is selected does nothing.
func React(m Model, t Target) Action {
	switch t.Kind {
	case TargetEnv, TargetHosts, TargetApplyHosts:
		if t.Slot < 0 || t.Slot >= MaxProfiles || !m.Slots[t.Slot].Visible {
			return Action{}
		}
		s := m.Slots[t.Slot]
		if t.Kind == TargetApplyHosts {
			return Action{Kind: ActApplyHosts, Profile: s.Profile}
		}
		env, hosts := s.Env, s.Hosts
		if t.Kind == TargetEnv {
			env = !env
		} else {
			hosts = !hosts
		}
		if !env && !hosts {
			return Action{Kind: ActClear}
		}
		return Action{Kind: ActSelect, Profile: s.Profile, Env: env, Hosts: hosts}
	case TargetNone:
		if m.NoneChecked {
			return Action{}
		}
		return Action{Kind: ActClear}
	case TargetOpenTUI:
		return Action{Kind: ActOpenTUI}
	case TargetOpenFolder:
		return Action{Kind: ActOpenFolder}
	case TargetAutostart:
		return Action{Kind: ActSetAutostart, Enable: !m.Autostart}
	case TargetQuit:
		return Action{Kind: ActQuit}
	}
	return Action{}
}

// Summary is a one-line description of the model for the debug log.
func (m Model) Summary() string {
	visible, checked, env, hosts := 0, "", false, false
	for _, s := range m.Slots {
		if s.Visible {
			visible++
			if s.Checked {
				checked, env, hosts = s.Profile, s.Env, s.Hosts
			}
		}
	}
	return fmt.Sprintf("tooltip=%q profiles=%d checked=%q env=%v hosts=%v none=%v more=%q empty=%v autostart=%v icon_selected=%v",
		m.Tooltip, visible, checked, env, hosts, m.NoneChecked, m.More, m.Empty, m.Autostart, m.HasSelection)
}
