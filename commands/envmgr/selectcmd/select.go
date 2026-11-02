// Package selectcmd implements `envmgr select`, which sets the machine-wide
// profile selection followed by shells that run the hook (`envmgr hook`).
// (The package is not called "select": that is a Go keyword.)
package selectcmd

import (
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/selected"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	none          bool
	withHosts     bool
	envOnly       bool
	only          string
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	if c.none == (len(args) == 1) {
		return fmt.Errorf("give a profile name or --none")
	}
	cmd.SilenceUsage = true
	s, err := selected.New()
	if err != nil {
		return err
	}
	if c.none && (c.withHosts || c.envOnly || c.only != "") {
		return fmt.Errorf("--none takes no scope flags")
	}
	if c.none {
		if err := s.Clear(); err != nil {
			return err
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "envmgr: selection cleared (following shells deactivate on their next prompt)")
		return nil
	}
	if c.envOnly && (c.withHosts || c.only != "") {
		return fmt.Errorf("--env cannot be combined with --hosts or --only")
	}
	scope, err := hostsapply.ParseScope(c.withHosts, c.only)
	if err != nil {
		return err
	}
	if err := s.SetScoped(args[0], selected.Scopes{Env: scope.Env, Hosts: scope.Hosts}); err != nil {
		return err
	}
	stderr := cmd.ErrOrStderr()
	if scope.Env {
		fmt.Fprintf(stderr, "envmgr: selected profile %q (following shells switch on their next prompt)\n", args[0])
	} else {
		fmt.Fprintf(stderr, "envmgr: selected the hosts of profile %q only (following shells deactivate their selected profile)\n", args[0])
	}
	if scope.Hosts {
		if st, err := hostsapply.ReadState(s.Profiles.Dir); err != nil || st.Profile != args[0] {
			fmt.Fprintf(stderr, "envmgr: the hosts entries are NOT applied by select (that needs administrator rights): run \"envmgr hosts apply %s\" in an elevated shell / with sudo\n", args[0])
		}
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "select <profile> | --none",
		Short: "Select the machine-wide profile that following shells activate",
		Long: `Set the machine-wide profile selection (the same one the tray icon sets),
stored in <ENVMGR_HOME>/selected.json.

Shells that run the envmgr hook ("envmgr hook --install <shell>") and are not
pinned switch to the selected profile on their next prompt; --none makes them
deactivate. A shell becomes pinned when it runs "envmgr use" (or activates a
profile from the TUI) and goes back to following with "envmgr follow".

Scopes: by default only the environment is selected. --hosts selects the
environment and the profile's hosts entries, --only hosts|env|all exactly that
(--env = --only env). The hosts scope is a record of intent: the hook and
"envmgr sync" never write the hosts file (it needs administrator rights); sync
prints a one-line hint, once per selection change, to run
"envmgr hosts apply <profile>" in an elevated shell.

This command changes no shell by itself and prints nothing on stdout.`,
		Args:          cobra.MaximumNArgs(1),
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
	c.cmd.Flags().BoolVar(&c.none, "none", false, "clear the selection (following shells deactivate)")
	c.cmd.Flags().BoolVar(&c.withHosts, "hosts", false, "select the environment and the profile's hosts entries")
	c.cmd.Flags().BoolVar(&c.envOnly, "env", false, "select the environment only (the default)")
	c.cmd.Flags().StringVar(&c.only, "only", "", "select only: env, hosts or all")
}

// CreateCommand builds `envmgr select` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
