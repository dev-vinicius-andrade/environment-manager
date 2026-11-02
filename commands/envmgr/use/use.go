package use

import (
	"errors"
	"fmt"
	"io"

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
	shellName     string
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	kind, err := shell.Resolve(c.shellName)
	if err != nil {
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
	// Render everything before writing so a failure leaves stdout empty.
	code, err := shell.Use(kind, shell.OSLookup, p)
	if err != nil {
		return fmt.Errorf("profile %q: %w", p.Name, err)
	}
	if _, err := io.WriteString(cmd.OutOrStdout(), code); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: activated profile %q (%d variables)\n", p.Name, len(p.Vars))
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "use <profile>",
		Short: "Print shell code that activates a profile in the current shell",
		Long: `Print shell code that activates a profile in the current shell session.

A program cannot change its parent shell's environment, so the shell must
evaluate the output (only the code goes to stdout; messages go to stderr):

  bash/zsh:    eval "$(envmgr use dev)"
  fish:        envmgr use dev --shell fish | source
  PowerShell:  envmgr use dev --shell powershell | Invoke-Expression
  cmd:         envmgr use dev --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"

Variables changed by a previously active profile are restored first. The
original values are kept in ENVMGR_PREV_<VAR> so "envmgr deactivate" can
restore them.`,
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
	c.cmd.Flags().StringVar(&c.shellName, "shell", "", shell.FlagUsage)
	_ = c.cmd.RegisterFlagCompletionFunc("shell", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return shell.Names, cobra.ShellCompDirectiveNoFileComp
	})
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
