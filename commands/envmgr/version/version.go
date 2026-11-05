// Package version implements `envmgr version`.
package version

import (
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/buildinfo"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	short         bool
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) {
	if c.short {
		fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Version)
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), buildinfo.Summary())
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "version",
		Short: "Print the envmgr version",
		Long: `Print the version, commit, build date, Go version and platform of this binary.
A build that was not made by the release workflow reports "dev".
"envmgr update --check" tells whether a newer release exists.`,
		Args: cobra.NoArgs,
		Run:  c.runCommand,
	}
	command.Flags().BoolVar(&c.short, "short", false, "print only the version number")
	c.cmd = command
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

// CreateCommand builds `envmgr version` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{context: context, parentCommand: parentCommand}
	return command.createCommandDefinition()
}
