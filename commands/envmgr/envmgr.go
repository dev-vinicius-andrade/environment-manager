package envmgr

import (
	"os"

	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/current"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/deactivate"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/list"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/profile"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/remote"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/run"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/serve"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/set"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/unset"
	"github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/use"
	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/types"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd *cobra.Command
	//information *context.DotfilesInformationContext
	context *context.EnvContext
}

func (c *commandDefinition) createCompletitionCommand() *cobra.Command {
	command := &cobra.Command{
		Use:                   "completion [bash|zsh|fish|powershell]",
		Short:                 "Generate completion script",
		Long:                  "To load completions",
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(c.cmd.Args),
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
		},
	}
	return command
}
func (c *commandDefinition) createCommandDefinition() *cobra.Command {

	command := &cobra.Command{
		Use:              "envmgr",
		Short:            "envmgr manages environment variable profiles per shell session.",
		Long:             `Activate named profiles of environment variables for the current shell session.`,
		TraverseChildren: true,
		Run:              c.runCommand,
	}

	c.context = createContext()
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	return command
}

func (c *commandDefinition) defineSubCommands() {
	c.cmd.AddCommand(c.createCompletitionCommand())
	use.CreateCommand(c.context, c.cmd)
	deactivate.CreateCommand(c.context, c.cmd)
	list.CreateCommand(c.context, c.cmd)
	current.CreateCommand(c.context, c.cmd)
	set.CreateCommand(c.context, c.cmd)
	unset.CreateCommand(c.context, c.cmd)
	run.CreateCommand(c.context, c.cmd)
	profile.CreateCommand(c.context, c.cmd)
	serve.CreateCommand(c.context, c.cmd)
	remote.CreateCommand(c.context, c.cmd)
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr"})
		return
	}
}

func (c *commandDefinition) defineFlags() {
}

func CreateCommand() *cobra.Command {
	c := commandDefinition{}
	return c.createCommandDefinition()
}
