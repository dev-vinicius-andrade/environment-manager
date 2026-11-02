package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"text/tabwriter"
)

const (
	// BeginMarker and EndMarker delimit the only lines envmgr ever writes.
	BeginMarker = "# BEGIN envmgr"
	EndMarker   = "# END envmgr"
	// headerComment is written right after BeginMarker and skipped on parse.
	headerComment  = "# Managed by envmgr (envmgr hosts). Lines between BEGIN and END are rewritten by envmgr."
	groupPrefix    = "# group:"
	disabledSuffix = "(disabled)"
)

var (
	// ErrNotFound means the hostname is not in the managed block.
	ErrNotFound = errors.New("not found in the managed block")
	// ErrGroupNotFound means the group does not exist in the managed block.
	ErrGroupNotFound = errors.New("group not found in the managed block")
)

// CriticalNameError refuses a name the system relies on (see IsCritical).
type CriticalNameError struct{ Name string }

func (e *CriticalNameError) Error() string {
	return fmt.Sprintf("refusing to shadow the critical name %q (use --force if you really mean it)", e.Name)
}

// Entry is one hosts line: an address and its names.
type Entry struct {
	IP        string   `json:"ip"`
	Hostnames []string `json:"hostnames"`
}

// Group is a named set of entries switched on or off together. A disabled
// group stays in the block with its entries commented out.
type Group struct {
	Name    string  `json:"name"`
	Enabled bool    `json:"enabled"`
	Entries []Entry `json:"entries"`
}

// Line is one line outside the managed block (shown read-only).
type Line struct {
	N    int    // 1-based line number in the file
	Text string // without the line terminator
}

// Doc is a parsed hosts file: the bytes around the managed block, kept
// verbatim, and the block's groups.
type Doc struct {
	Groups []Group

	raw           []byte
	eol           string
	prefix        []byte // bytes before the BEGIN line (the whole file when there is no block)
	suffix        []byte // bytes after the END line's terminator
	hasBlock      bool
	endTerminated bool // the END line had a line terminator
	unmanaged     []Line
}

type rawLine struct {
	start, next int // byte offsets: line start, start of the following line
	text        string
}

func splitRaw(raw []byte) []rawLine {
	var out []rawLine
	for pos := 0; pos < len(raw); {
		end := bytes.IndexByte(raw[pos:], '\n')
		next := len(raw)
		textEnd := len(raw)
		if end >= 0 {
			textEnd = pos + end
			next = textEnd + 1
		}
		text := string(raw[pos:textEnd])
		text = strings.TrimSuffix(text, "\r")
		out = append(out, rawLine{start: pos, next: next, text: text})
		pos = next
	}
	return out
}

func defaultEOL() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

// Parse reads a hosts file. It fails on files it cannot edit safely: UTF-16
// or binary content, more than one managed block, a BEGIN without END (or the
// reverse), or a line inside the block that envmgr did not write.
func Parse(raw []byte) (*Doc, error) {
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) {
		return nil, errors.New("the hosts file is UTF-16 encoded; envmgr only edits ASCII/UTF-8 hosts files")
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return nil, errors.New("the hosts file contains NUL bytes; refusing to edit it")
	}
	d := &Doc{raw: raw, eol: defaultEOL()}
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		d.eol = "\n"
		if i > 0 && raw[i-1] == '\r' {
			d.eol = "\r\n"
		}
	}
	lines := splitRaw(raw)
	begin, end := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l.text) {
		case BeginMarker:
			if begin >= 0 {
				return nil, fmt.Errorf("line %d: more than one %q line; fix the file by hand or restore a backup", i+1, BeginMarker)
			}
			begin = i
		case EndMarker:
			if begin < 0 {
				return nil, fmt.Errorf("line %d: %q without %q; fix the file by hand or restore a backup", i+1, EndMarker, BeginMarker)
			}
			if end >= 0 {
				return nil, fmt.Errorf("line %d: more than one %q line; fix the file by hand or restore a backup", i+1, EndMarker)
			}
			end = i
		}
	}
	if begin >= 0 && end < 0 {
		return nil, fmt.Errorf("line %d: %q without %q; fix the file by hand or restore a backup", begin+1, BeginMarker, EndMarker)
	}
	if begin < 0 {
		d.prefix = raw
		for i, l := range lines {
			d.unmanaged = append(d.unmanaged, Line{N: i + 1, Text: l.text})
		}
		return d, nil
	}
	d.hasBlock = true
	d.prefix = raw[:lines[begin].start]
	d.suffix = raw[lines[end].next:]
	d.endTerminated = lines[end].next > lines[end].start && raw[lines[end].next-1] == '\n'
	for i, l := range lines {
		if i < begin || i > end {
			d.unmanaged = append(d.unmanaged, Line{N: i + 1, Text: l.text})
		}
	}
	body := make([]string, 0, end-begin)
	for _, l := range lines[begin+1 : end] {
		body = append(body, l.text)
	}
	groups, err := parseBlock(body, begin+2)
	if err != nil {
		return nil, err
	}
	d.Groups = groups
	return d, nil
}

