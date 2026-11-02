package set

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
	name, key, value := args[0], args[1], args[2]
	if !profile.ValidName(name) {
		return fmt.Errorf("invalid profile name %q (letters, digits, '_', '.', '-'; must start with a letter or digit)", name)
	}
	if !profile.ValidVar(key) {
		return fmt.Errorf("invalid variable name %q (letters, digits and '_'; must not start with a digit)", key)
	}
	if shell.Reserved(key) {
		return fmt.Errorf("variable %q uses the reserved ENVMGR_ prefix", key)
	}
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	if err := store.SetVar(name, key, value); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: set %s in profile %q\n", key, name)
	if os.Getenv(shell.EnvProfile) == name {
		fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: profile %q is active; run \"envmgr use %s\" again to apply\n", name, name)
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "set <profile> <VAR> <value>",
		Short: "Set a variable in a profile (creates the profile if missing)",
		Long: `Set the variable VAR to value in a profile, creating the profile when it does
not exist. Profiles are stored as JSON files (mode 0600) under $ENVMGR_HOME
(default: <user config dir>/envmgr).

Variable names must match [A-Za-z_][A-Za-z0-9_]* and must not start with the
reserved ENVMGR_ prefix. A value starting with "-" is accepted as is.`,
		Args:          cobra.ExactArgs(3),
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
	// Everything after the profile name is positional, so values like "-x" work.
	c.cmd.Flags().SetInterspersed(false)
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
