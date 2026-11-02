package show

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
	reveal        bool
	section       string
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if err := profile.CheckSection(c.section); err != nil {
		return err
	}
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
	stderr := cmd.ErrOrStderr()
	switch c.section {
	case profile.SectionVars:
		if len(p.Vars) == 0 {
			fmt.Fprintf(stderr, "envmgr: profile %q has no variables\n", p.Name)
		}
	case profile.SectionHosts:
		if len(p.Hosts) == 0 {
			fmt.Fprintf(stderr, "envmgr: profile %q has no hosts entries\n", p.Name)
		}
	default:
		if len(p.Vars) == 0 && len(p.Hosts) == 0 {
			fmt.Fprintf(stderr, "envmgr: profile %q has no variables and no hosts entries\n", p.Name)
		} else if len(p.Vars) == 0 {
			fmt.Fprintf(stderr, "envmgr: profile %q has no variables\n", p.Name)
		}
	}
	return profile.Write(cmd.OutOrStdout(), p, c.reveal, c.section)
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a profile's variables (masked unless --reveal) and hosts entries",
		Long: `Show a profile: its variables as NAME=value lines, sorted by name, and its
hosts entries as "IP NAME... [# comment]" lines. Values are masked because
profiles often hold secrets; pass --reveal to print them. Hosts entries are not
secret and are always shown.

When the profile has hosts entries, "# variables" and "# hosts" headers
separate the parts; --section vars|hosts prints only one part, without header.`,
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
	c.cmd.Flags().StringVar(&c.section, "section", "", "print only one part: vars or hosts")
	_ = c.cmd.RegisterFlagCompletionFunc("section", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{profile.SectionVars, profile.SectionHosts}, cobra.ShellCompDirectiveNoFileComp
	})
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