func parseBlock(lines []string, firstLine int) ([]Group, error) {
	var groups []Group
	cur := -1
	find := func(name string) int {
		for i, g := range groups {
			if g.Name == name {
				return i
			}
		}
		return -1
	}
	for i, l := range lines {
		n := firstLine + i
		t := strings.TrimSpace(l)
		switch {
		case t == "" || t == headerComment:
			continue
		case strings.HasPrefix(t, groupPrefix):
			rest := strings.TrimSpace(t[len(groupPrefix):])
			enabled := true
			if strings.HasSuffix(rest, disabledSuffix) {
				enabled = false
				rest = strings.TrimSpace(strings.TrimSuffix(rest, disabledSuffix))
			}
			if !ValidGroup(rest) {
				return nil, fmt.Errorf("line %d: invalid group name %q in the managed block", n, rest)
			}
			if find(rest) >= 0 {
				return nil, fmt.Errorf("line %d: group %q appears twice in the managed block", n, rest)
			}
			groups = append(groups, Group{Name: rest, Enabled: enabled})
			cur = len(groups) - 1
		default:
			body, commented := t, false
			if strings.HasPrefix(t, "#") {
				body, commented = strings.TrimSpace(t[1:]), true
			}
			if cur < 0 {
				if find(DefaultGroup) >= 0 {
					return nil, fmt.Errorf("line %d: entry outside any group", n)
				}
				groups = append(groups, Group{Name: DefaultGroup, Enabled: true})
				cur = len(groups) - 1
			}
			g := &groups[cur]
			if commented == g.Enabled {
				return nil, fmt.Errorf("line %d: %q does not match the state of group %q; envmgr did not write this line, fix the block by hand", n, t, g.Name)
			}
			fields := strings.Fields(body)
			if len(fields) < 2 {
				return nil, fmt.Errorf("line %d: %q is not an \"<ip> <hostname>...\" entry", n, t)
			}
			ip, err := ParseIP(fields[0])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			names, err := NormalizeHostnames(fields[1:])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			g.Entries = append(g.Entries, Entry{IP: ip, Hostnames: names})
		}
	}
	return groups, nil
}

// HasBlock reports whether the file already has a managed block.
func (d *Doc) HasBlock() bool { return d.hasBlock }

// Unmanaged returns the lines outside the managed block (read-only).
func (d *Doc) Unmanaged() []Line { return d.unmanaged }

// Group returns the named group, or nil.
func (d *Doc) Group(name string) *Group {
	for i := range d.Groups {
		if d.Groups[i].Name == name {
			return &d.Groups[i]
		}
	}
	return nil
}

func (d *Doc) blockLines() []string {
	out := []string{BeginMarker, headerComment}
	for _, g := range d.Groups {
		h := groupPrefix + " " + g.Name
		if !g.Enabled {
			h += " " + disabledSuffix
		}
		out = append(out, h)
		for _, e := range g.Entries {
			l := e.IP + " " + strings.Join(e.Hostnames, " ")
			if !g.Enabled {
				l = "# " + l
			}
			out = append(out, l)
		}
	}
	return append(out, EndMarker)
}

// Render returns the file with the managed block regenerated. Bytes outside
// the block are copied verbatim; the block uses the file's line ending and
// the file keeps its trailing-newline state. A new block goes at the end.
func (d *Doc) Render() []byte {
	var b bytes.Buffer
	b.Write(d.prefix)
	terminateLast := d.endTerminated
	if !d.hasBlock {
		terminateLast = len(d.raw) == 0 || d.raw[len(d.raw)-1] == '\n'
		if len(d.prefix) > 0 && d.prefix[len(d.prefix)-1] != '\n' {
			b.WriteString(d.eol) // the last unmanaged line had no terminator
		}
	}
	lines := d.blockLines()
	for i, l := range lines {
		b.WriteString(l)
		if i < len(lines)-1 || terminateLast {
			b.WriteString(d.eol)
		}
	}
	b.Write(d.suffix)
	return b.Bytes()
}

