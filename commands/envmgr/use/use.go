package use

import (
	"errors"
	"fmt"
	"io"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
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
	withHosts     bool
	only          string
	bestEffort    bool
	dryRun        bool
	hostsFile     string
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	scope, err := hostsapply.ParseScope(c.withHosts, c.only)
	if err != nil {
		return err
	}
	if c.dryRun && !scope.Hosts {
		return errors.New("--dry-run previews the hosts part: add --hosts or --only hosts")
	}
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
	stderr := cmd.ErrOrStderr()
	// Render everything before writing so a failure leaves stdout empty.
	code := ""
	if scope.Env {
		if code, err = shell.Use(kind, shell.OSLookup, p); err != nil {
			return fmt.Errorf("profile %q: %w", p.Name, err)
		}
	}
	if scope.Hosts {
		path, _ := hosts.ResolvePath(c.hostsFile)
		ch, herr := hostsapply.Apply(store, path, p.Name, c.dryRun)
		switch {
		case herr != nil && scope.Env && c.bestEffort && !c.dryRun:
			fmt.Fprintf(stderr, "envmgr: warning: hosts of profile %q NOT applied: %v\n", p.Name, herr)
			fmt.Fprintln(stderr, "envmgr: --best-effort: activating the environment anyway")
		case herr != nil && scope.Env:
			return fmt.Errorf("hosts of profile %q not applied, so the environment was not activated either (stdout is empty; add --best-effort to activate it anyway): %w", p.Name, herr)
		case herr != nil:
			return fmt.Errorf("hosts of profile %q not applied: %w", p.Name, herr)
		default:
			hostsapply.Report(stderr, ch, c.dryRun, fmt.Sprintf("hosts of profile %q (%d entries)", p.Name, len(p.Hosts)))
			if len(p.Hosts) == 0 {
				fmt.Fprintf(stderr, "envmgr: profile %q has no hosts entries: only a previously applied profile's hosts were removed\n", p.Name)
			}
		}
	}
	if c.dryRun {
		fmt.Fprintln(stderr, "envmgr: dry run: nothing was written and no shell code was printed")
		return nil
	}
	if scope.Env {
		if _, err := io.WriteString(cmd.OutOrStdout(), code); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "envmgr: activated profile %q (%d variables)\n", p.Name, len(p.Vars))
	}
	return nil
}

// scopeFlags registers --hosts and --only.
func scopeFlags(command *cobra.Command, withHosts *bool, only *string, verb string) {
	command.Flags().BoolVar(withHosts, "hosts", false, verb+" the environment AND the profile's hosts entries (writes the hosts file: needs an elevated shell / sudo)")
	command.Flags().StringVar(only, "only", "", "what to "+verb+": env, hosts or all (--only hosts prints no shell code)")
	_ = command.RegisterFlagCompletionFunc("only", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"env", "hosts", "all"}, cobra.ShellCompDirectiveNoFileComp
	})
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
restore them.

By default only the environment is activated. A profile can also hold hosts
entries; they are machine-wide and written by envmgr itself into the managed
block of the hosts file (group "profile:<name>", replacing the previously
applied profile's group, backup first):

  --hosts          environment and hosts
  --only hosts     hosts only (no shell code on stdout)
  --only env|all   environment only / both

Writing the hosts file needs an elevated shell (Windows) or sudo; envmgr never
elevates itself. If the hosts part fails, nothing is printed on stdout and the
exit code is 1, so the shell is never left half applied; --best-effort prints
the environment code anyway. --dry-run shows the hosts diff (stderr) and
prints no code. --file / ENVMGR_HOSTS_FILE choose another hosts file.`,
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
	scopeFlags(c.cmd, &c.withHosts, &c.only, "activate")
	c.cmd.Flags().BoolVar(&c.bestEffort, "best-effort", false, "with --hosts: print the environment code even when the hosts file cannot be written")
	c.cmd.Flags().BoolVar(&c.dryRun, "dry-run", false, "with --hosts / --only hosts: print the hosts diff on stderr, write nothing, print no code")
	c.cmd.Flags().StringVar(&c.hostsFile, "file", "", "hosts file (default: $"+hosts.EnvFile+", else the system hosts file)")
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
