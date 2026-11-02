package deactivate

import (
	"fmt"
	"io"

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

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	kind, err := shell.Resolve(c.shellName)
	if err != nil {
		return err
	}
	st := shell.ReadState(shell.OSLookup)
	if !st.Active() {
		fmt.Fprintln(cmd.ErrOrStderr(), "envmgr: no active profile")
		return nil
	}
	code, err := shell.Deactivate(kind, shell.OSLookup)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(cmd.OutOrStdout(), code); err != nil {
		return err
	}
	if st.Profile != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: deactivated profile %q\n", st.Profile)
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "deactivate",
		Short: "Print shell code that deactivates the active profile",
		Long: `Print shell code that deactivates the active profile, restoring every
variable it changed to its original value (or unsetting it when it did not
exist before) and clearing the ENVMGR_* state variables.

  bash/zsh:    eval "$(envmgr deactivate)"
  fish:        envmgr deactivate --shell fish | source
  PowerShell:  envmgr deactivate --shell powershell | Invoke-Expression
  cmd:         envmgr deactivate --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"

Prints nothing when no profile is active.`,
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
	c.cmd.Flags().StringVar(&c.shellName, "shell", "", shell.FlagUsage)
	_ = c.cmd.RegisterFlagCompletionFunc("shell", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return shell.Names, cobra.ShellCompDirectiveNoFileComp
	})
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
