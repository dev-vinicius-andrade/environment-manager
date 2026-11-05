// Package tray implements `envmgr tray`: the Windows system tray icon from
// which the user picks the machine-wide profile (internal/selected) that
// shells running the hook follow.
package tray

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	itray "github.com/dev-vinicius-andrade/environment-manager/internal/tray"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	foreground    bool
}

// DebugLogPath returns where the tray writes its debug log when
// ENVMGR_TRAY_DEBUG=1: ENVMGR_TRAY_LOG, or <temp>/envmgr-tray/debug.log.
func DebugLogPath() string {
	if p := os.Getenv("ENVMGR_TRAY_LOG"); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "envmgr-tray", "debug.log")
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if runtime.GOOS != "windows" {
		return itray.ErrUnsupported
	}
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	errw := cmd.ErrOrStderr()
	if !c.foreground {
		if itray.Running(store.Dir) {
			fmt.Fprintln(errw, "envmgr: the tray is already running")
			return nil
		}
		pid, err := itray.Launch(exe, []string{"tray", "--foreground"})
		if err != nil {
			return fmt.Errorf("starting the tray: %w", err)
		}
		fmt.Fprintf(errw, "envmgr: tray started in the background (pid %d)\n", pid)
		return nil
	}
	var logw io.Writer
	if os.Getenv("ENVMGR_TRAY_DEBUG") == "1" {
		p := DebugLogPath()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		logw = f
		fmt.Fprintf(errw, "envmgr: tray debug log: %s\n", p)
	}
	err = itray.Run(itray.Options{Store: store, Exe: exe, Log: logw, HostsFile: os.Getenv(hosts.EnvFile)})
	if errors.Is(err, itray.ErrAlreadyRunning) {
		return fmt.Errorf("%w (for %s)", err, store.Dir)
	}
	return err
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "tray",
		Short: "Show a Windows tray icon to pick the machine-wide profile",
		Long: `Show a system tray icon (Windows only) from which you pick the
machine-wide profile. Picking one writes <ENVMGR_HOME>/selected.json (the same
as "envmgr select"); every open shell that runs the hook ("envmgr hook
--install powershell") and is not pinned switches on its next prompt.
Each profile has a submenu: "Environment" and "Hosts" toggle the selected
scopes (the same as "envmgr select <p> --only env|hosts|all"), and
"Apply hosts (asks for administrator)..." runs "envmgr hosts apply <p>" in a
new elevated window after Windows' UAC prompt. "None (deactivate)" clears the
selection.

The tray does not change any process's environment and needs no admin rights;
it never writes the hosts file itself and never elevates silently.
Its menu lists up to 40 profiles (use the TUI for the rest), refreshes every
2 seconds (so CLI changes show up), and has Open TUI, Open profiles folder,
Start with Windows (HKCU Run key) and Quit.

By default it relaunches itself in the background with no console window and
returns; --foreground keeps it attached. Only one tray runs per envmgr home.
Set ENVMGR_TRAY_DEBUG=1 to log its menu state (path printed on start;
ENVMGR_TRAY_LOG overrides it).`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE:          c.runCommand,
	}
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

func (c *commandDefinition) defineSubCommands() {
}

func (c *commandDefinition) defineFlags() {
	c.cmd.Flags().BoolVar(&c.foreground, "foreground", false, "run in this process instead of relaunching in the background")
}

// CreateCommand builds `envmgr tray` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
