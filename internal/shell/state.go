package shell

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// State variable names kept in the shell's environment.
const (
	EnvProfile = "ENVMGR_PROFILE"
	EnvVars    = "ENVMGR_VARS"
	PrevPrefix = "ENVMGR_PREV_"
	// EnvPinned is "1" in a session that chose its profile itself (use, TUI):
	// such a session ignores the machine-wide selection until `envmgr follow`.
	EnvPinned = "ENVMGR_PINNED"
)

// Pinned reports whether the session read through lookup is pinned
// (ENVMGR_PINNED set to anything but "" or "0").
func Pinned(lookup LookupFunc) bool {
	v, ok := lookup(EnvPinned)
	v = strings.TrimSpace(v)
	return ok && v != "" && v != "0"
}

// reservedPrefix marks variables owned by envmgr; profiles may not set them.
const reservedPrefix = "ENVMGR_"

// Reserved reports whether name belongs to envmgr's own namespace (ENVMGR_*,
// case-insensitive because Windows environment names are).
func Reserved(name string) bool {
	return strings.HasPrefix(strings.ToUpper(name), reservedPrefix)
}

// LookupFunc reads one environment variable (os.LookupEnv semantics).
type LookupFunc func(string) (string, bool)

// OSLookup reads the current process environment.
var OSLookup LookupFunc = os.LookupEnv

// Op is one environment change: set Name to Value, or unset it.
type Op struct {
	Name  string
	Value string
	Unset bool
}

func setOp(name, value string) Op { return Op{Name: name, Value: value} }
func unsetOp(name string) Op      { return Op{Name: name, Unset: true} }

// State is the activation state found in an environment.
type State struct {
	Profile string   // active profile name, "" when none
	Vars    []string // variables set by the active profile (validated, sorted, unique)
}

// Active reports whether a profile is active (or left variables to restore).
func (s State) Active() bool { return s.Profile != "" || len(s.Vars) > 0 }

// ReadState reads ENVMGR_PROFILE / ENVMGR_VARS. Names in ENVMGR_VARS that are
// not valid variable names are dropped: they are never embedded in shell code.
func ReadState(lookup LookupFunc) State {
	st := State{}
	st.Profile, _ = lookup(EnvProfile)
	raw, _ := lookup(EnvVars)
	seen := map[string]bool{}
	for _, n := range strings.Split(raw, ",") {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] || !profile.ValidVar(n) || Reserved(n) {
			continue
		}
		seen[n] = true
		st.Vars = append(st.Vars, n)
	}
	sort.Strings(st.Vars)
	return st
}

// restoreOps returns the ops that undo st: each managed variable gets its
// backed-up value back (or is unset when it had none) and the backups are removed.
func restoreOps(lookup LookupFunc, st State) []Op {
	ops := []Op{}
	for _, n := range st.Vars {
		if prev, ok := lookup(PrevPrefix + n); ok {
			ops = append(ops, setOp(n, prev))
		} else {
			ops = append(ops, unsetOp(n))
		}
		ops = append(ops, unsetOp(PrevPrefix+n))
	}
	return ops
}

// UseOps returns the changes that activate p in an environment read through
// lookup: undo the active profile (if any), back up and set p's variables, and
// record the new state. The session is pinned (ENVMGR_PINNED=1): it no longer
// follows the machine-wide selection.
func UseOps(lookup LookupFunc, p profile.Profile) ([]Op, error) {
	return useOps(lookup, p, true)
}

// FollowUseOps is UseOps for a session that follows the machine-wide selection
// (envmgr sync / follow): same changes, but ENVMGR_PINNED is removed instead of set.
func FollowUseOps(lookup LookupFunc, p profile.Profile) ([]Op, error) {
	return useOps(lookup, p, false)
}

func useOps(lookup LookupFunc, p profile.Profile, pin bool) ([]Op, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(p.Vars))
	for n := range p.Vars {
		if Reserved(n) {
			return nil, fmt.Errorf("variable %q uses the reserved ENVMGR_ prefix", n)
		}
		names = append(names, n)
	}
	sort.Strings(names)

	st := ReadState(lookup)
	managed := map[string]bool{}
	for _, n := range st.Vars {
		managed[n] = true
	}
	// original returns the value a variable has once the active profile is undone.
	original := func(n string) (string, bool) {
		if managed[n] {
			return lookup(PrevPrefix + n)
		}
		return lookup(n)
	}

	ops := restoreOps(lookup, st)
	for _, n := range names {
		if v, ok := original(n); ok {
			ops = append(ops, setOp(PrevPrefix+n, v))
		} else {
			ops = append(ops, unsetOp(PrevPrefix+n))
		}
		ops = append(ops, setOp(n, p.Vars[n]))
	}
	ops = append(ops, setOp(EnvProfile, p.Name))
	if len(names) > 0 {
		ops = append(ops, setOp(EnvVars, strings.Join(names, ",")))
	} else {
		ops = append(ops, unsetOp(EnvVars))
	}
	if pin {
		ops = append(ops, setOp(EnvPinned, "1"))
	} else {
		ops = append(ops, unsetOp(EnvPinned))
	}
	return ops, nil
}

// DeactivateOps returns the changes that undo the active profile and clear the
// state variables (ENVMGR_PINNED included, so the session follows the
// machine-wide selection again). It returns no ops when nothing is active.
func DeactivateOps(lookup LookupFunc) []Op {
	st := ReadState(lookup)
	if !st.Active() {
		return nil
	}
	ops := restoreOps(lookup, st)
	return append(ops, unsetOp(EnvProfile), unsetOp(EnvVars), unsetOp(EnvPinned))
}

// UnpinOps returns the op that clears ENVMGR_PINNED (none when not pinned).
func UnpinOps(lookup LookupFunc) []Op {
	if _, ok := lookup(EnvPinned); !ok {
		return nil
	}
	return []Op{unsetOp(EnvPinned)}
}

// Apply applies ops to an environment map (used by tests and simulations).
func Apply(env map[string]string, ops []Op) {
	for _, o := range ops {
		if o.Unset {
			delete(env, o.Name)
		} else {
			env[o.Name] = o.Value
		}
	}
}

// MapLookup adapts a map to a LookupFunc.
func MapLookup(env map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

// Use renders the shell code that activates p.
func Use(k Kind, lookup LookupFunc, p profile.Profile) (string, error) {
	ops, err := UseOps(lookup, p)
	if err != nil {
		return "", err
	}
	return Render(k, ops)
}

// Deactivate renders the shell code that undoes the active profile ("" when none).
func Deactivate(k Kind, lookup LookupFunc) (string, error) {
	return Render(k, DeactivateOps(lookup))
}

// FollowUse renders the shell code that activates p for a session following
// the machine-wide selection (no pin; see FollowUseOps).
func FollowUse(k Kind, lookup LookupFunc, p profile.Profile) (string, error) {
	ops, err := FollowUseOps(lookup, p)
	if err != nil {
		return "", err
	}
	return Render(k, ops)
}
