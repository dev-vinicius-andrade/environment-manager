package profile

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// MaskedValue is shown instead of a variable value. It does not reveal length.
const MaskedValue = "********"

// Sections of a profile for Write.
const (
	SectionAll   = ""
	SectionVars  = "vars"
	SectionHosts = "hosts"
)

// CheckSection validates a --section value.
func CheckSection(s string) error {
	switch s {
	case SectionAll, "all", SectionVars, SectionHosts:
		return nil
	}
	return fmt.Errorf("invalid --section %q: use vars or hosts", s)
}

// Write prints a profile: variables as sorted NAME=value lines (values masked
// unless reveal) and hosts entries as "IP NAME... [# comment]" lines (hosts
// are not secret). section limits the output to one part. In the full view a
// "# variables" / "# hosts" header precedes each part only when the profile
// has hosts entries, so a variables-only profile prints exactly as before.
func Write(w io.Writer, p Profile, reveal bool, section string) error {
	if section == "all" {
		section = SectionAll
	}
	headers := section == SectionAll && len(p.Hosts) > 0
	if section != SectionHosts {
		if headers && len(p.Vars) > 0 {
			if _, err := fmt.Fprintln(w, "# variables"); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(p.Vars))
		for k := range p.Vars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := MaskedValue
			if reveal {
				v = p.Vars[k]
			}
			if _, err := fmt.Fprintf(w, "%s=%s\n", k, v); err != nil {
				return err
			}
		}
	}
	if section != SectionVars {
		if headers {
			if _, err := fmt.Fprintln(w, "# hosts"); err != nil {
				return err
			}
		}
		for _, e := range p.Hosts {
			line := e.IP + " " + strings.Join(e.Names, " ")
			if e.Comment != "" {
				line += "  # " + e.Comment
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}
	return nil
}
