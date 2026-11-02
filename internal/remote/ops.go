package remote

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
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

	// Hosts operations need the CapHosts grant. They act only on the child's
	// own hosts file (resolved locally); the protocol carries no path.
	OpHostsList   = "hosts.list"
	OpHostsAdd    = "hosts.add"
	OpHostsRemove = "hosts.remove"
	OpHostsGroup  = "hosts.group"
	// OpHostsApplyProfile applies the hosts entries of a profile from the
	// child's OWN store (only the profile name travels; no hosts data from
	// the network); OpHostsUnapply removes the applied profile's group.
	OpHostsApplyProfile = "hosts.apply-profile"
	OpHostsUnapply      = "hosts.unapply"
)

// CapHosts is the explicit, off-by-default grant (`parent invite
// --allow-hosts`) that lets a parent edit the child's hosts file.
const CapHosts = "hosts"

// Error codes carried in Result.Code.
const (
	CodeNotGranted       = "not_granted"
	CodePermissionDenied = "permission_denied"
)

// ValidCaps reports whether every capability is known.
func ValidCaps(caps []string) bool {
	for _, c := range caps {
		if c != CapHosts {
			return false
		}
	}
	return true
}

// HasCap reports whether caps contains c.
func HasCap(caps []string, c string) bool {
	for _, x := range caps {
		if x == c {
			return true
		}
	}
	return false
}

// Job is one operation relayed from the parent to a child.
type Job struct {
	ID   string  `json:"id"`
	Op   string  `json:"op"`
	Args JobArgs `json:"args"`
}

// JobArgs are the operation arguments; which ones are used depends on Op.
// There is deliberately no file path: a child edits only its own hosts file.
type JobArgs struct {
	Profile string            `json:"profile,omitempty"`
	Var     string            `json:"var,omitempty"`
	Value   *string           `json:"value,omitempty"`
	Vars    map[string]string `json:"vars,omitempty"`
	// Hosts are the hosts entries of a put (a profile edit: no hosts grant
	// needed). Children older than this field ignore it and keep no hosts.
	Hosts []profile.HostEntry `json:"hosts,omitempty"`

	IP        string   `json:"ip,omitempty"`        // hosts.add
	Hostnames []string `json:"hostnames,omitempty"` // hosts.add (1-32), hosts.remove (exactly 1)
	Group     string   `json:"group,omitempty"`     // hosts.add/remove (optional), hosts.group
	Enable    *bool    `json:"enable,omitempty"`    // hosts.group
	DryRun    bool     `json:"dryRun,omitempty"`    // hosts mutations: return the diff, write nothing
	// hosts.apply-profile uses only Profile (and DryRun).
}

// Result is a child's answer to a Job.
type Result struct {
	OK       bool             `json:"ok"`
	Error    string           `json:"error,omitempty"`
	Code     string           `json:"code,omitempty"`
	NotFound bool             `json:"notFound,omitempty"`
	Profiles []string         `json:"profiles,omitempty"`
	Profile  *profile.Profile `json:"profile,omitempty"`
	Hosts    *HostsResult     `json:"hosts,omitempty"`
}

// HostsResult is the answer to a hosts.* operation.
type HostsResult struct {
	Path    string        `json:"path"` // the child's hosts file (informational)
	Groups  []hosts.Group `json:"groups,omitempty"`
	Changed bool          `json:"changed,omitempty"`
	Diff    string        `json:"diff,omitempty"`
	Backup  string        `json:"backup,omitempty"`
	Warning string        `json:"warning,omitempty"`
	// Applied lists the profiles applied before (hosts.unapply) or now
	// (hosts.apply-profile).
	Applied []string `json:"applied,omitempty"`
}

// IsHosts reports whether the op is a hosts.* operation.
func (j Job) IsHosts() bool { return strings.HasPrefix(j.Op, "hosts.") }

