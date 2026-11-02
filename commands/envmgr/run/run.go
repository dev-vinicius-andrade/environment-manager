package run

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// exit terminates envmgr with the child's exit code (replaced in tests).
var exit = os.Exit

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	name, command := args[0], args[1:]
	if len(command) > 0 && command[0] == "--" && cmd.ArgsLenAtDash() < 0 {
		command = command[1:]
	}
	if len(command) == 0 {
		return fmt.Errorf("missing command: envmgr run %s -- <command> [args...]", name)
	}
	cmd.SilenceUsage = true

	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	p, err := store.Get(name)
	if errors.Is(err, profile.ErrNotFound) {
		return fmt.Errorf("profile %q not found", name)
	}
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(p.Vars))
	for k := range p.Vars {
		if shell.Reserved(k) {
			return fmt.Errorf("profile %q: variable %q uses the reserved ENVMGR_ prefix", name, k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := os.Environ()
	for _, k := range keys {
		// Later entries win (os/exec keeps the last duplicate).
		env = append(env, k+"="+p.Vars[k])
	}

	child := exec.Command(command[0], command[1:]...)
	child.Env = env
	child.Stdin = cmd.InOrStdin()
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()

	// The terminal delivers Ctrl+C to the child too; let it decide and report
	// its own exit code instead of envmgr dying first.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	err = child.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 {
			code = 1
		}
		exit(code)
		return nil
	}
	if err != nil {
		return fmt.Errorf("run %q: %w", command[0], err)
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "run <profile> -- <command> [args...]",
		Short: "Run a command with a profile's variables in its environment",
		Long: `Run a command with the profile's variables added to the current environment,
without changing the current shell. The command's stdin, stdout and stderr are
passed through and envmgr exits with the command's exit code.

  envmgr run dev -- gcloud config list
  envmgr run dev -- sh -c 'echo $MY_API_URL'`,
		Args:          cobra.MinimumNArgs(2),
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
	// Flags after the profile name belong to the command being run.
	c.cmd.Flags().SetInterspersed(false)
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
