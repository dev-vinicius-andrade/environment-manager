// Package update implements `envmgr update`: find the latest GitHub release,
// download the archive for this OS/arch, verify its SHA-256 against the
// release's checksums.txt and replace the running executable.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version (vMAJOR.MINOR.PATCH[-pre][+build]).
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
}

// ParseVersion accepts an optional leading "v" and ignores "+build" metadata.
func ParseVersion(s string) (Version, error) {
	orig := s
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s, pre = s[:i], s[i+1:]
		if pre == "" {
			return Version{}, fmt.Errorf("invalid version %q", orig)
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH", orig)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || (len(p) > 1 && p[0] == '0') {
			return Version{}, fmt.Errorf("invalid version %q", orig)
		}
		n[i] = v
	}
	v := Version{Major: n[0], Minor: n[1], Patch: n[2]}
	if pre != "" {
		v.Pre = strings.Split(pre, ".")
	}
	return v, nil
}

// String returns the version without a leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// IsPrerelease reports whether the version has a pre-release part.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Compare returns -1, 0 or 1 following semver precedence: a pre-release is
// lower than its release; identifiers compare numerically when both are
// numbers, numbers rank below text, a shorter list ranks below a longer one.
func (v Version) Compare(o Version) int {
	for _, p := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if p[0] != p[1] {
			return cmpInt(p[0], p[1])
		}
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		a, b := v.Pre[i], o.Pre[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return cmpInt(an, bn)
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		case a != b:
			return cmpInt(strings.Compare(a, b), 0)
		}
	}
	return cmpInt(len(v.Pre), len(o.Pre))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
