// Package selected implements `envmgr selected`, which prints the machine-wide
// profile selection.
package selected

import (
	"fmt"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	sel "github.com/dev-vinicius-andrade/environment-manager/internal/selected"
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
	s, err := sel.New()
	if err != nil {
		return err
	}
	cur, err := s.Get()
	if err != nil {
		return err
	}
	if cur.Profile != "" {
		fmt.Fprintln(cmd.OutOrStdout(), cur.Profile)
		scopes := []string{}
		if cur.Env {
			scopes = append(scopes, "env")
		}
		if cur.Hosts {
			scopes = append(scopes, "hosts")
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "(scopes: %s)\n", strings.Join(scopes, ", "))
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "selected",
		Short: "Print the machine-wide selected profile",
		Long: `Print the machine-wide selected profile (set by the tray icon or
"envmgr select"), or nothing when none is selected. Always exits 0 unless the
selection file cannot be read. The selected scopes (env, hosts) are printed
on stderr.`,
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

// CreateCommand builds `envmgr selected` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
