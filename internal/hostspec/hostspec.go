// Package hostspec validates hosts-file values (IP addresses, host names,
// entry comments). It has no dependencies inside envmgr so that both
// internal/hosts (the hosts file) and internal/profile (hosts entries stored
// in a profile) use the same single implementation without an import cycle.
package hostspec

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"unicode"
)

const (
	// MaxHostnameLen is the RFC 1123 limit for a full host name.
	MaxHostnameLen = 253
	// MaxHostnamesPerEntry caps the names on one line (some resolvers have limits).
	MaxHostnamesPerEntry = 32
	// MaxCommentLen caps the optional comment of a profile hosts entry.
	MaxCommentLen = 128
)

var labelRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ParseIP validates an IPv4 or IPv6 address and returns its canonical form.
// Zone ids (fe80::1%eth0) are rejected: they are interface specific.
func ParseIP(s string) (string, error) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", fmt.Errorf("invalid IP address %q", s)
	}
	if a.Zone() != "" {
		return "", fmt.Errorf("invalid IP address %q: zone ids are not allowed", s)
	}
	return a.String(), nil
}

// ValidHostname checks an RFC 1123 host name (letters, digits, hyphens and
// dots; labels of 1-63 characters; 253 characters in total) and returns it in
// lower case. Anything else, including spaces, '#', and control characters
// that could start a new line or a comment, is rejected.
func ValidHostname(s string) (string, error) {
	if s == "" || len(s) > MaxHostnameLen {
		return "", fmt.Errorf("invalid hostname %q: must be 1-%d characters", s, MaxHostnameLen)
	}
	for _, label := range strings.Split(s, ".") {
		if !labelRe.MatchString(label) {
			return "", fmt.Errorf("invalid hostname %q: use letters, digits, '-' and '.' (RFC 1123)", s)
		}
	}
	return strings.ToLower(s), nil
}

// NormalizeHostnames validates names, lower-cases them and drops duplicates
// (order kept). One entry holds 1 to MaxHostnamesPerEntry names.
func NormalizeHostnames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one hostname is required")
	}
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		h, err := ValidHostname(n)
		if err != nil {
			return nil, err
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	if len(out) > MaxHostnamesPerEntry {
		return nil, fmt.Errorf("too many hostnames for one entry (%d, max %d)", len(out), MaxHostnamesPerEntry)
	}
	return out, nil
}

// IsCritical reports whether name is one the system relies on (localhost,
// broadcasthost, ip6-*): shadowing it can break the machine.
func IsCritical(name string) bool {
	n := strings.ToLower(name)
	return n == "localhost" || n == "localhost.localdomain" || n == "broadcasthost" || strings.HasPrefix(n, "ip6-")
}

// ValidComment checks the optional free-text comment of a profile hosts
// entry: at most MaxCommentLen characters, no '#' and no control characters
// (so it can never start a new line or a comment wherever it is shown).
func ValidComment(s string) error {
	if len(s) > MaxCommentLen {
		return fmt.Errorf("comment too long (%d bytes, max %d)", len(s), MaxCommentLen)
	}
	for _, r := range s {
		if r == '#' || unicode.IsControl(r) || unicode.In(r, unicode.Cf) || r == unicode.ReplacementChar {
			return fmt.Errorf("invalid comment %q: no '#', newlines or control characters", s)
		}
	}
	return nil
}
