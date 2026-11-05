package dotfiles

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// State is how one manifest entry looks on this machine.
type State string

const (
	StateOK        State = "ok"         // dest is a link to src
	StateCopied    State = "copied"     // dest is a copy envmgr made
	StateMissing   State = "missing"    // dest does not exist
	StateWrongLink State = "wrong-link" // dest is a link to something else
	StateConflict  State = "conflict"   // dest is a real file or directory
	StateSrcGone   State = "src-missing"
	StateSkipped   State = "skipped" // the entry is for another OS
)

// Item is one manifest entry resolved for this machine.
type Item struct {
	Link   Link
	Src    string // absolute path inside the repository
	Dest   string // absolute destination
	State  State
	Detail string
}

// BuildPlan resolves every manifest entry against the disk.
func BuildPlan(dir string, m Manifest, goos, home string, env func(string) string, applied Applied) ([]Item, error) {
	items := make([]Item, 0, len(m.Links))
	for _, l := range m.Links {
		it := Item{Link: l, Src: filepath.Join(dir, filepath.FromSlash(l.Src))}
		if !l.appliesTo(goos) {
			it.State = StateSkipped
			it.Dest = l.Dest
			it.Detail = "only for " + fmt.Sprint(l.OS)
			items = append(items, it)
			continue
		}
		d, err := ExpandDest(l.Dest, home, env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", l.Src, err)
		}
		it.Dest = d
		it.State, it.Detail = inspect(it.Src, d, applied)
		items = append(items, it)
	}
	return items, nil
}

func inspect(src, dest string, applied Applied) (State, string) {
	if _, err := os.Lstat(src); err != nil {
		return StateSrcGone, "not in the repository"
	}
	fi, err := os.Lstat(dest)
	if errors.Is(err, os.ErrNotExist) {
		return StateMissing, ""
	}
	if err != nil {
		return StateConflict, err.Error()
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		if linksTo(dest, src) {
			return StateOK, ""
		}
		t, _ := os.Readlink(dest)
		return StateWrongLink, "points to " + t
	}
	if i := applied.find(dest); i >= 0 && applied.Links[i].Mode == "copy" {
		return StateCopied, ""
	}
	return StateConflict, "exists and was not created by envmgr"
}

// linksTo reports whether the symlink at dest resolves to src.
func linksTo(dest, src string) bool {
	a, errA := filepath.EvalSymlinks(dest)
	b, errB := filepath.EvalSymlinks(src)
	if errA == nil && errB == nil {
		return samePath(a, b)
	}
	t, err := os.Readlink(dest)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(t) {
		t = filepath.Join(filepath.Dir(dest), t)
	}
	return samePath(t, src)
}

// Options change how Link and Unlink act.
type Options struct {
	DryRun bool
	Copy   bool // copy instead of symlink
	Now    func() time.Time
}

// Action is what Link or Unlink did (or would do) for one entry.
type Action struct {
	Item   Item
	Op     string // linked, copied, ok, skipped, unlinked, restored, kept ...
	Backup string
	Note   string // why an entry was left alone (not a failure)
	Err    error
}

// Apply makes every applicable entry point at the repository. Whatever is in the
// way is moved to a timestamped backup first. Errors are per entry; the rest go on.
func Apply(items []Item, applied *Applied, opt Options) []Action {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	acts := make([]Action, 0, len(items))
	for _, it := range items {
		a := Action{Item: it}
		switch it.State {
		case StateSkipped:
			a.Op = "skipped"
		case StateSrcGone:
			a.Op = "failed"
			a.Err = fmt.Errorf("%s is not in the repository", it.Link.Src)
		case StateOK, StateCopied:
			a.Op = "ok"
		default:
			a.Op, a.Backup, a.Err = place(it, opt, now(), applied)
		}
		acts = append(acts, a)
	}
	return acts
}

func place(it Item, opt Options, now time.Time, applied *Applied) (op, backup string, err error) {
	mode := "symlink"
	op = "linked"
	if opt.Copy {
		mode, op = "copy", "copied"
	}
	if it.State == StateWrongLink || it.State == StateConflict {
		backup = backupName(it.Dest, now)
	}
	if opt.DryRun {
		return "would-" + op, backup, nil
	}
	if backup != "" {
		if err := os.Rename(it.Dest, backup); err != nil {
			return "failed", "", fmt.Errorf("backing up %s: %w", it.Dest, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(it.Dest), 0o755); err != nil {
		return "failed", backup, err
	}
	if opt.Copy {
		err = copyTree(it.Src, it.Dest)
	} else {
		err = os.Symlink(it.Src, it.Dest)
		if err != nil && runtime.GOOS == "windows" {
			err = fmt.Errorf("%w (Windows needs Developer Mode or an elevated shell to create symlinks; or use --copy)", err)
		}
	}
	if err != nil {
		if backup != "" {
			_ = os.Rename(backup, it.Dest) // put it back
			backup = ""
		}
		return "failed", backup, err
	}
	applied.set(AppliedLink{Src: it.Src, Dest: it.Dest, Mode: mode, Backup: backup})
	return op, backup, nil
}

func backupName(dest string, now time.Time) string {
	base := dest + ".envmgr-bak-" + now.Format("20060102-150405")
	name := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// Unlink removes what envmgr created (and still matches the record) and puts
// back the backup unless restore is false. Copies are kept unless force is set,
// since they may have been edited.
func Unlink(applied *Applied, restore, force bool, opt Options) []Action {
	var acts []Action
	for _, rec := range append([]AppliedLink(nil), applied.Links...) {
		a := Action{Item: Item{Src: rec.Src, Dest: rec.Dest}, Backup: rec.Backup}
		fi, err := os.Lstat(rec.Dest)
		switch {
		case errors.Is(err, os.ErrNotExist):
			a.Op = "gone"
		case err != nil:
			a.Op, a.Err = "kept", err
		case rec.Mode == "symlink" && fi.Mode()&os.ModeSymlink == 0:
			a.Op, a.Note = "kept", "no longer a link; leaving it alone"
		case rec.Mode == "symlink" && !linksTo(rec.Dest, rec.Src):
			a.Op, a.Note = "kept", "now points elsewhere; leaving it alone"
		case rec.Mode == "copy" && !force:
			a.Op, a.Note = "kept", "is a copy and may have been edited; use --force to remove it"
		default:
			a.Op = "unlinked"
			if !opt.DryRun {
				if err := os.RemoveAll(rec.Dest); err != nil {
					a.Op, a.Err = "kept", err
				}
			}
		}
		if a.Err != nil || a.Op == "kept" {
			acts = append(acts, a)
			continue
		}
		if restore && rec.Backup != "" {
			if _, err := os.Lstat(rec.Backup); err == nil {
				if opt.DryRun {
					a.Op += "+would-restore"
				} else if err := os.Rename(rec.Backup, rec.Dest); err != nil {
					a.Err = fmt.Errorf("restoring %s: %w", rec.Backup, err)
				} else {
					a.Op += "+restored"
				}
			}
		}
		if !opt.DryRun && a.Err == nil {
			applied.drop(rec.Dest)
		}
		acts = append(acts, a)
	}
	return acts
}

// copyTree copies a file or a directory tree.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, target)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		fi, err := in.Stat()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
