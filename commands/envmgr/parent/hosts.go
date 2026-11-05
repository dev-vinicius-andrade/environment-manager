package parent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hosts"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// parent hosts <agent> list|add|remove|group ...
func createHostsCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var group string
	var dryRun bool
	command := &cobra.Command{
		Use:   "hosts <agent> list|add|remove|group|apply-profile|unapply [args]",
		Short: "Edit the envmgr block of a child's hosts file (needs an invite made with --allow-hosts)",
		Long: `Edit the managed block ("# BEGIN envmgr" ... "# END envmgr") of the child's own hosts file.
The child must have been invited with --allow-hosts; mutations also need a read-write child.
The child uses its local hosts file (system default or its ENVMGR_HOSTS_FILE) and must run
elevated (Windows) or as root (Linux/macOS) for writes. Critical names (localhost,
broadcasthost, ip6-*) are always refused remotely.

  envmgr parent hosts <agent> list
  envmgr parent hosts <agent> add <ip> <hostname>... [--group g] [--dry-run]
  envmgr parent hosts <agent> remove <hostname> [--group g] [--dry-run]
  envmgr parent hosts <agent> group enable|disable <group> [--dry-run]
  envmgr parent hosts <agent> apply-profile <profile> [--dry-run]
  envmgr parent hosts <agent> unapply [--dry-run]

apply-profile writes the hosts entries of the CHILD's own profile <profile>
(push it first to send yours: "envmgr parent push <agent> <profile>"); only the
name travels. It replaces the profile applied on the child before, like
"envmgr hosts apply" there. unapply removes it. Both need the grant and a
read-write child, and are logged on both sides.`,
		Args:          cobra.MinimumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			child, action, rest := args[0], args[1], args[2:]
			var job agent.Job
			switch action {
			case "list":
				if len(rest) != 0 {
					return errors.New("usage: parent hosts <agent> list")
				}
				job = agent.Job{Op: agent.OpHostsList}
			case "add":
				if len(rest) < 2 {
					return errors.New("usage: parent hosts <agent> add <ip> <hostname>... [--group g]")
				}
				job = agent.Job{Op: agent.OpHostsAdd, Args: agent.JobArgs{IP: rest[0], Hostnames: rest[1:], Group: group}}
			case "remove":
				if len(rest) != 1 {
					return errors.New("usage: parent hosts <agent> remove <hostname> [--group g]")
				}
				job = agent.Job{Op: agent.OpHostsRemove, Args: agent.JobArgs{Hostnames: rest, Group: group}}
			case "group":
				if len(rest) != 2 || (rest[0] != "enable" && rest[0] != "disable") {
					return errors.New("usage: parent hosts <agent> group enable|disable <group>")
				}
				enable := rest[0] == "enable"
				job = agent.Job{Op: agent.OpHostsGroup, Args: agent.JobArgs{Group: rest[1], Enable: &enable}}
			case "apply-profile":
				if len(rest) != 1 || group != "" {
					return errors.New("usage: parent hosts <agent> apply-profile <profile> [--dry-run]")
				}
				job = agent.Job{Op: agent.OpHostsApplyProfile, Args: agent.JobArgs{Profile: rest[0]}}
			case "unapply":
				if len(rest) != 0 || group != "" {
					return errors.New("usage: parent hosts <agent> unapply [--dry-run]")
				}
				job = agent.Job{Op: agent.OpHostsUnapply}
			default:
				return fmt.Errorf("unknown hosts action %q (list, add, remove, group, apply-profile, unapply)", action)
			}
			if job.Mutates() {
				job.Args.DryRun = dryRun
			} else if dryRun {
				return errors.New("--dry-run applies to add, remove, group, apply-profile and unapply")
			}
			if err := job.Validate(); err != nil {
				return err
			}
			res, err := relay(child, job)
			if err != nil {
				return err
			}
			if res.Hosts == nil {
				return fmt.Errorf("%s: child returned no hosts result", child)
			}
			h := res.Hosts
			stderr := cmd.ErrOrStderr()
			if job.Op == agent.OpHostsList {
				fmt.Fprintf(stderr, "%s: %s\n", child, h.Path)
				return hosts.WriteTable(cmd.OutOrStdout(), h.Groups)
			}
			switch {
			case !h.Changed:
				fmt.Fprintf(stderr, "%s: no change (%s is already up to date)\n", child, h.Path)
			case dryRun:
				fmt.Fprint(cmd.OutOrStdout(), h.Diff)
				fmt.Fprintf(stderr, "%s: dry run, nothing was written\n", child)
			default:
				fmt.Fprintf(stderr, "%s: updated %s (backup on the child: %s)\n", child, h.Path, h.Backup)
			}
			if h.Warning != "" {
				fmt.Fprintf(stderr, "%s: warning: %s\n", child, h.Warning)
			}
			if !dryRun && len(h.Applied) > 0 {
				switch job.Op {
				case agent.OpHostsApplyProfile:
					fmt.Fprintf(stderr, "%s: hosts of profile %q applied\n", child, h.Applied[0])
				case agent.OpHostsUnapply:
					fmt.Fprintf(stderr, "%s: removed the hosts of profile %q\n", child, strings.Join(h.Applied, `", "`))
				}
			}
			return nil
		},
	}
	f := command.Flags()
	f.StringVar(&group, "group", "", "group for add (default \""+hosts.DefaultGroup+"\") or to restrict remove")
	f.BoolVar(&dryRun, "dry-run", false, "the child returns the diff and writes nothing")
	command.Flags().BoolVar(&dryRun, "show-diff", false, "alias of --dry-run")
	helpers.CobraHelper.AddCommandToParent(command, parent)
	return command
}
