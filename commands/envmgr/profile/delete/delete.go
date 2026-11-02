package delete

import (
	"errors"
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
	name := args[0]
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	if err := store.Delete(name); errors.Is(err, profile.ErrNotFound) {
		return fmt.Errorf("profile %q not found", name)
	} else if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: deleted profile %q\n", name)
	if os.Getenv(shell.EnvProfile) == name {
		fmt.Fprintln(cmd.ErrOrStderr(), "envmgr: it is still active in this shell; \"envmgr deactivate\" restores the previous values")
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile",
		Long: `Delete a stored profile. Shells where it is active keep their variables until
"envmgr deactivate" (which still works: the state lives in the shell).`,
		Args:          cobra.ExactArgs(1),
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
