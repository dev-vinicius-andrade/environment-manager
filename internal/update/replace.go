package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TargetPath returns the real file to replace: the running executable with
// symlinks resolved, so an install through a link (for example
// ~/.local/bin/envmgr -> /opt/envmgr/envmgr) updates the file and keeps the link.
func TargetPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// CleanupOld removes the .old file a previous Windows update left behind.
func CleanupOld(target string) { _ = os.Remove(target + ".old") }

// SelfTest runs the new binary's `version --short` and checks it reports want:
// it catches a wrong architecture or a corrupt file before anything is replaced.
func SelfTest(newPath, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(newPath, 0o755); err != nil {
			return err
		}
	}
	out, err := exec.CommandContext(ctx, newPath, "version", "--short").Output()
	if err != nil {
		return fmt.Errorf("the downloaded binary does not run: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		return fmt.Errorf("the downloaded binary reports version %q, expected %q", got, want)
	}
	return nil
}

// Replace puts newPath in place of target. The new file takes target's mode.
// On unix it renames over the target (atomic, fine while the old binary runs);
// on Windows a running exe cannot be overwritten but can be renamed, so the old
// one is moved to target+".old" first and moved back if the swap fails.
func Replace(target, newPath string) error {
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm() | 0o111
	}
	if err := os.Chmod(newPath, mode); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Rename(newPath, target); err != nil {
			return permHint(target, err)
		}
		return nil
	}
	old := target + ".old"
	_ = os.Remove(old)
	if err := os.Rename(target, old); err != nil {
		return permHint(target, err)
	}
	if err := os.Rename(newPath, target); err != nil {
		if rerr := os.Rename(old, target); rerr != nil {
			return fmt.Errorf("replacing %s failed (%v) and the old binary could not be restored (%v): it is at %s", target, err, rerr, old)
		}
		return permHint(target, err)
	}
	return nil
}

func permHint(target string, err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("cannot write %s: %w\nreinstall to a directory you own, or rerun from a shell that may write there (envmgr never elevates itself)", target, err)
	}
	return err
}
