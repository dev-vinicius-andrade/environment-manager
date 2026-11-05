// Package hosts implements `envmgr profile hosts add|remove`: edit the hosts
// entries stored in a profile. It never touches the hosts file; applying the
// entries is `envmgr hosts apply <profile>` (or `use <profile> --hosts`).
package hosts

import (
	"errors"
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostsapply"
	"github.com/dev-vinicius-andrade/environment-manager/internal/hostspec"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// CreateCommand builds `envmgr profile hosts` and attaches it to parentCommand.
func CreateCommand(_ *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &cobra.Command{
		Use:   "hosts",
		Short: "Edit the hosts entries stored in a profile",
		Long: `Edit the hosts entries stored in a profile (the profile file only; the
hosts file is not touched). Apply them with "envmgr hosts apply <profile>" or
"envmgr use <profile> --hosts" from an elevated shell / with sudo.

  envmgr profile hosts add dev 10.0.0.5 api.dev.local db.dev.local [--comment "dev cluster"]
  envmgr profile hosts remove dev api.dev.local     # one hostname
  envmgr profile hosts remove dev 10.0.0.5          # the whole entry of an IP

Same validation as "envmgr hosts add": IPv4/IPv6 (no zone ids), RFC 1123
names, 1-32 names per entry; critical names (localhost, broadcasthost, ip6-*)
are refused. A name already mapped to another IP in the profile moves.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr profile hosts"})
		},
	}
	helpers.CobraHelper.AddCommandToParent(command, parentCommand)

	var comment string
	add := &cobra.Command{
		Use:           "add <profile> <ip> <hostname>...",
		Short:         "Map hostnames to an IP in a profile (creates the profile if missing)",
		Args:          cobra.MinimumNArgs(3),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := profile.NewStore()
			if err != nil {
				return err
			}
			if err := store.SetHostEntry(args[0], profile.HostEntry{IP: args[1], Names: args[2:], Comment: comment}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: profile %q maps %v to %s\n", args[0], args[2:], args[1])
			appliedNote(cmd, store, args[0])
			return nil
		},
	}
	add.Flags().StringVar(&comment, "comment", "", "optional note kept in the profile (not written to the hosts file)")
	helpers.CobraHelper.AddCommandToParent(add, command)

	remove := &cobra.Command{
		Use:           "remove <profile> <hostname>|<ip>",
		Short:         "Remove a hostname, or the whole entry of an IP, from a profile",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if _, ipErr := hostspec.ParseIP(args[1]); ipErr == nil {
				err = p.RemoveHostIP(args[1])
			} else {
				err = p.RemoveHostname(args[1])
			}
			if errors.Is(err, profile.ErrHostNotFound) {
				fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: %v in profile %q\n", err, args[0])
				return nil
			}
			if err != nil {
				return err
			}
			if err := store.Save(p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: removed %s from profile %q\n", args[1], args[0])
			appliedNote(cmd, store, args[0])
			return nil
		},
	}
	helpers.CobraHelper.AddCommandToParent(remove, command)
	return command
}

// appliedNote reminds the user that the hosts file keeps the old entries of
// an applied profile until it is applied again.
func appliedNote(cmd *cobra.Command, store *profile.Store, name string) {
	if st, err := hostsapply.ReadState(store.Dir); err == nil && st.Profile == name {
		fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: profile %q is applied to the hosts file; run \"envmgr hosts apply %s\" again (elevated) to update it\n", name, name)
	}
}
