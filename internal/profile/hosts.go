package profile

import (
	"errors"
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hostspec"
)

// MaxHostEntries caps the hosts entries of one profile.
const MaxHostEntries = 256

// ErrHostNotFound means the profile has no entry for the hostname or IP.
var ErrHostNotFound = errors.New("not in the profile's hosts entries")

// HostEntry is one hosts line of a profile: an address and its names. Comment
// is an optional note kept in the profile only (it is not written to the
// hosts file).
type HostEntry struct {
	IP      string   `json:"ip"`
	Names   []string `json:"names"`
	Comment string   `json:"comment,omitempty"`
}

// NormalizeHosts validates entries with the same rules as the hosts file
// (internal/hostspec) and returns them in canonical form: canonical IPs,
// lower-case de-duplicated names. It refuses more than MaxHostEntries
// entries, an IP used by two entries and a hostname mapped twice. An empty
// list gives nil. Critical names (localhost, ...) are not refused here: they
// are refused when an entry is added and when a profile is applied.
func NormalizeHosts(entries []HostEntry) ([]HostEntry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) > MaxHostEntries {
		return nil, fmt.Errorf("too many hosts entries (%d, max %d)", len(entries), MaxHostEntries)
	}
	out := make([]HostEntry, 0, len(entries))
	ips := map[string]bool{}
	names := map[string]string{}
	for _, e := range entries {
		ip, err := hostspec.ParseIP(e.IP)
		if err != nil {
			return nil, err
		}
		if ips[ip] {
			return nil, fmt.Errorf("IP %s appears in two hosts entries", ip)
		}
		ips[ip] = true
		ns, err := hostspec.NormalizeHostnames(e.Names)
		if err != nil {
			return nil, fmt.Errorf("hosts entry %s: %w", ip, err)
		}
		for _, n := range ns {
			if other, ok := names[n]; ok {
				return nil, fmt.Errorf("hostname %q is mapped to both %s and %s", n, other, ip)
			}
			names[n] = ip
		}
		if err := hostspec.ValidComment(e.Comment); err != nil {
			return nil, fmt.Errorf("hosts entry %s: %w", ip, err)
		}
		out = append(out, HostEntry{IP: ip, Names: ns, Comment: e.Comment})
	}
	return out, nil
}

// AddHost maps hostnames to ip. A name mapped to another address moves to ip;
// names already on ip are kept and new ones appended. A non-empty comment
// replaces the entry's comment. Critical names (localhost, broadcasthost,
// ip6-*) are refused. It reports whether the profile changed.
func (p *Profile) AddHost(ip string, hostnames []string, comment string) (bool, error) {
	ip, err := hostspec.ParseIP(ip)
	if err != nil {
		return false, err
	}
	names, err := hostspec.NormalizeHostnames(hostnames)
	if err != nil {
		return false, err
	}
	if err := hostspec.ValidComment(comment); err != nil {
		return false, err
	}
	for _, n := range names {
		if hostspec.IsCritical(n) {
			return false, fmt.Errorf("refusing to map the critical name %q in a profile (localhost, broadcasthost and ip6-* are never applied)", n)
		}
	}
	hs, err := NormalizeHosts(p.Hosts)
	if err != nil {
		return false, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	changed := false
	kept := hs[:0]
	idx := -1
	for _, e := range hs {
		if e.IP == ip {
			idx = len(kept)
			kept = append(kept, e)
			continue
		}
		left := e.Names[:0:0]
		for _, n := range e.Names {
			if want[n] {
				changed = true
				continue
			}
			left = append(left, n)
		}
		if len(left) == 0 {
			continue
		}
		e.Names = left
		kept = append(kept, e)
	}
	hs = kept
	if idx < 0 {
		if len(hs) >= MaxHostEntries {
			return false, fmt.Errorf("too many hosts entries (max %d)", MaxHostEntries)
		}
		p.Hosts = append(hs, HostEntry{IP: ip, Names: names, Comment: comment})
		return true, nil
	}
	e := &hs[idx]
	have := map[string]bool{}
	for _, n := range e.Names {
		have[n] = true
	}
	merged := append([]string(nil), e.Names...)
	for _, n := range names {
		if !have[n] {
			merged = append(merged, n)
		}
	}
	if len(merged) > hostspec.MaxHostnamesPerEntry {
		return false, fmt.Errorf("too many hostnames for %s (%d, max %d)", ip, len(merged), hostspec.MaxHostnamesPerEntry)
	}
	if len(merged) != len(e.Names) {
		e.Names = merged
		changed = true
	}
	if comment != "" && comment != e.Comment {
		e.Comment = comment
		changed = true
	}
	p.Hosts = hs
	return changed, nil
}

// RemoveHostname removes hostname from the profile's entries; an entry left
// without names is dropped.
func (p *Profile) RemoveHostname(hostname string) error {
	h, err := hostspec.ValidHostname(hostname)
	if err != nil {
		return err
	}
	removed := false
	var out []HostEntry
	for _, e := range p.Hosts {
		left := e.Names[:0:0]
		for _, n := range e.Names {
			if n == h {
				removed = true
				continue
			}
			left = append(left, n)
		}
		if len(left) > 0 {
			e.Names = left
			out = append(out, e)
		}
	}
	if !removed {
		return fmt.Errorf("hostname %q: %w", h, ErrHostNotFound)
	}
	p.Hosts = out
	return nil
}

// RemoveHostIP removes the whole entry of ip.
func (p *Profile) RemoveHostIP(ip string) error {
	c, err := hostspec.ParseIP(ip)
	if err != nil {
		return err
	}
	var out []HostEntry
	removed := false
	for _, e := range p.Hosts {
		if e.IP == c {
			removed = true
			continue
		}
		out = append(out, e)
	}
	if !removed {
		return fmt.Errorf("IP %s: %w", c, ErrHostNotFound)
	}
	p.Hosts = out
	return nil
}