// Mutates reports whether the op changes the child's store or hosts file.
// A hosts dry run still counts: it needs the same rights as the real change.
func (j Job) Mutates() bool {
	switch j.Op {
	case OpPut, OpDelete, OpSet, OpUnset, OpHostsAdd, OpHostsRemove, OpHostsGroup, OpHostsApplyProfile, OpHostsUnapply:
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
		return profile.Profile{Name: a.Profile, Vars: a.Vars, Hosts: a.Hosts}.Validate()
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
	case OpHostsList:
		return nil
	case OpHostsAdd:
		if _, err := hosts.ParseIP(a.IP); err != nil {
			return err
		}
		if _, err := hosts.NormalizeHostnames(a.Hostnames); err != nil {
			return err
		}
		_, err := hosts.CheckGroup(a.Group)
		return err
	case OpHostsRemove:
		if len(a.Hostnames) != 1 {
			return errors.New("hosts.remove takes exactly one hostname")
		}
		if _, err := hosts.ValidHostname(a.Hostnames[0]); err != nil {
			return err
		}
		if a.Group != "" && !hosts.ValidGroup(a.Group) {
			return fmt.Errorf("invalid group name %q", a.Group)
		}
		return nil
	case OpHostsGroup:
		if !hosts.ValidGroup(a.Group) {
			return fmt.Errorf("invalid group name %q", a.Group)
		}
		if a.Enable == nil {
			return errors.New("missing enable/disable")
		}
		return nil
	case OpHostsApplyProfile, OpHostsUnapply:
		if j.Op == OpHostsApplyProfile {
			if err := needProfile(); err != nil {
				return err
			}
		} else if a.Profile != "" {
			return errors.New("hosts.unapply takes no profile")
		}
		// Only the profile name travels: the child applies the entries of its
		// own profile, never hosts data sent over the network.
		if a.IP != "" || len(a.Hostnames) != 0 || a.Group != "" || a.Enable != nil || len(a.Hosts) != 0 || len(a.Vars) != 0 || a.Var != "" || a.Value != nil {
			return fmt.Errorf("%s takes only a profile name and dryRun (the child uses its own profile)", j.Op)
		}
		return nil
	}
	return fmt.Errorf("unknown operation %q", j.Op)
}

// Target is what a child executes jobs against.
type Target struct {
	Store *profile.Store
	Role  Role
	Caps  []string
	// HostsFile is the child's own hosts file, resolved locally (never from
	// the network).
	HostsFile string
}

// Execute runs an allow-listed profile job against store. Mutations are
// refused when role is not rw. Hosts jobs are refused (no grant).
func Execute(store *profile.Store, role Role, j Job) Result {
	return ExecuteJob(Target{Store: store, Role: role}, j)
}

