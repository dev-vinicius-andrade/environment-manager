package list

import (
	"fmt"
	"os"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	names, err := store.List()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "envmgr: no profiles (create one with: envmgr set <profile> <VAR> <value>)")
		return nil
	}
	active := os.Getenv(shell.EnvProfile)
	for _, n := range names {
		mark := " "
		if n == active {
			mark = "*"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", mark, n)
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List profiles, marking the active one with *",
		Long: `List the stored profiles, one per line. The profile active in the current
shell (from ENVMGR_PROFILE) is marked with "*".`,
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
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
