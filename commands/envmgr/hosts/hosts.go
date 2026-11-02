// Package hosts implements `envmgr hosts`: edit the envmgr-managed block of
// this machine's hosts file. Lines outside the block are never changed.
package hosts

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	file          string
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) {
	helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr hosts"})
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "hosts",
		Short: "Edit the envmgr-managed block of this machine's hosts file",
		Long: `envmgr only edits the lines between "# BEGIN envmgr" and "# END envmgr" (created
at the end of the file when missing). Every other line is kept byte for byte.
Entries are grouped ("# group: <name>") so a set can be switched on or off together.

  envmgr hosts list [--all]
  envmgr hosts add 10.0.0.5 api.dev.local db.dev.local --group dev
  envmgr hosts remove api.dev.local [--group dev]
  envmgr hosts group disable dev
  envmgr hosts restore [<backup>] | --list

Profiles can hold hosts entries too ("envmgr profile hosts add"). Their group is
"profile:<name>", written only by:
  envmgr hosts apply <profile>    (= envmgr use <profile> --only hosts)
  envmgr hosts unapply            (= envmgr deactivate --only hosts)
  envmgr hosts applied            (which profile is applied on this machine)
One profile is applied at a time: applying another replaces its group.

Every mutating command accepts --dry-run (alias --show-diff): print the change, write nothing.
A timestamped backup (last 10 kept) is written next to the file before every change.
Writing needs an elevated shell (Windows) or sudo (Linux/macOS); envmgr never elevates itself.
Use --file or ENVMGR_HOSTS_FILE to work on another file (for example to try it safely).`,
		Run: c.runCommand,
	}
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

func (c *commandDefinition) defineFlags() {
	c.cmd.PersistentFlags().StringVar(&c.file, "file", "", "hosts file to edit (default: $"+hosts.EnvFile+", else the system hosts file)")
}

func (c *commandDefinition) hostsFile() *hosts.File {
	p, _ := hosts.ResolvePath(c.file)
	return hosts.NewFile(p)
}

func leaf(parent *cobra.Command, use, short string, args cobra.PositionalArgs, run func(cmd *cobra.Command, args []string) error) *cobra.Command {
	command := &cobra.Command{Use: use, Short: short, Args: args, SilenceUsage: true, SilenceErrors: true, RunE: run}
	helpers.CobraHelper.AddCommandToParent(command, parent)
	return command
}

// dryRunFlags registers --dry-run and its alias --show-diff.
func dryRunFlags(command *cobra.Command, dryRun *bool) {
	command.Flags().BoolVar(dryRun, "dry-run", false, "print the change as a diff and write nothing")
	command.Flags().BoolVar(dryRun, "show-diff", false, "alias of --dry-run")
}

