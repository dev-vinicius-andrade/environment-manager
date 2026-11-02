package remote

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// remote add <name> <host:port> --token T --fingerprint FP
func createAddCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var token, fingerprint string
	var tokenStdin, force bool
	command := newLeaf(parent, "add <name> <host:port>", "Pair with a machine running `envmgr serve`", 2,
		func(cmd *cobra.Command, args []string) error {
			if tokenStdin {
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && line == "" {
					return fmt.Errorf("read token from stdin: %w", err)
				}
				token = strings.TrimSpace(line)
			}
			if token == "" || fingerprint == "" {
				return errors.New("--token (or --token-stdin) and --fingerprint are required; get both from `envmgr serve --show-token` on the remote machine")
			}
			rs, err := loadRemotes()
			if err != nil {
				return err
			}
			if _, err := rs.Get(args[0]); err == nil && !force {
				return fmt.Errorf("remote %q already exists (use --force to replace it)", args[0])
			}
			if err := rs.Add(agent.Remote{Name: args[0], Address: args[1], Token: token, Fingerprint: fingerprint}); err != nil {
				return err
			}
			if err := rs.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "remote %q saved (%s); test it with: envmgr remote profiles %s\n", args[0], args[1], args[0])
			return nil
		})
	f := command.Flags()
	f.StringVar(&token, "token", "", "access token printed by `envmgr serve` (visible in shell history; see --token-stdin)")
	f.BoolVar(&tokenStdin, "token-stdin", false, "read the access token from the first line of stdin")
	f.StringVar(&fingerprint, "fingerprint", "", "server certificate SHA-256 fingerprint printed by `envmgr serve`")
	f.BoolVar(&force, "force", false, "replace an existing remote with the same name")
	return command
}

// remote list
func createListCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "list", "List paired remotes (tokens are never printed)", 0,
		func(cmd *cobra.Command, args []string) error {
			rs, err := loadRemotes()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, n := range rs.Names() {
				r, _ := rs.Get(n)
				fmt.Fprintf(out, "%s\t%s\tsha256:%s\n", n, r.Address, r.Fingerprint)
			}
			return nil
		})
}

// remote remove <name>
func createRemoveCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "remove <name>", "Forget a paired remote (deletes its saved token)", 1,
		func(cmd *cobra.Command, args []string) error {
			rs, err := loadRemotes()
			if err != nil {
				return err
			}
			if err := rs.Remove(args[0]); err != nil {
				return err
			}
			return rs.Save()
		})
}

// remote profiles <name>
func createProfilesCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "profiles <name>", "List the profile names on a remote", 1,
		func(cmd *cobra.Command, args []string) error {
			c, err := clientFor(args[0])
			if err != nil {
				return err
			}
			names, err := c.List()
			if err != nil {
				return err
			}
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		})
}

// remote show <name> <profile> [--reveal]
func createShowCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var reveal bool
	command := newLeaf(parent, "show <name> <profile>", "Show a remote profile's variables (values masked unless --reveal)", 2,
		func(cmd *cobra.Command, args []string) error {
			c, err := clientFor(args[0])
			if err != nil {
				return err
			}
			p, err := c.Get(args[1])
			if err != nil {
				return err
			}
			return agent.WriteProfile(cmd.OutOrStdout(), p, reveal)
		})
	command.Flags().BoolVar(&reveal, "reveal", false, "print the variable values in clear text")
	return command
}

// remote push <name> <profile>
func createPushCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "push <name> <profile>", "Copy a local profile to the remote, overwriting it there", 2,
		func(cmd *cobra.Command, args []string) error {
			store, err := localStore()
			if err != nil {
				return err
			}
			p, err := store.Get(args[1])
			if err != nil {
				return fmt.Errorf("local profile %q: %w", args[1], err)
			}
			c, err := clientFor(args[0])
			if err != nil {
				return err
			}
			if err := c.Put(p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "pushed %q (%d vars) to %s\n", p.Name, len(p.Vars), args[0])
			return nil
		})
}

// remote pull <name> <profile> [--force]
func createPullCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var force bool
	command := newLeaf(parent, "pull <name> <profile>", "Copy a remote profile into the local store", 2,
		func(cmd *cobra.Command, args []string) error {
			store, err := localStore()
			if err != nil {
				return err
			}
			if !profile.ValidName(args[1]) {
				return fmt.Errorf("invalid profile name %q", args[1])
			}
			if _, err := store.Get(args[1]); err == nil && !force {
				return fmt.Errorf("local profile %q already exists (use --force to overwrite)", args[1])
			} else if err != nil && !errors.Is(err, profile.ErrNotFound) && !force {
				return err
			}
			c, err := clientFor(args[0])
			if err != nil {
				return err
			}
			p, err := c.Get(args[1])
			if err != nil {
				return err
			}
			if err := store.Save(p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "pulled %q (%d vars) from %s\n", p.Name, len(p.Vars), args[0])
			return nil
		})
	command.Flags().BoolVar(&force, "force", false, "overwrite an existing local profile")
	return command
}

// remote set <name> <profile> <VAR> <value>
func createSetCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "set <name> <profile> <VAR> <value>", "Set one variable in a remote profile (creates the profile if missing)", 4,
		func(cmd *cobra.Command, args []string) error {
			c, err := clientFor(args[0])
			if err != nil {
				return err
			}
			return c.SetVar(args[1], args[2], args[3])
		})
}

// remote unset <name> <profile> <VAR>
func createUnsetCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "unset <name> <profile> <VAR>", "Remove one variable from a remote profile", 3,
		func(cmd *cobra.Command, args []string) error {
			c, err := clientFor(args[0])
			if err != nil {
				return err
			}
			return c.UnsetVar(args[1], args[2])
		})
}

// remote delete <name> <profile>
func createDeleteCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "delete <name> <profile>", "Delete a profile on the remote", 2,
		func(cmd *cobra.Command, args []string) error {
			c, err := clientFor(args[0])
			if err != nil {
				return err
			}
			return c.Delete(args[1])
		})
}
