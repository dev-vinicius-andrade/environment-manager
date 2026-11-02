package hosts

import (
	"fmt"
	"reflect"
)

// AppliedProfiles returns the profiles whose group ("profile:<name>") is in
// the managed block, in block order. envmgr keeps at most one.
func (d *Doc) AppliedProfiles() []string {
	var out []string
	for _, g := range d.Groups {
		if p, ok := ProfileOfGroup(g.Name); ok {
			out = append(out, p)
		}
	}
	return out
}

// ApplyProfile replaces every profile group of the block by the group of
// profile name holding entries (enabled), so two profiles are never applied
// at once. The new group takes the place of the first old profile group, or
// goes at the end. An empty entries list only removes the old profile
// groups. Entries are validated like `hosts add` and critical names are
// always refused. User groups and every line outside the block are kept.
func (d *Doc) ApplyProfile(name string, entries []Entry) (bool, error) {
	group := ProfileGroup(name)
	if _, ok := ProfileOfGroup(group); !ok {
		return false, fmt.Errorf("invalid profile name %q", name)
	}
	clean := make([]Entry, 0, len(entries))
	seen := map[string]string{}
	ips := map[string]bool{}
	for _, e := range entries {
		ip, err := ParseIP(e.IP)
		if err != nil {
			return false, err
		}
		if ips[ip] {
			return false, fmt.Errorf("IP %s appears in two entries of profile %q", ip, name)
		}
		ips[ip] = true
		names, err := NormalizeHostnames(e.Hostnames)
		if err != nil {
			return false, err
		}
		for _, n := range names {
			if IsCritical(n) {
				return false, &CriticalNameError{Name: n}
			}
			if other, dup := seen[n]; dup {
				return false, fmt.Errorf("hostname %q is mapped to both %s and %s in profile %q", n, other, ip, name)
			}
			seen[n] = ip
		}
		clean = append(clean, Entry{IP: ip, Hostnames: names})
	}
	before := append([]Group(nil), d.Groups...)
	out := make([]Group, 0, len(d.Groups)+1)
	placed := false
	for _, g := range d.Groups {
		if _, ok := ProfileOfGroup(g.Name); !ok {
			out = append(out, g)
			continue
		}
		if !placed && len(clean) > 0 {
			out = append(out, Group{Name: group, Enabled: true, Entries: clean})
		}
		placed = true
	}
	if !placed && len(clean) > 0 {
		out = append(out, Group{Name: group, Enabled: true, Entries: clean})
	}
	d.Groups = out
	return !reflect.DeepEqual(normalizeGroups(before), normalizeGroups(out)), nil
}

// RemoveProfileGroups drops every profile group from the block.
func (d *Doc) RemoveProfileGroups() bool {
	out := d.Groups[:0:0]
	changed := false
	for _, g := range d.Groups {
		if _, ok := ProfileOfGroup(g.Name); ok {
			changed = true
			continue
		}
		out = append(out, g)
	}
	d.Groups = out
	return changed
}

// normalizeGroups maps nil and empty slices to the same value for comparison.
func normalizeGroups(gs []Group) []Group {
	out := make([]Group, 0, len(gs))
	for _, g := range gs {
		if len(g.Entries) == 0 {
			g.Entries = nil
		}
		out = append(out, g)
	}
	return out
}
