package tray

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

func TestDebugLogPath(t *testing.T) {
	t.Setenv("ENVMGR_TRAY_LOG", filepath.Join("x", "y.log"))
	if got := DebugLogPath(); got != filepath.Join("x", "y.log") {
		t.Fatalf("override: %q", got)
	}
	t.Setenv("ENVMGR_TRAY_LOG", "")
	if got := DebugLogPath(); !strings.HasSuffix(got, filepath.Join("envmgr-tray", "debug.log")) {
		t.Fatalf("default: %q", got)
	}
}

func TestUnsupportedOutsideWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tray is supported here; it is exercised by hand (see .claude/docs/tray.md)")
	}
	root := &cobra.Command{Use: "envmgr"}
	CreateCommand(&context.EnvContext{}, root)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"tray"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "only supported on Windows") {
		t.Fatalf("err = %v", err)
	}
}
