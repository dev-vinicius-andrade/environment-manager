package dotfiles

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Clone runs `git clone source dest`; dest must be missing or an empty directory.
func Clone(source, dest string, out io.Writer) error {
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", dest)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return git(out, "clone", "--", source, dest)
}

// Pull fast-forwards the repository at dir; it never merges or rewrites.
func Pull(dir string, out io.Writer) error {
	return git(out, "-C", dir, "pull", "--ff-only")
}

func git(out io.Writer, args ...string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed or not in PATH")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}