func (c *commandDefinition) defineSubCommands() {
	var all bool
	list := leaf(c.cmd, "list", "List the managed entries by group", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		doc, err := c.hostsFile().Read()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if err := hosts.WriteTable(out, doc.Groups); err != nil {
			return err
		}
		if all {
			fmt.Fprintln(out, "\nunmanaged lines (read-only):")
			for _, l := range doc.Unmanaged() {
				fmt.Fprintf(out, "%5d  %s\n", l.N, l.Text)
			}
		}
		return nil
	})
	list.Flags().BoolVar(&all, "all", false, "also print the lines outside the managed block (read-only)")

	var addGroup string
	var force, addDry bool
	add := leaf(c.cmd, "add <ip> <hostname>...", "Map hostnames to an IP in the managed block", cobra.MinimumNArgs(2), func(cmd *cobra.Command, args []string) error {
		disabled := false
		g, _ := hosts.CheckGroup(addGroup)
		ch, err := c.hostsFile().Update(addDry, func(d *hosts.Doc) (bool, error) {
			changed, err := d.Add(args[0], args[1:], addGroup, force)
			if err == nil && d.Group(g) != nil {
				disabled = !d.Group(g).Enabled
			}
			return changed, err
		})
		if err == nil && disabled {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: group %q is disabled; its entries stay commented out until `envmgr hosts group enable %s`\n", g, g)
		}
		return report(cmd, ch, err, addDry)
	})
	add.Flags().StringVar(&addGroup, "group", "", "group to add to (default \""+hosts.DefaultGroup+"\")")
	add.Flags().BoolVar(&force, "force", false, "allow shadowing critical names (localhost, broadcasthost, ip6-*)")
	dryRunFlags(add, &addDry)

	var rmGroup string
	var rmDry bool
	remove := leaf(c.cmd, "remove <hostname>", "Remove a hostname from the managed block", cobra.ExactArgs(1), func(cmd *cobra.Command, args []string) error {
		ch, err := c.hostsFile().Update(rmDry, func(d *hosts.Doc) (bool, error) { return d.Remove(args[0], rmGroup) })
		return report(cmd, ch, err, rmDry)
	})
	remove.Flags().StringVar(&rmGroup, "group", "", "only remove it from this group (default: every group)")
	dryRunFlags(remove, &rmDry)

	var grpDry bool
	group := leaf(c.cmd, "group enable|disable <group>", "Switch a group of entries on or off (disabled entries stay commented out)", cobra.ExactArgs(2), func(cmd *cobra.Command, args []string) error {
		var enable bool
		switch args[0] {
		case "enable":
			enable = true
		case "disable":
		default:
			return fmt.Errorf("unknown action %q: use enable or disable", args[0])
		}
		ch, err := c.hostsFile().Update(grpDry, func(d *hosts.Doc) (bool, error) { return d.SetGroupEnabled(args[1], enable) })
		return report(cmd, ch, err, grpDry)
	})
	dryRunFlags(group, &grpDry)

	leaf(c.cmd, "backup", "Save a timestamped copy of the hosts file next to it (last 10 kept)", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		p, err := c.hostsFile().Backup()
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), p)
		return nil
	})

	var restoreDry, restoreList bool
	restore := leaf(c.cmd, "restore [<backup>]", "Replace the hosts file with a backup (default: the newest)", cobra.MaximumNArgs(1), func(cmd *cobra.Command, args []string) error {
		f := c.hostsFile()
		if restoreList {
			list, err := f.Backups()
			if err != nil {
				return err
			}
			for _, b := range list {
				fmt.Fprintln(cmd.OutOrStdout(), filepath.Base(b))
			}
			return nil
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		ch, err := f.Restore(name, restoreDry)
		return report(cmd, ch, err, restoreDry)
	})
	restore.Flags().BoolVar(&restoreList, "list", false, "list the backups (oldest first) instead of restoring")
	dryRunFlags(restore, &restoreDry)

	leaf(c.cmd, "path", "Print the hosts file envmgr would edit", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		p, src := hosts.ResolvePath(c.file)
		fmt.Fprintln(cmd.OutOrStdout(), p)
		fmt.Fprintf(cmd.ErrOrStderr(), "(from %s)\n", src)
		return nil
	})

	c.defineProfileSubCommands()
}

// defineProfileSubCommands adds apply, unapply and applied: the hosts part of
// a profile (`use --only hosts` / `deactivate --only hosts`).
func (c *commandDefinition) defineProfileSubCommands() {
	var applyDry, applyPause bool
	var applyHome string
	apply := leaf(c.cmd, "apply <profile>", "Write a profile's hosts entries into the managed block (replaces the applied profile)", cobra.ExactArgs(1), func(cmd *cobra.Command, args []string) error {
		defer pauseIf(cmd, applyPause)
		store, err := storeAt(applyHome)
		if err != nil {
			return err
		}
		p, _ := hosts.ResolvePath(c.file)
		ch, err := hostsapply.Apply(store, p, args[0], applyDry)
		if errors.Is(err, profile.ErrNotFound) {
			return fmt.Errorf("profile %q not found in %s", args[0], store.Dir)
		}
		if err := report(cmd, ch, err, applyDry); err != nil {
			return err
		}
		if !applyDry {
			fmt.Fprintf(cmd.ErrOrStderr(), "applied hosts of profile %q (group %q)\n", args[0], hosts.ProfileGroup(args[0]))
		}
		return nil
	})
	dryRunFlags(apply, &applyDry)
	homeFlags(apply, &applyHome, &applyPause)

	var unDry, unPause bool
	var unHome string
	unapply := leaf(c.cmd, "unapply", "Remove the applied profile's hosts entries from the managed block", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		defer pauseIf(cmd, unPause)
		home, err := homeDir(unHome)
		if err != nil {
			return err
		}
		p, _ := hosts.ResolvePath(c.file)
		ch, was, err := hostsapply.Unapply(home, p, unDry)
		if err := report(cmd, ch, err, unDry); err != nil {
			return err
		}
		if !unDry && len(was) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "removed the hosts of profile %q\n", strings.Join(was, `", "`))
		}
		return nil
	})
	dryRunFlags(unapply, &unDry)
	homeFlags(unapply, &unHome, &unPause)

	var appliedHome string
	applied := leaf(c.cmd, "applied", "Print the profile whose hosts are applied on this machine", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		home, err := homeDir(appliedHome)
		if err != nil {
			return err
		}
		st, err := hostsapply.ReadState(home)
		if err != nil {
			return err
		}
		stderr := cmd.ErrOrStderr()
		if st.Profile != "" {
			fmt.Fprintln(cmd.OutOrStdout(), st.Profile)
			fmt.Fprintf(stderr, "(applied to %s at %s)\n", st.File, st.AppliedAt.Local().Format(time.RFC3339))
		}
		// The hosts file is the truth (a restore can change it): warn on a mismatch.
		p, _ := hosts.ResolvePath(c.file)
		if doc, err := hosts.NewFile(p).Read(); err == nil {
			inFile := doc.AppliedProfiles()
			if strings.Join(inFile, ",") != st.Profile {
				fmt.Fprintf(stderr, "warning: %s holds the hosts of profile(s) [%s] but envmgr recorded [%s]; run `envmgr hosts apply <profile>` or `envmgr hosts unapply`\n", p, strings.Join(inFile, ", "), st.Profile)
			}
		}
		return nil
	})
	applied.Flags().StringVar(&appliedHome, "home", "", "envmgr home directory (default: $ENVMGR_HOME, else <user config dir>/envmgr)")
}

func homeFlags(command *cobra.Command, home *string, pause *bool) {
	command.Flags().StringVar(home, "home", "", "envmgr home directory holding the profiles (default: $ENVMGR_HOME, else <user config dir>/envmgr); useful under sudo or an elevated prompt")
	command.Flags().BoolVar(pause, "pause", false, "wait for Enter before exiting (used by the tray's elevated window)")
	_ = command.Flags().MarkHidden("pause")
}

func homeDir(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return profile.DefaultDir()
}

func storeAt(flagValue string) (*profile.Store, error) {
	d, err := homeDir(flagValue)
	if err != nil {
		return nil, err
	}
	return &profile.Store{Dir: d}, nil
}

// pauseIf keeps the window of an elevated run (tray) open until Enter.
func pauseIf(cmd *cobra.Command, pause bool) {
	if !pause {
		return
	}
	fmt.Fprint(cmd.ErrOrStderr(), "\nPress Enter to close this window.")
	_, _ = bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
}

// report prints the outcome of a mutating command: the diff on stdout for a
// dry run, a one-line summary on stderr otherwise.
func report(cmd *cobra.Command, ch hosts.Change, err error, dryRun bool) error {
	if err != nil {
		return err
	}
	stderr := cmd.ErrOrStderr()
	switch {
	case !ch.Changed:
		fmt.Fprintf(stderr, "no change: %s is already up to date\n", ch.Path)
	case dryRun:
		fmt.Fprint(cmd.OutOrStdout(), ch.Diff)
		fmt.Fprintln(stderr, "dry run: nothing was written")
	default:
		fmt.Fprintf(stderr, "updated %s (backup: %s)\n", ch.Path, ch.Backup)
	}
	if ch.Warning != "" {
		fmt.Fprintln(stderr, "warning:", ch.Warning)
	}
	return nil
}

// CreateCommand builds `envmgr hosts` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
