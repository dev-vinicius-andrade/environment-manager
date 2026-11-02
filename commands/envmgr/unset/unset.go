package unset

import (
	"errors"
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
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
	name, key := args[0], args[1]
	if !profile.ValidVar(key) {
		return fmt.Errorf("invalid variable name %q", key)
	}
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	p, err := store.Get(name)
	if errors.Is(err, profile.ErrNotFound) {
		return fmt.Errorf("profile %q not found", name)
	}
	if err != nil {
		return err
	}
	if _, ok := p.Vars[key]; !ok {
		fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: %s is not set in profile %q\n", key, name)
		return nil
	}
	if err := store.UnsetVar(name, key); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: removed %s from profile %q\n", key, name)
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "unset <profile> <VAR>",
		Short: "Remove a variable from a profile",
		Long: `Remove the variable VAR from a profile. The profile itself is kept (use
"envmgr profile delete" to remove it). Does not change the current shell; run
"envmgr use <profile>" again to apply the change to an active profile.`,
		Args:          cobra.ExactArgs(2),
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
