// Package hook implements `envmgr hook`: print (or install in the shell's
// profile file) the init code that makes a shell follow the machine-wide
// profile selection.
package hook

import (
	"fmt"
	"io"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	ihook "github.com/dev-vinicius-andrade/environment-manager/internal/hook"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	install       bool
	uninstall     bool
	dryRun        bool
}

// Overridable for tests.
var (
	currentParams = ihook.CurrentParams
	currentEnv    = ihook.CurrentEnv
)

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	if c.install && c.uninstall {
		return fmt.Errorf("--install and --uninstall are mutually exclusive")
	}
	if c.dryRun && !c.install && !c.uninstall {
		return fmt.Errorf("--dry-run needs --install or --uninstall")
	}
	cmd.SilenceUsage = true
	kind, err := shell.Parse(args[0])
	if err != nil {
		return err
	}
	if kind == shell.Cmd {
		return ihook.ErrCmdUnsupported
	}
	params, err := currentParams()
	if err != nil {
		return err
	}
	if !c.install && !c.uninstall {
		script, err := ihook.Script(kind, params)
		if err != nil {
			return err
		}
		_, err = io.WriteString(cmd.OutOrStdout(), script)
		return err
	}
	env, err := currentEnv()
	if err != nil {
		return err
	}
	paths, err := ihook.ProfilePaths(kind, env)
	if err != nil {
		return err
	}
	block, err := ihook.Block(kind, params.Exe, env.GOOS)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, p := range paths {
		cur, exists, err := ihook.ReadFile(p)
		if err != nil {
			return err
		}
		var ch ihook.Change
		if c.install {
			ch = ihook.PlanInstall(p, cur, exists, block)
		} else {
			ch = ihook.PlanUninstall(p, cur, exists)
		}
		status := describe(ch, c.install)
		if c.dryRun {
			fmt.Fprintf(out, "%s (dry run): %s\n", status, p)
			if c.install && ch.Changed {
				fmt.Fprint(out, block)
			}
			continue
		}
		if err := ihook.Apply(ch); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		fmt.Fprintf(out, "%s: %s\n", status, p)
	}
	if c.install && !c.dryRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "envmgr: open a new shell (or reload its profile) to start following the selection")
	}
	return nil
}

func describe(ch ihook.Change, install bool) string {
	switch {
	case install && !ch.Changed:
		return "already installed"
	case install && ch.Had:
		return "updated"
	case install && !ch.Exists:
		return "created"
	case install:
		return "installed"
	case !ch.Had:
		return "not installed"
	}
	return "removed"
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "hook <powershell|bash|zsh|fish>",
		Short: "Print or install the shell hook that follows the machine-wide selection",
		Long: `Print the init code that makes a shell follow the machine-wide profile
selection (the tray icon / "envmgr select"). On every prompt it checks
selected.json cheaply inside the shell and runs "envmgr sync" only when it
changed. Pinned shells ("envmgr use", TUI activation) ignore the selection
until "envmgr follow". Nothing is printed when nothing changes; sourcing it
twice is harmless; the existing prompt is kept.

Add it to your shell's profile, or let --install do it:

  PowerShell:  envmgr hook powershell | Out-String | Invoke-Expression   (in $PROFILE)
  bash:        eval "$(envmgr hook bash)"                                (in ~/.bashrc)
  zsh:         eval "$(envmgr hook zsh)"                                 (in ~/.zshrc)
  fish:        envmgr hook fish | source                                 (in config.fish)

--install appends a marked block to the profile file (creating it if missing;
running it again only refreshes the block); for PowerShell on Windows both the
Windows PowerShell 5.1 and the PowerShell 7 profiles. --uninstall removes only
that block. --dry-run shows what would change. The paths are printed.

cmd has no prompt hook, so it cannot follow the selection automatically.`,
		Args:          cobra.ExactArgs(1),
		ValidArgs:     []string{"powershell", "bash", "zsh", "fish"},
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
	f := c.cmd.Flags()
	f.BoolVar(&c.install, "install", false, "add the hook to the shell's profile file (idempotent)")
	f.BoolVar(&c.uninstall, "uninstall", false, "remove the hook block from the shell's profile file")
	f.BoolVar(&c.dryRun, "dry-run", false, "with --install/--uninstall: show what would change without writing")
}

// CreateCommand builds `envmgr hook` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
