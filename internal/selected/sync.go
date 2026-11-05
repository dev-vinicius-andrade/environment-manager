package selected

import (
	"errors"
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

// Action is what a following session must do to match the selection.
type Action int

const (
	// NoChange: nothing to emit.
	NoChange Action = iota
	// Activate Decision.Profile (without pinning the session).
	Activate
	// Deactivate the active profile.
	Deactivate
)

func (a Action) String() string {
	switch a {
	case Activate:
		return "activate"
	case Deactivate:
		return "deactivate"
	}
	return "none"
}

// Input is everything Decide looks at.
type Input struct {
	Selection string // selected profile ("" = none)
	Exists    bool   // the selected profile exists in the store
	Current   string // ENVMGR_PROFILE of the session
	Active    bool   // the session has an active profile (or variables to restore)
	Pinned    bool   // ENVMGR_PINNED is set
}

// Decision is the result of Decide.
type Decision struct {
	Action  Action
	Profile string // profile to activate (Activate only)
	Note    string // message for stderr, "" when none
}

// Decide is the sync decision table:
//
//	pinned                               -> nothing
//	no selection, profile active         -> deactivate
//	no selection, nothing active         -> nothing
//	selected profile missing             -> deactivate if active, with a note
//	selection == ENVMGR_PROFILE          -> nothing
//	otherwise                            -> activate the selection (unpinned)
func Decide(in Input) Decision {
	switch {
	case in.Pinned:
		return Decision{Action: NoChange}
	case in.Selection == "":
		if in.Active {
			return Decision{Action: Deactivate}
		}
		return Decision{Action: NoChange}
	case !in.Exists:
		note := fmt.Sprintf("envmgr: selected profile %q no longer exists", in.Selection)
		if in.Active {
			return Decision{Action: Deactivate, Note: note + "; deactivating"}
		}
		return Decision{Action: NoChange, Note: note}
	case in.Selection == in.Current:
		return Decision{Action: NoChange}
	}
	return Decision{Action: Activate, Profile: in.Selection}
}

// Result is the shell code a sync produced plus what it decided.
type Result struct {
	Decision  Decision
	Code      string    // shell code for stdout ("" when nothing to do)
	Selection Selection // the selection that was read
}

// Sync computes the code that makes the session read through lookup match the
// selection. With follow, the pin is ignored and always cleared (envmgr follow).
// Only the environment scope is followed: a selection without it counts as
// none here (the hosts scope never changes a shell). On error the code is
// empty.
func (s *Selector) Sync(k shell.Kind, lookup shell.LookupFunc, follow bool) (Result, error) {
	sel, err := s.Get()
	if err != nil {
		return Result{}, err
	}
	envProfile := sel.EnvProfile()
	st := shell.ReadState(lookup)
	in := Input{
		Selection: envProfile,
		Current:   st.Profile,
		Active:    st.Active(),
		Pinned:    shell.Pinned(lookup) && !follow,
	}
	var p profile.Profile
	if envProfile != "" {
		p, err = s.Profiles.Get(envProfile)
		switch {
		case err == nil:
			in.Exists = true
		case errors.Is(err, profile.ErrNotFound):
		default:
			return Result{}, err
		}
	}
	d := Decide(in)
	var ops []shell.Op
	switch d.Action {
	case Activate:
		ops, err = shell.FollowUseOps(lookup, p)
		if err != nil {
			return Result{Decision: d}, fmt.Errorf("profile %q: %w", p.Name, err)
		}
	case Deactivate:
		ops = shell.DeactivateOps(lookup)
	}
	if follow && d.Action == NoChange {
		ops = shell.UnpinOps(lookup)
	}
	code, err := shell.Render(k, ops)
	if err != nil {
		return Result{Decision: d}, err
	}
	return Result{Decision: d, Code: code, Selection: sel}, nil
}
