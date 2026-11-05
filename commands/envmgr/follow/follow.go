// Package follow implements `envmgr follow`: unpin the current shell and apply
// the machine-wide selection.
package follow

import (
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/synccmd"
	"github.com/dev-vinicius-andrade/environment-manager/helpers"
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

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "follow",
		Short: "Print shell code that makes this shell follow the machine-wide selection again",
		Long: `Print shell code that clears this shell's pin (ENVMGR_PINNED, set by
"envmgr use" and the TUI) and applies the machine-wide selection (tray icon /
"envmgr select") right away: activates the selected profile, deactivates when
nothing is selected, or only clears the pin when the shell already has it.

  bash/zsh:    eval "$(envmgr follow)"
  fish:        envmgr follow --shell fish | source
  PowerShell:  envmgr follow --shell powershell | Invoke-Expression
  cmd:         envmgr follow --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"

Later selection changes reach the shell only if it runs the hook (envmgr hook).`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return synccmd.Run(cmd, c.shellName, true)
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

// CreateCommand builds `envmgr follow` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
