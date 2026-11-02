// Package parent implements `envmgr parent`: the hub that issues join codes
// and controls the profiles of enrolled child machines.
package parent

import (
	"errors"
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
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
	helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr parent"})
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "parent",
		Short: "Control the profiles of child machines that joined this machine",
		Long: `This machine acts as the parent (hub). Children dial in; they expose no port.

  envmgr parent serve                      run the hub (keep it running)
  envmgr parent invite --name laptop       print a single-use, expiring join command for a child
  envmgr parent agents                     list enrolled children (online or not)
  envmgr parent show laptop dev            operate on a child's profiles
  envmgr parent revoke laptop              cut a child off immediately

Only profile operations exist; nothing can be executed on a child.`,
		Run: c.runCommand,
	}
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

func (c *commandDefinition) defineSubCommands() {
	for _, sub := range []func(*context.EnvContext, *cobra.Command) *cobra.Command{
		createServeCommand,
		createInviteCommand,
		createInvitesCommand,
		createCancelCommand,
		createAgentsCommand,
		createRevokeCommand,
		createProfilesCommand,
		createShowCommand,
		createPushCommand,
		createPullCommand,
		createSetCommand,
		createUnsetCommand,
		createDeleteCommand,
	} {
		sub(c.context, c.cmd)
	}
}

func (c *commandDefinition) defineFlags() {
}

// CreateCommand builds `envmgr parent` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}

// newLeaf builds a subcommand with shared settings (exact args, no usage on errors).
func newLeaf(parent *cobra.Command, use, short string, nargs int, run func(cmd *cobra.Command, args []string) error) *cobra.Command {
	command := &cobra.Command{
		Use:          use,
		Short:        short,
		Args:         cobra.ExactArgs(nargs),
		SilenceUsage: true,
		RunE:         run,
	}
	helpers.CobraHelper.AddCommandToParent(command, parent)
	return command
}

func hubState() (*agent.HubState, error) {
	dir, err := agent.ParentDir()
	if err != nil {
		return nil, err
	}
	return agent.NewHubState(dir), nil
}

func adminClient() (*agent.AdminClient, error) {
	dir, err := agent.ParentDir()
	if err != nil {
		return nil, err
	}
	return agent.NewAdminClient(dir)
}

// relay runs one job on a child through the running hub.
func relay(child string, job agent.Job) (agent.Result, error) {
	a, err := adminClient()
	if err != nil {
		return agent.Result{}, err
	}
	res, err := a.Relay(child, job)
	if err != nil {
		return res, fmt.Errorf("%s: %w", child, err)
	}
	if !res.OK {
		return res, fmt.Errorf("%s: %s", child, res.Error)
	}
	return res, nil
}

var errNoProfile = errors.New("child returned no profile")
