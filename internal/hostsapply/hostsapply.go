// Package hostsapply applies the hosts entries of a profile to this machine's
// hosts file and records which profile is applied.
//
// The entries go to the envmgr managed block as one group named
// "profile:<name>" (hosts.ProfileGroup). Applying profile B while A is
// applied replaces A's group by B's in one atomic edit (backup first, same
// lock and safety model as `envmgr hosts`), so two profiles are never applied
// at once. The state is machine-wide: <ENVMGR_HOME>/hosts-applied.json.
package hostsapply

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/selected"
)

const (
	// StateFileName records the applied profile inside the envmgr home.
	StateFileName = "hosts-applied.json"
	// hintFileName remembers the selection a sync hint was shown for.
	hintFileName = "hosts-hint.json"
)

// State is the content of hosts-applied.json.
type State struct {
	Profile   string    `json:"profile"`        // "" = none applied
	File      string    `json:"file,omitempty"` // hosts file it was applied to
	AppliedAt time.Time `json:"appliedAt"`
}

// StatePath returns the state file of an envmgr home.
func StatePath(home string) string { return filepath.Join(home, StateFileName) }

// ReadState returns the recorded state; a missing file means none applied.
func ReadState(home string) (State, error) {
	p := StatePath(home)
	b, err := selected.ReadFileRetry(p)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, fmt.Errorf("%s: %w", p, err)
	}
	if st.Profile != "" && !profile.ValidName(st.Profile) {
		return State{}, fmt.Errorf("%s: invalid profile name %q", p, st.Profile)
	}
	return st, nil
}

func writeState(home string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return selected.WriteFileAtomic(StatePath(home), append(b, '\n'))
}

// Scope says which parts of a profile a command applies.
type Scope struct {
	Env, Hosts bool
}

// ParseScope turns the --hosts and --only flags into a scope. Default: env
// only; --hosts: env and hosts; --only env|hosts|all: exactly that.
func ParseScope(hostsFlag bool, only string) (Scope, error) {
	if hostsFlag && only != "" {
		return Scope{}, errors.New("use either --hosts or --only, not both")
	}
	switch only {
	case "":
		return Scope{Env: true, Hosts: hostsFlag}, nil
	case "env":
		return Scope{Env: true}, nil
	case "hosts":
		return Scope{Hosts: true}, nil
	case "all":
		return Scope{Env: true, Hosts: true}, nil
	}
	return Scope{}, fmt.Errorf("invalid --only %q: use env, hosts or all", only)
}

// Entries converts a profile's hosts entries for the hosts file (the comment
// stays in the profile).
func Entries(p profile.Profile) []hosts.Entry {
	out := make([]hosts.Entry, 0, len(p.Hosts))
	for _, e := range p.Hosts {
		out = append(out, hosts.Entry{IP: e.IP, Hostnames: append([]string(nil), e.Names...)})
	}
	return out
}

// Apply writes the hosts entries of the profile name (read from store) to
// the hosts file at hostsPath, replacing any previously applied profile, and
// records it in <store.Dir>/hosts-applied.json. With dryRun nothing is
// written: the returned change carries the diff. An unknown profile gives an
// error wrapping profile.ErrNotFound; a permission problem a
// *hosts.PermissionError (envmgr never elevates itself).
func Apply(store *profile.Store, hostsPath, name string, dryRun bool) (hosts.Change, error) {
	p, err := store.Get(name)
	if errors.Is(err, profile.ErrNotFound) {
		return hosts.Change{}, fmt.Errorf("profile %q: %w", name, profile.ErrNotFound)
	}
	if err != nil {
		return hosts.Change{}, err
	}
	entries := Entries(p)
	ch, err := hosts.NewFile(hostsPath).Update(dryRun, func(d *hosts.Doc) (bool, error) {
		return d.ApplyProfile(p.Name, entries)
	})
	if err != nil || dryRun {
		return ch, err
	}
	if err := writeState(store.Dir, State{Profile: p.Name, File: ch.Path, AppliedAt: time.Now().UTC()}); err != nil {
		return ch, fmt.Errorf("hosts applied, but recording it failed: %w", err)
	}
	return ch, nil
}

// Unapply removes every profile group from the hosts file at hostsPath and
// clears the recorded state of home. It returns the profiles that were
// applied in the file (usually one, none when nothing was applied).
func Unapply(home, hostsPath string, dryRun bool) (hosts.Change, []string, error) {
	var was []string
	ch, err := hosts.NewFile(hostsPath).Update(dryRun, func(d *hosts.Doc) (bool, error) {
		was = d.AppliedProfiles()
		return d.RemoveProfileGroups(), nil
	})
	if err != nil || dryRun {
		return ch, was, err
	}
	if st, _ := ReadState(home); st.Profile != "" || len(was) > 0 {
		if err := writeState(home, State{AppliedAt: time.Now().UTC()}); err != nil {
			return ch, was, fmt.Errorf("hosts removed, but recording it failed: %w", err)
		}
	}
	return ch, was, nil
}

// SyncHint returns the one-line stderr hint `envmgr sync` prints when the
// machine-wide selection asks for a profile's hosts that are not applied:
// sync never writes the hosts file (it needs administrator rights). The hint
// is returned once per selection change (remembered in
// <home>/hosts-hint.json), so the hook does not repeat it on every prompt.
func SyncHint(home string, sel selected.Selection) string {
	name := sel.HostsProfile()
	if name == "" {
		return ""
	}
	if st, err := ReadState(home); err == nil && st.Profile == name {
		return ""
	}
	key := name + "@" + sel.UpdatedAt.UTC().Format(time.RFC3339Nano)
	p := filepath.Join(home, hintFileName)
	var seen struct {
		Shown string `json:"shown"`
	}
	if b, err := selected.ReadFileRetry(p); err == nil && json.Unmarshal(b, &seen) == nil && seen.Shown == key {
		return ""
	}
	seen.Shown = key
	if b, err := json.Marshal(seen); err == nil {
		_ = selected.WriteFileAtomic(p, append(b, '\n'))
	}
	return fmt.Sprintf("envmgr: profile %q is selected with its hosts, which are not applied; run \"envmgr hosts apply %s\" in an elevated shell (Windows: Run as administrator; Linux/macOS: sudo)", name, name)
}

// Report prints the outcome of an apply/unapply on w (the stderr of use and
// deactivate, whose stdout carries only shell code), including the diff of a
// dry run. what names the change ("hosts of profile \"dev\"").
func Report(w io.Writer, ch hosts.Change, dryRun bool, what string) {
	switch {
	case !ch.Changed:
		fmt.Fprintf(w, "envmgr: %s: no change, %s is already up to date\n", what, ch.Path)
	case dryRun:
		fmt.Fprint(w, ch.Diff)
		fmt.Fprintf(w, "envmgr: %s: dry run, %s was not written\n", what, ch.Path)
	default:
		fmt.Fprintf(w, "envmgr: %s: updated %s (backup: %s)\n", what, ch.Path, ch.Backup)
	}
	if ch.Warning != "" {
		fmt.Fprintln(w, "envmgr: warning:", ch.Warning)
	}
}
