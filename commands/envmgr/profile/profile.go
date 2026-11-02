package profile

import (
	deletecmd "github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/profile/delete"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/profile/show"
	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/types"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) {
	helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr profile"})
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "profile",
		Short: "Inspect or delete stored profiles",
		Long: `Inspect or delete stored profiles. Use "envmgr set" / "envmgr unset" to edit
a profile's variables and "envmgr list" to list profiles.`,
		Args: cobra.NoArgs,
		Run:  c.runCommand,
	}
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

func (c *commandDefinition) defineSubCommands() {
	show.CreateCommand(c.context, c.cmd)
	deletecmd.CreateCommand(c.context, c.cmd)
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
