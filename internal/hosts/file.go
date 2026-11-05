package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultKeep is how many backups are kept next to the hosts file.
	DefaultKeep = 10
	// BackupInfix separates the hosts file name from the backup timestamp.
	BackupInfix = ".envmgr-bak-"
	lockSuffix  = ".envmgr.lock"
	backupStamp = "20060102T150405.000000000Z"
	maxFileSize = 64 << 20

	lockStaleAfter = 30 * time.Second
)

// ErrLocked means another envmgr process holds the hosts lock.
var ErrLocked = errors.New("another envmgr process is editing the hosts file")

// PermissionError means the hosts file (or its directory) is not writable by
// this process. envmgr never elevates itself; the user must.
type PermissionError struct {
	Op   string
	Path string
	Err  error
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("cannot %s %s: permission denied. Editing the hosts file needs administrator rights: "+
		"run envmgr from an elevated shell (Windows: \"Run as administrator\") or with sudo (Linux/macOS). "+
		"envmgr never elevates itself", e.Op, e.Path)
}

func (e *PermissionError) Unwrap() error { return e.Err }

func wrapPerm(op, path string, err error) error {
	if err != nil && errors.Is(err, fs.ErrPermission) {
		return &PermissionError{Op: op, Path: path, Err: err}
	}
	return err
}

// File is a hosts file on disk.
type File struct {
	Path string
	// Keep is the number of backups kept (default DefaultKeep).
	Keep int
	// LockWait is how long to wait for another envmgr process (default 10s).
	LockWait time.Duration
	// Now is the clock used for backup names (default time.Now).
	Now func() time.Time
}

// NewFile returns the hosts file at path.
func NewFile(path string) *File { return &File{Path: path} }

// Change is the outcome of an update.
type Change struct {
	Path    string // the file that was (or would be) written
	Changed bool
	Diff    string // unified-style diff; set when Changed
	Backup  string // backup written before the change (not on dry runs)
	Warning string // e.g. the non-atomic fallback on Windows
}

func (f *File) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// target resolves symlinks so a link (e.g. /etc/hosts -> elsewhere) is never
// replaced by a regular file.
func (f *File) target() (string, error) {
	if f.Path == "" {
		return "", errors.New("no hosts file path")
	}
	p, err := filepath.EvalSymlinks(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("hosts file %s does not exist (set --file or %s to an existing file)", f.Path, EnvFile)
	}
	if err != nil {
		return "", wrapPerm("read", f.Path, err)
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", wrapPerm("read", p, err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", p)
	}
	return p, nil
}

func readLimited(p string) ([]byte, error) {
	fh, err := os.Open(p)
	if err != nil {
		return nil, wrapPerm("read", p, err)
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, maxFileSize+1))
	if err != nil {
		return nil, wrapPerm("read", p, err)
	}
	if len(b) > maxFileSize {
		return nil, fmt.Errorf("%s is larger than %d MiB; refusing to edit it", p, maxFileSize>>20)
	}
	return b, nil
}

// Read parses the hosts file (no lock: reading never needs one).
func (f *File) Read() (*Doc, error) {
	p, err := f.target()
	if err != nil {
		return nil, err
	}
	raw, err := readLimited(p)
	if err != nil {
		return nil, err
	}
	doc, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return doc, nil
}

// Update applies fn to the parsed file. When fn reports a change, the file is
// backed up and rewritten atomically under the lock; with dryRun nothing is
// written (and no lock is taken), only the diff is returned.
func (f *File) Update(dryRun bool, fn func(*Doc) (bool, error)) (Change, error) {
	p, err := f.target()
	if err != nil {
		return Change{}, err
	}
	ch := Change{Path: p}
	if !dryRun {
		unlock, err := f.lock(p)
		if err != nil {
			return ch, err
		}
		defer unlock()
	}
	raw, err := readLimited(p)
	if err != nil {
		return ch, err
	}
	doc, err := Parse(raw)
	if err != nil {
		return ch, fmt.Errorf("%s: %w", p, err)
	}
	changed, err := fn(doc)
	if err != nil || !changed {
		return ch, err
	}
	out := doc.Render()
	if bytes.Equal(out, raw) {
		return ch, nil
	}
	ch.Changed = true
	ch.Diff = Diff(p, raw, out)
	if dryRun {
		return ch, nil
	}
	if ch.Backup, err = f.backup(p, raw); err != nil {
		return ch, err
	}
	ch.Warning, err = writeReplace(p, out)
	return ch, err
}

// Backup copies the current file to a new timestamped backup (keeping the
// last Keep) and returns its path.
func (f *File) Backup() (string, error) {
	p, err := f.target()
	if err != nil {
		return "", err
	}
	unlock, err := f.lock(p)
	if err != nil {
		return "", err
	}
	defer unlock()
	raw, err := readLimited(p)
	if err != nil {
		return "", err
	}
	return f.backup(p, raw)
}