// ExecuteJob runs an allow-listed job against t. Mutations are refused when
// the role is not rw; hosts jobs also need the CapHosts grant. It never
// touches anything but the profile store and t.HostsFile's managed block.
func ExecuteJob(t Target, j Job) Result {
	if err := j.Validate(); err != nil {
		return Result{Error: err.Error()}
	}
	if j.IsHosts() && !HasCap(t.Caps, CapHosts) {
		return Result{Error: "hosts editing was not granted to this parent (it needs an invite made with --allow-hosts)", Code: CodeNotGranted}
	}
	if j.Mutates() && t.Role != RoleReadWrite {
		return Result{Error: "this child is read-only for its parent"}
	}
	if j.Op == OpHostsApplyProfile || j.Op == OpHostsUnapply {
		return executeApplyProfile(t, j)
	}
	if j.IsHosts() {
		return executeHosts(t.HostsFile, j)
	}
	store := t.Store
	if store == nil {
		return Result{Error: "no profile store"}
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
		if err := store.Save(profile.Profile{Name: a.Profile, Vars: vars, Hosts: a.Hosts}); err != nil {
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

// executeHosts runs a validated, granted hosts job on the child's own file
// through internal/hosts (the same code as `envmgr hosts`). Critical names
// are always refused remotely (no --force over the network).
// hostsFail maps a hosts error to a structured result.
func hostsFail(err error) Result {
	var pe *hosts.PermissionError
	var ce *hosts.CriticalNameError
	switch {
	case errors.As(err, &ce):
		return Result{Error: fmt.Sprintf("refusing to shadow the critical name %q: critical names (localhost, broadcasthost, ip6-*) cannot be set from a parent", ce.Name)}
	case errors.As(err, &pe):
		return Result{Code: CodePermissionDenied, Error: fmt.Sprintf(
			"the child cannot write its hosts file %s: permission denied; run `envmgr agent run` on the child from an elevated shell (Windows) or with sudo (Linux/macOS)", pe.Path)}
	case errors.Is(err, profile.ErrNotFound):
		return Result{Error: "profile not found on the child", NotFound: true}
	case errors.Is(err, hosts.ErrNotFound), errors.Is(err, hosts.ErrGroupNotFound):
		return Result{Error: err.Error(), NotFound: true}
	}
	return Result{Error: err.Error()}
}

// executeApplyProfile applies (or removes) a profile's hosts on the child
// with internal/hostsapply, the same code as `envmgr hosts apply`: the
// entries come from the child's own profile store, the file is the child's
// own hosts file, and the applied state goes to the child's envmgr home.
func executeApplyProfile(t Target, j Job) Result {
	if t.HostsFile == "" {
		return Result{Error: "this child has no hosts file configured"}
	}
	if t.Store == nil {
		return Result{Error: "no profile store"}
	}
	a := j.Args
	if j.Op == OpHostsApplyProfile {
		ch, err := hostsapply.Apply(t.Store, t.HostsFile, a.Profile, a.DryRun)
		if err != nil {
			return hostsFail(err)
		}
		return Result{OK: true, Hosts: &HostsResult{Path: ch.Path, Changed: ch.Changed, Diff: ch.Diff, Backup: ch.Backup, Warning: ch.Warning, Applied: []string{a.Profile}}}
	}
	ch, was, err := hostsapply.Unapply(t.Store.Dir, t.HostsFile, a.DryRun)
	if err != nil {
		return hostsFail(err)
	}
	return Result{OK: true, Hosts: &HostsResult{Path: ch.Path, Changed: ch.Changed, Diff: ch.Diff, Backup: ch.Backup, Warning: ch.Warning, Applied: was}}
}

func executeHosts(path string, j Job) Result {
	if path == "" {
		return Result{Error: "this child has no hosts file configured"}
	}
	f := hosts.NewFile(path)
	a := j.Args
	fail := hostsFail
	if j.Op == OpHostsList {
		doc, err := f.Read()
		if err != nil {
			return fail(err)
		}
		return Result{OK: true, Hosts: &HostsResult{Path: path, Groups: doc.Groups}}
	}
	var fn func(*hosts.Doc) (bool, error)
	switch j.Op {
	case OpHostsAdd:
		fn = func(d *hosts.Doc) (bool, error) { return d.Add(a.IP, a.Hostnames, a.Group, false) }
	case OpHostsRemove:
		fn = func(d *hosts.Doc) (bool, error) { return d.Remove(a.Hostnames[0], a.Group) }
	case OpHostsGroup:
		fn = func(d *hosts.Doc) (bool, error) { return d.SetGroupEnabled(a.Group, *a.Enable) }
	default:
		return Result{Error: fmt.Sprintf("unknown operation %q", j.Op)}
	}
	ch, err := f.Update(a.DryRun, fn)
	if err != nil {
		return fail(err)
	}
	return Result{OK: true, Hosts: &HostsResult{Path: ch.Path, Changed: ch.Changed, Diff: ch.Diff, Backup: ch.Backup, Warning: ch.Warning}}
}

// MaskedValue is shown instead of a secret value. It does not reveal length.
const MaskedValue = profile.MaskedValue

// WriteProfile prints a profile like `envmgr profile show`: sorted KEY=value
// lines (values masked unless reveal) and its hosts entries.
func WriteProfile(w io.Writer, p profile.Profile, reveal bool) error {
	return profile.Write(w, p, reveal, profile.SectionAll)
}

func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }
