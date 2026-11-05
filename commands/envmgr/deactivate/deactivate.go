package deactivate

import (
	"errors"
	"fmt"
	"io"
	"strings"

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
	stderr := cmd.ErrOrStderr()
	st := shell.ReadState(shell.OSLookup)
	code := ""
	if scope.Env && st.Active() {
		// Rendered before the hosts part so a failure leaves stdout empty.
		if code, err = shell.Deactivate(kind, shell.OSLookup); err != nil {
			return err
		}
	}
	if scope.Hosts {
		home, err := profile.DefaultDir()
		if err != nil {
			return err
		}
		path, _ := hosts.ResolvePath(c.hostsFile)
		ch, was, herr := hostsapply.Unapply(home, path, c.dryRun)
		switch {
		case herr != nil && code != "" && c.bestEffort && !c.dryRun:
			fmt.Fprintf(stderr, "envmgr: warning: applied profile hosts NOT removed: %v\n", herr)
			fmt.Fprintln(stderr, "envmgr: --best-effort: deactivating the environment anyway")
		case herr != nil && code != "":
			return fmt.Errorf("applied profile hosts not removed, so the environment was not deactivated either (stdout is empty; add --best-effort to deactivate it anyway): %w", herr)
		case herr != nil:
			return fmt.Errorf("applied profile hosts not removed: %w", herr)
		default:
			what := "applied profile hosts"
			if len(was) > 0 {
				what = fmt.Sprintf("hosts of profile %q", strings.Join(was, `", "`))
			}
			hostsapply.Report(stderr, ch, c.dryRun, "remove "+what)
		}
	}
	if c.dryRun {
		fmt.Fprintln(stderr, "envmgr: dry run: nothing was written and no shell code was printed")
		return nil
	}
	if scope.Env && !st.Active() {
		fmt.Fprintln(stderr, "envmgr: no active profile")
		return nil
	}
	if code != "" {
		if _, err := io.WriteString(cmd.OutOrStdout(), code); err != nil {
			return err
		}
		if st.Profile != "" {
			fmt.Fprintf(stderr, "envmgr: deactivated profile %q\n", st.Profile)
		}
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "deactivate",
		Short: "Print shell code that deactivates the active profile",
		Long: `Print shell code that deactivates the active profile, restoring every
variable it changed to its original value (or unsetting it when it did not
exist before) and clearing the ENVMGR_* state variables.

  bash/zsh:    eval "$(envmgr deactivate)"
  fish:        envmgr deactivate --shell fish | source
  PowerShell:  envmgr deactivate --shell powershell | Invoke-Expression
  cmd:         envmgr deactivate --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"

Prints nothing when no profile is active.

--hosts also removes the applied profile hosts group from the hosts file
(machine-wide: whichever profile "envmgr hosts apply" / "use --hosts" applied);
--only hosts does only that and prints no code. Same safety rules as
"use --hosts": elevated shell / sudo needed, nothing on stdout and exit 1 when
the hosts part fails (unless --best-effort), --dry-run, --file.`,
		Args:          cobra.NoArgs,
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
	c.cmd.Flags().BoolVar(&c.withHosts, "hosts", false, "deactivate the environment AND remove the applied profile hosts (needs an elevated shell / sudo)")
	c.cmd.Flags().StringVar(&c.only, "only", "", "what to deactivate: env, hosts or all (--only hosts prints no shell code)")
	_ = c.cmd.RegisterFlagCompletionFunc("only", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"env", "hosts", "all"}, cobra.ShellCompDirectiveNoFileComp
	})
	c.cmd.Flags().BoolVar(&c.bestEffort, "best-effort", false, "with --hosts: print the deactivate code even when the hosts file cannot be written")
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
