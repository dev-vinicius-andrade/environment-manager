// Package join implements `envmgr join <code>`: bind this machine (child) to
// the parent that issued the code.
package join

import (
	ctx "context"
	"fmt"
	"time"

	agentcmd "github.com/dev-vinicius-andrade/environment-manager/commands/envmgr/agent"
	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	remote "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	name          string
	force         bool
	noRun         bool
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	dir, err := remote.AgentDir()
	if err != nil {
		return err
	}
	jctx, cancel := ctx.WithTimeout(ctx.Background(), 30*time.Second)
	defer cancel()
	b, err := remote.Join(jctx, dir, args[0], c.name, c.force)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "joined parent %s (%s) as %q with role %s\n", b.Parent, b.Addr, b.Name, b.Role)
	if remote.HasCap(b.Caps, remote.CapHosts) {
		fmt.Fprintln(cmd.ErrOrStderr(), "this parent was granted hosts editing: it may change the envmgr block of this machine's hosts file (`envmgr agent leave` to undo)")
	}
	if c.noRun {
		fmt.Fprintln(cmd.ErrOrStderr(), "start the connection later with: envmgr agent run")
		return nil
	}
	return agentcmd.RunLoop(cmd, b)
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "join <code>",
		Short: "Connect this machine to a parent with a one-time join code",
		Long: `Bind this machine (child) to the parent that printed the code with ` + "`envmgr parent invite`" + `.
The code is single-use and expires. The parent's certificate is pinned from the code;
any other certificate is refused. A machine has exactly one parent.

After joining, the connection runs in the foreground (Ctrl+C stops; resume with
` + "`envmgr agent run`" + `) unless --no-run is given.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE:         c.runCommand,
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
	f := c.cmd.Flags()
	f.StringVar(&c.name, "name", "", "name to register as (ignored when the parent fixed the name in the invite)")
	f.BoolVar(&c.force, "force", false, "replace an existing parent binding")
	f.BoolVar(&c.noRun, "no-run", false, "only enroll; do not start the connection loop")
}

// CreateCommand builds `envmgr join` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