// Backups lists this file's backups, oldest first.
func (f *File) Backups() ([]string, error) {
	p, err := f.target()
	if err != nil {
		return nil, err
	}
	return listBackups(p)
}

// Restore replaces the file with a backup (the newest when name is ""). name
// is a backup file name from Backups (a path is accepted only if it points to
// one of them). The current content is backed up first, so a restore can be
// undone.
func (f *File) Restore(name string, dryRun bool) (Change, error) {
	p, err := f.target()
	if err != nil {
		return Change{}, err
	}
	ch := Change{Path: p}
	list, err := listBackups(p)
	if err != nil {
		return ch, err
	}
	if len(list) == 0 {
		return ch, fmt.Errorf("no envmgr backups of %s", p)
	}
	src := list[len(list)-1]
	if name != "" {
		src = ""
		for _, b := range list {
			if name == filepath.Base(b) || filepath.Clean(name) == b {
				src = b
			}
		}
		if src == "" {
			return ch, fmt.Errorf("unknown backup %q (list them with `envmgr hosts restore --list`)", name)
		}
	}
	data, err := readLimited(src)
	if err != nil {
		return ch, err
	}
	if !dryRun {
		unlock, err := f.lock(p)
		if err != nil {
			return ch, err
		}
		defer unlock()
	}
	cur, err := readLimited(p)
	if err != nil {
		return ch, err
	}
	if bytes.Equal(cur, data) {
		return ch, nil
	}
	ch.Changed = true
	ch.Diff = Diff(p, cur, data)
	if dryRun {
		return ch, nil
	}
	if ch.Backup, err = f.backup(p, cur); err != nil {
		return ch, err
	}
	ch.Warning, err = writeReplace(p, data)
	return ch, err
}

func (f *File) lock(p string) (func(), error) {
	lp := p + lockSuffix
	wait := f.LockWait
	if wait <= 0 {
		wait = 10 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		fh, err := os.OpenFile(lp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = fh.WriteString(strconv.Itoa(os.Getpid()))
			fh.Close()
			return func() { _ = os.Remove(lp) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, wrapPerm("write", filepath.Dir(lp), err)
		}
		if st, serr := os.Stat(lp); serr == nil && time.Since(st.ModTime()) > lockStaleAfter {
			_ = os.Remove(lp) // holder died
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w (lock %s); if no envmgr hosts command is running, delete the lock file", ErrLocked, lp)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func listBackups(p string) ([]string, error) {
	dir, prefix := filepath.Dir(p), filepath.Base(p)+BackupInfix
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, wrapPerm("read", dir, err)
	}
	var out []string
	for _, e := range ents {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (f *File) backup(p string, raw []byte) (string, error) {
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm() | 0o200 // owner-writable so rotation can delete it
	}
	base := p + BackupInfix + f.now().UTC().Format(backupStamp)
	var name string
	for i := 0; ; i++ {
		name = base
		if i > 0 {
			name = fmt.Sprintf("%s-%02d", base, i)
		}
		fh, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) && i < 99 {
			continue
		}
		if err != nil {
			return "", wrapPerm("write a backup next to", p, err)
		}
		_, werr := fh.Write(raw)
		cerr := fh.Close()
		if werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(name)
			return "", wrapPerm("write a backup next to", p, werr)
		}
		break
	}
	keep := f.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	if list, err := listBackups(p); err == nil && len(list) > keep {
		for _, old := range list[:len(list)-keep] {
			_ = os.Remove(old)
		}
	}
	return name, nil
}

// writeReplace writes data to a temp file in p's directory and renames it over
// p. If the rename fails on Windows (the file is open elsewhere, or replacing
// it is refused), it falls back to rewriting p in place and returns a warning.
func writeReplace(p string, data []byte) (string, error) {
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(p)+".envmgr-*.tmp")
	if err != nil {
		return "", wrapPerm("write", p, err)
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && runtime.GOOS != "windows" {
		err = os.Chmod(tmpName, mode)
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return "", wrapPerm("write", p, err)
	}
	for i := 0; ; i++ {
		err = os.Rename(tmpName, p)
		if err == nil || i >= 5 || runtime.GOOS != "windows" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err == nil {
		return "", nil
	}
	_ = os.Remove(tmpName)
	if runtime.GOOS != "windows" {
		return "", wrapPerm("replace", p, err)
	}
	renameErr := err
	fh, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return "", wrapPerm("write", p, err)
	}
	_, err = fh.Write(data)
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", wrapPerm("write", p, err)
	}
	return fmt.Sprintf("could not replace %s atomically (%v); it was rewritten in place instead (a backup was saved first)", p, renameErr), nil
}
