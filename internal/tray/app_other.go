//go:build !windows

package tray

// Run is only implemented on Windows.
func Run(Options) error { return ErrUnsupported }

// Launch is only implemented on Windows.
func Launch(exe string, args []string) (int, error) { return 0, ErrUnsupported }

// Running is only implemented on Windows.
func Running(home string) bool { return false }
