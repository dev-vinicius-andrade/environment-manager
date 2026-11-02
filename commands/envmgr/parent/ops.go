package parent

import (
	"errors"
	"fmt"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// parent profiles <agent>
func createProfilesCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "profiles <agent>", "List the profile names on a child", 1,
		func(cmd *cobra.Command, args []string) error {
			res, err := relay(args[0], agent.Job{Op: agent.OpList})
			if err != nil {
				return err
			}
			for _, n := range res.Profiles {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		})
}

// parent show <agent> <profile> [--reveal]
func createShowCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var reveal bool
	command := newLeaf(parent, "show <agent> <profile>", "Show a child's profile (values masked unless --reveal)", 2,
		func(cmd *cobra.Command, args []string) error {
			p, err := fetch(args[0], args[1])
			if err != nil {
				return err
			}
			return agent.WriteProfile(cmd.OutOrStdout(), p, reveal)
		})
	command.Flags().BoolVar(&reveal, "reveal", false, "print the values in clear text")
	return command
}

func fetch(child, name string) (profile.Profile, error) {
	if !profile.ValidName(name) {
		return profile.Profile{}, fmt.Errorf("invalid profile name %q", name)
	}
	res, err := relay(child, agent.Job{Op: agent.OpGet, Args: agent.JobArgs{Profile: name}})
	if err != nil {
		return profile.Profile{}, err
	}
	if res.Profile == nil {
		return profile.Profile{}, errNoProfile
	}
	p := *res.Profile
	p.Name = name
	if p.Vars == nil {
		p.Vars = map[string]string{}
	}
	return p, p.Validate()
}

// parent push <agent> <profile>
func createPushCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "push <agent> <profile>", "Copy a local profile to the child, overwriting it there", 2,
		func(cmd *cobra.Command, args []string) error {
			store, err := profile.NewStore()
			if err != nil {
				return err
			}
			p, err := store.Get(args[1])
			if err != nil {
				return fmt.Errorf("local profile %q: %w", args[1], err)
			}
			if _, err := relay(args[0], agent.Job{Op: agent.OpPut, Args: agent.JobArgs{Profile: p.Name, Vars: p.Vars}}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "pushed %q (%d vars) to %s\n", p.Name, len(p.Vars), args[0])
			return nil
		})
}

// parent pull <agent> <profile> [--force]
func createPullCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var force bool
	command := newLeaf(parent, "pull <agent> <profile>", "Copy a child's profile into the local store", 2,
		func(cmd *cobra.Command, args []string) error {
			if !profile.ValidName(args[1]) {
				return fmt.Errorf("invalid profile name %q", args[1])
			}
			store, err := profile.NewStore()
			if err != nil {
				return err
			}
			if _, err := store.Get(args[1]); err == nil && !force {
				return fmt.Errorf("local profile %q already exists (use --force to overwrite)", args[1])
			} else if err != nil && !errors.Is(err, profile.ErrNotFound) && !force {
				return err
			}
			p, err := fetch(args[0], args[1])
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

// parent set <agent> <profile> <VAR> <value>
func createSetCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "set <agent> <profile> <VAR> <value>", "Set one variable in a child's profile (creates the profile if missing)", 4,
		func(cmd *cobra.Command, args []string) error {
			v := args[3]
			_, err := relay(args[0], agent.Job{Op: agent.OpSet, Args: agent.JobArgs{Profile: args[1], Var: args[2], Value: &v}})
			return err
		})
}

// parent unset <agent> <profile> <VAR>
func createUnsetCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "unset <agent> <profile> <VAR>", "Remove one variable from a child's profile", 3,
		func(cmd *cobra.Command, args []string) error {
			_, err := relay(args[0], agent.Job{Op: agent.OpUnset, Args: agent.JobArgs{Profile: args[1], Var: args[2]}})
			return err
		})
}

// parent delete <agent> <profile>
func createDeleteCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "delete <agent> <profile>", "Delete a profile on a child", 2,
		func(cmd *cobra.Command, args []string) error {
			_, err := relay(args[0], agent.Job{Op: agent.OpDelete, Args: agent.JobArgs{Profile: args[1]}})
			return err
		})
}