// Add maps hostnames to ip in group ("" = DefaultGroup), creating the group
// (enabled) if needed. It is idempotent. A hostname already mapped to another
// address in the same group moves to ip. Critical names (see IsCritical) are
// refused unless force is set.
func (d *Doc) Add(ip string, hostnames []string, group string, force bool) (bool, error) {
	ip, err := ParseIP(ip)
	if err != nil {
		return false, err
	}
	names, err := NormalizeHostnames(hostnames)
	if err != nil {
		return false, err
	}
	if group, err = CheckGroup(group); err != nil {
		return false, err
	}
	if !force {
		for _, n := range names {
			if IsCritical(n) {
				return false, &CriticalNameError{Name: n}
			}
		}
	}
	changed := false
	g := d.Group(group)
	if g == nil {
		d.Groups = append(d.Groups, Group{Name: group, Enabled: true})
		g = &d.Groups[len(d.Groups)-1]
		changed = true
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	// Move names mapped to another address in this group.
	kept := g.Entries[:0]
	for _, e := range g.Entries {
		if e.IP != ip {
			hs := e.Hostnames[:0:0]
			for _, h := range e.Hostnames {
				if want[h] {
					changed = true
					continue
				}
				hs = append(hs, h)
			}
			e.Hostnames = hs
			if len(hs) == 0 {
				continue
			}
		}
		kept = append(kept, e)
	}
	g.Entries = kept
	for i := range g.Entries {
		e := &g.Entries[i]
		if e.IP != ip {
			continue
		}
		have := map[string]bool{}
		for _, h := range e.Hostnames {
			have[h] = true
		}
		merged := append([]string(nil), e.Hostnames...)
		for _, n := range names {
			if !have[n] {
				merged = append(merged, n)
			}
		}
		if len(merged) > MaxHostnamesPerEntry {
			return false, fmt.Errorf("too many hostnames for %s in group %q (%d, max %d)", ip, group, len(merged), MaxHostnamesPerEntry)
		}
		if len(merged) != len(e.Hostnames) {
			e.Hostnames = merged
			changed = true
		}
		return changed, nil
	}
	g.Entries = append(g.Entries, Entry{IP: ip, Hostnames: names})
	return true, nil
}

// Remove deletes hostname from group, or from every user group when group is
// "". Entries and groups left empty are dropped. Profile groups
// ("profile:<name>") are never edited here: they belong to `hosts apply`.
func (d *Doc) Remove(hostname, group string) (bool, error) {
	h, err := ValidHostname(hostname)
	if err != nil {
		return false, err
	}
	if group != "" {
		if _, err := CheckGroup(group); err != nil {
			return false, err
		}
		if d.Group(group) == nil {
			return false, fmt.Errorf("%w: %q", ErrGroupNotFound, group)
		}
	}
	removed := false
	groups := d.Groups[:0]
	for _, g := range d.Groups {
		_, isProfile := ProfileOfGroup(g.Name)
		if !isProfile && (group == "" || g.Name == group) {
			entries := g.Entries[:0]
			for _, e := range g.Entries {
				hs := e.Hostnames[:0:0]
				for _, x := range e.Hostnames {
					if x == h {
						removed = true
						continue
					}
					hs = append(hs, x)
				}
				if len(hs) > 0 {
					e.Hostnames = hs
					entries = append(entries, e)
				}
			}
			g.Entries = entries
			if len(g.Entries) == 0 {
				continue
			}
		}
		groups = append(groups, g)
	}
	d.Groups = groups
	if !removed {
		return false, fmt.Errorf("hostname %q: %w", h, ErrNotFound)
	}
	return true, nil
}

// SetGroupEnabled switches a group on or off; a disabled group's entries stay
// in the block, commented out.
func (d *Doc) SetGroupEnabled(group string, enabled bool) (bool, error) {
	if !ValidGroup(group) {
		return false, fmt.Errorf("invalid group name %q", group)
	}
	g := d.Group(group)
	if g == nil {
		return false, fmt.Errorf("%w: %q", ErrGroupNotFound, group)
	}
	if g.Enabled == enabled {
		return false, nil
	}
	g.Enabled = enabled
	return true, nil
}

// WriteTable prints groups as GROUP STATE IP HOSTNAMES rows.
func WriteTable(w io.Writer, groups []Group) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tSTATE\tIP\tHOSTNAMES")
	for _, g := range groups {
		state := "enabled"
		if !g.Enabled {
			state = "disabled"
		}
		for _, e := range g.Entries {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", g.Name, state, e.IP, strings.Join(e.Hostnames, " "))
		}
	}
	return tw.Flush()
}
