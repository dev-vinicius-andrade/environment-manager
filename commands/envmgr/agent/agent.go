// Package agent implements `envmgr agent`: the child side that runs the loop
// connecting this machine to its single parent.
package agent

import (
	ctx "context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	remote "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
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
	helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr agent"})
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "agent",
		Short: "Run or inspect this machine's connection to its parent",
		Long: `This machine as a child: it dials its single parent (set by ` + "`envmgr join <code>`" + `),
waits for profile operations and runs only those against the local profile store.
No port is opened and no command can be executed through it.`,
		Run: c.runCommand,
	}
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

func (c *commandDefinition) defineSubCommands() {
	leaf := func(use, short string, run func(cmd *cobra.Command, args []string) error) {
		helpers.CobraHelper.AddCommandToParent(&cobra.Command{
			Use: use, Short: short, Args: cobra.NoArgs, SilenceUsage: true, RunE: run,
		}, c.cmd)
	}
	leaf("run", "Connect to the parent and serve its requests (foreground; Ctrl+C stops)", func(cmd *cobra.Command, args []string) error {
		b, err := loadBinding()
		if err != nil {
			return err
		}
		return RunLoop(cmd, b)
	})
	leaf("status", "Show the parent this machine is bound to and whether it accepts us", func(cmd *cobra.Command, args []string) error {
		b, err := loadBinding()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		caps := "none"
		if len(b.Caps) > 0 {
			caps = strings.Join(b.Caps, ",")
		}
		fmt.Fprintf(out, "parent:      %s (%s)\nname:        %s\nrole:        %s\ncaps:        %s\nfingerprint: %s\nenrolled:    %s\n",
			b.Parent, b.Addr, b.Name, b.Role, caps, b.Fingerprint, b.EnrolledAt.Local().Format("2006-01-02 15:04"))
		a, err := remote.NewAgent(b, nil, nil)
		if err != nil {
			return err
		}
		c, cancel := ctx.WithTimeout(ctx.Background(), 10*time.Second)
		defer cancel()
		switch _, err := a.Ping(c); {
		case err == nil:
			fmt.Fprintln(out, "connected:   yes (credential accepted)")
		case errors.Is(err, remote.ErrRevoked):
			fmt.Fprintln(out, "connected:   no (revoked by the parent)")
		default:
			fmt.Fprintf(out, "connected:   no (%v)\n", err)
		}
		return nil
	})
	leaf("leave", "Forget the parent (deletes the local credential; tells the parent best-effort)", func(cmd *cobra.Command, args []string) error {
		dir, err := remote.AgentDir()
		if err != nil {
			return err
		}
		b, err := remote.LoadBinding(dir)
		if err != nil {
			return err
		}
		if a, err := remote.NewAgent(b, nil, nil); err == nil {
			c, cancel := ctx.WithTimeout(ctx.Background(), 5*time.Second)
			if err := a.Leave(c); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "could not notify the parent (%v); it can still `envmgr parent revoke %s`\n", err, b.Name)
			}
			cancel()
		}
		if err := remote.RemoveBinding(dir); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "left parent %s\n", b.Parent)
		return nil
	})
}

func (c *commandDefinition) defineFlags() {
}

// CreateCommand builds `envmgr agent` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}

func loadBinding() (*remote.Binding, error) {
	dir, err := remote.AgentDir()
	if err != nil {
		return nil, err
	}
	return remote.LoadBinding(dir)
}

// RunLoop runs the child loop for b in the foreground until Ctrl+C (nil) or
// until the parent revokes this machine (remote.ErrRevoked).
func RunLoop(cmd *cobra.Command, b *remote.Binding) error {
	stderr := cmd.ErrOrStderr()
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	a, err := remote.NewAgent(b, store, log.New(stderr, "envmgr agent: ", log.LstdFlags))
	if err != nil {
		return err
	}
	a.OnState = func(s string) { fmt.Fprintf(stderr, "envmgr agent: %s\n", s) }
	base := cmd.Context()
	if base == nil {
		base = ctx.Background()
	}
	sigCtx, stop := signal.NotifyContext(base, os.Interrupt)
	defer stop()
	fmt.Fprintf(stderr, "connecting to parent %s (%s) as %q, role %s; Ctrl+C stops\n", b.Parent, b.Addr, b.Name, b.Role)
	if remote.HasCap(b.Caps, remote.CapHosts) {
		fmt.Fprintf(stderr, "this parent may edit the envmgr block of the hosts file %s\n", a.HostsFile)
	}
	if err := a.Run(sigCtx); err != nil {
		return err
	}
	fmt.Fprintln(stderr, "stopped")
	return nil
}
