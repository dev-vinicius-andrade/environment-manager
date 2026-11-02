package remote

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sort"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// Role is what the parent may do on a child.
type Role string

const (
	RoleReadWrite Role = "rw"
	RoleReadOnly  Role = "ro"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleReadWrite || r == RoleReadOnly }

// The complete allow-list of operations a child will ever execute.
const (
	OpList   = "list"
	OpGet    = "get"
	OpPut    = "put"
	OpDelete = "delete"
	OpSet    = "set"
	OpUnset  = "unset"
)

// Job is one operation relayed from the parent to a child.
type Job struct {
	ID   string  `json:"id"`
	Op   string  `json:"op"`
	Args JobArgs `json:"args"`
}

// JobArgs are the operation arguments; which ones are used depends on Op.
type JobArgs struct {
	Profile string            `json:"profile,omitempty"`
	Var     string            `json:"var,omitempty"`
	Value   *string           `json:"value,omitempty"`
	Vars    map[string]string `json:"vars,omitempty"`
}

// Result is a child's answer to a Job.
type Result struct {
	OK       bool             `json:"ok"`
	Error    string           `json:"error,omitempty"`
	NotFound bool             `json:"notFound,omitempty"`
	Profiles []string         `json:"profiles,omitempty"`
	Profile  *profile.Profile `json:"profile,omitempty"`
}

// Mutates reports whether the op changes the child's store.
func (j Job) Mutates() bool {
	switch j.Op {
	case OpPut, OpDelete, OpSet, OpUnset:
		return true
	}
	return false
}

// Validate checks the op against the allow-list and every name.
func (j Job) Validate() error {
	a := j.Args
	needProfile := func() error {
		if !profile.ValidName(a.Profile) {
			return fmt.Errorf("invalid profile name %q", a.Profile)
		}
		return nil
	}
	needVar := func() error {
		if !profile.ValidVar(a.Var) {
			return fmt.Errorf("invalid variable name %q", a.Var)
		}
		return nil
	}
	switch j.Op {
	case OpList:
		return nil
	case OpGet, OpDelete:
		return needProfile()
	case OpPut:
		if err := needProfile(); err != nil {
			return err
		}
		return profile.Profile{Name: a.Profile, Vars: a.Vars}.Validate()
	case OpSet:
		if err := needProfile(); err != nil {
			return err
		}
		if a.Value == nil {
			return errors.New("missing value")
		}
		return needVar()
	case OpUnset:
		if err := needProfile(); err != nil {
			return err
		}
		return needVar()
	}
	return fmt.Errorf("unknown operation %q", j.Op)
}

// Execute runs an allow-listed job against store. Mutations are refused when
// role is not rw. It never touches anything outside the profile store.
func Execute(store *profile.Store, role Role, j Job) Result {
	if err := j.Validate(); err != nil {
		return Result{Error: err.Error()}
	}
	if j.Mutates() && role != RoleReadWrite {
		return Result{Error: "this child is read-only for its parent"}
	}
	fail := func(err error) Result {
		if errors.Is(err, profile.ErrNotFound) {
			return Result{Error: "profile not found", NotFound: true}
		}
		return Result{Error: err.Error()}
	}
	a := j.Args
	switch j.Op {
	case OpList:
		names, err := store.List()
		if err != nil {
			return fail(err)
		}
		return Result{OK: true, Profiles: names}
	case OpGet:
		p, err := store.Get(a.Profile)
		if err != nil {
			return fail(err)
		}
		return Result{OK: true, Profile: &p}
	case OpPut:
		vars := a.Vars
		if vars == nil {
			vars = map[string]string{}
		}
		if err := store.Save(profile.Profile{Name: a.Profile, Vars: vars}); err != nil {
			return fail(err)
		}
	case OpDelete:
		if err := store.Delete(a.Profile); err != nil {
			return fail(err)
		}
	case OpSet:
		if err := store.SetVar(a.Profile, a.Var, *a.Value); err != nil {
			return fail(err)
		}
	case OpUnset:
		if err := store.UnsetVar(a.Profile, a.Var); err != nil {
			return fail(err)
		}
	}
	return Result{OK: true}
}

// MaskedValue is shown instead of a secret value. It does not reveal length.
const MaskedValue = "********"

// WriteProfile prints a profile as sorted KEY=value lines; values are masked
// unless reveal is true.
func WriteProfile(w io.Writer, p profile.Profile, reveal bool) error {
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
	return nil
}

func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }
