package show

import (
	"errors"
	"fmt"
	"sort"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// mask replaces every value unless --reveal is given; it does not leak length.
const mask = "********"

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	reveal        bool
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	p, err := store.Get(args[0])
	if errors.Is(err, profile.ErrNotFound) {
		return fmt.Errorf("profile %q not found", args[0])
	}
	if err != nil {
		return err
	}
	if len(p.Vars) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: profile %q has no variables\n", p.Name)
		return nil
	}
	keys := make([]string, 0, len(p.Vars))
	for k := range p.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := mask
		if c.reveal {
			v = p.Vars[k]
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s=%s\n", k, v)
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a profile's variables (values masked unless --reveal)",
		Long: `Show a profile's variables as NAME=value lines, sorted by name. Values are
masked because profiles often hold secrets; pass --reveal to print them.`,
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
	c.cmd.Flags().BoolVar(&c.reveal, "reveal", false, "print the values instead of masking them")
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
