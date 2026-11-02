package remote

import (
	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
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
	helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr remote"})
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "remote",
		Short: "Manage profiles on another machine running `envmgr serve`",
		Long: `Pair with machines running ` + "`envmgr serve`" + ` and manage their profiles over pinned HTTPS.

Pair:   envmgr remote add <name> <host:port> --token T --fingerprint FP
Use:    envmgr remote profiles <name> | show | push | pull | set | unset | delete

Only profile operations exist; nothing can be executed on the remote machine.`,
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
		createAddCommand,
		createListCommand,
		createRemoveCommand,
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

// CreateCommand builds `envmgr remote` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}

// loadRemotes opens <ENVMGR_HOME>/remote/remotes.json.
func loadRemotes() (*agent.Remotes, error) {
	dir, err := agent.DefaultDir()
	if err != nil {
		return nil, err
	}
	return agent.LoadRemotes(dir)
}

// clientFor builds a pinned client for the named remote.
func clientFor(name string) (*agent.Client, error) {
	rs, err := loadRemotes()
	if err != nil {
		return nil, err
	}
	r, err := rs.Get(name)
	if err != nil {
		return nil, err
	}
	return r.Client()
}

func localStore() (*profile.Store, error) { return profile.NewStore() }

// newLeaf builds a subcommand with the shared settings (exact args, no usage on errors).
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
