// Package synccmd implements `envmgr sync` (called by the shell hook) and the
// shared logic of `envmgr follow`.
package synccmd

import (
	"fmt"
	"io"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/selected"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	shellName     string
}

// Run prints on stdout the code that makes the calling shell match the
// machine-wide selection (only when something must change) and a one-line
// message on stderr when it changes something. With follow, the session's pin
// is ignored and cleared. On error stdout stays empty.
func Run(cmd *cobra.Command, shellName string, follow bool) error {
	cmd.SilenceUsage = true
	kind, err := shell.Resolve(shellName)
	if err != nil {
		return err
	}
	s, err := selected.New()
	if err != nil {
		return err
	}
	res, err := s.Sync(kind, shell.OSLookup, follow)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(cmd.OutOrStdout(), res.Code); err != nil {
		return err
	}
	errw := cmd.ErrOrStderr()
	if res.Decision.Note != "" {
		fmt.Fprintln(errw, res.Decision.Note)
	}
	cur := shell.ReadState(shell.OSLookup).Profile
	switch res.Decision.Action {
	case selected.Activate:
		fmt.Fprintf(errw, "envmgr: activated profile %q (machine-wide selection)\n", res.Decision.Profile)
	case selected.Deactivate:
		if res.Decision.Note == "" && cur != "" {
			fmt.Fprintf(errw, "envmgr: deactivated profile %q (selection cleared)\n", cur)
		}
	}
	if follow {
		fmt.Fprintln(errw, "envmgr: this shell follows the machine-wide selection again")
	}
	// The hosts scope is never applied here (it needs administrator rights):
	// hint once per selection change.
	if hint := hostsapply.SyncHint(s.Profiles.Dir, res.Selection); hint != "" {
		fmt.Fprintln(errw, hint)
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "sync",
		Short: "Print shell code that applies the machine-wide selection (used by the hook)",
		Long: `Print shell code that makes the current shell match the machine-wide
profile selection (tray icon / "envmgr select"). The shell hook installed with
"envmgr hook" runs it when the selection changes; you rarely need it directly.

Prints nothing when nothing must change: the shell is pinned (it ran
"envmgr use" or activated from the TUI), or it already has the selected
profile. Otherwise it prints the same code as "use" without pinning, or the
deactivate code when the selection was cleared or the selected profile no
longer exists. Only code goes to stdout; messages go to stderr.

Only the environment scope of the selection is followed. When the selection
also asks for the profile's hosts (envmgr select --hosts) and they are not
applied, a one-line hint is printed on stderr once per selection change: the
hosts file is never written here (it needs an elevated shell / sudo).

  bash/zsh:    eval "$(envmgr sync --shell bash)"
  fish:        envmgr sync --shell fish | source
  PowerShell:  envmgr sync --shell powershell | Invoke-Expression`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(cmd, c.shellName, false)
		},
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
	c.cmd.Flags().StringVar(&c.shellName, "shell", "", shell.FlagUsage)
	_ = c.cmd.RegisterFlagCompletionFunc("shell", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return shell.Names, cobra.ShellCompDirectiveNoFileComp
	})
}

// CreateCommand builds `envmgr sync` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
