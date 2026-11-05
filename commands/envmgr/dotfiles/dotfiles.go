// Package dotfiles implements `envmgr dotfiles`: clone a dotfiles repository
// and link its files into place from the manifest in its .dotfiles.config.json.
package dotfiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	idot "github.com/dev-vinicius-andrade/environment-manager/internal/dotfiles"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/types"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	dir           string // --dir: the repository (default: the registered one)
	home          string // --home: what ~ means (default: the user's home)
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) {
	helpers.CobraHelper.ShowHelp(cmd, &types.CobraHelpOptions{Title: "envmgr dotfiles"})
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "dotfiles",
		Short: "Clone a dotfiles repository and link its files into place",
		Long: `Your dotfiles repository stays the source of truth; envmgr clones it and links
its files to where each tool expects them. What goes where is the "links"
section of the repository's .dotfiles.config.json (other keys are ignored):

  "links": [
    { "src": "nvim",        "dest": "~/.config/nvim" },
    { "src": ".zshrc",      "dest": "~/.zshrc" },
    { "src": "hyprland",    "dest": "$HOME/.config/hypr", "os": ["linux"] }
  ]

  envmgr dotfiles clone <source> [dest]   git clone (default dest ~/dotfiles) and register it
  envmgr dotfiles register <dir>          use a repository you already have
  envmgr dotfiles status [--check]        what is linked, missing or in the way
  envmgr dotfiles link [--dry-run|--copy] link everything (backs up what is in the way)
  envmgr dotfiles unlink [--dry-run]      remove only what envmgr created
  envmgr dotfiles pull                    git pull --ff-only, then link

A file already at a destination is never deleted: it is moved to
<dest>.envmgr-bak-<timestamp>, and unlink puts it back. On Windows, symlinks
need Developer Mode or an elevated shell (envmgr never elevates itself);
without them use --copy.`,
		Run: c.runCommand,
	}
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

func (c *commandDefinition) defineFlags() {
	c.cmd.PersistentFlags().StringVar(&c.dir, "dir", "", "dotfiles repository (default: the one registered with clone/register)")
	c.cmd.PersistentFlags().StringVar(&c.home, "home", "", "what ~ means in destinations (default: your home directory)")
}

func leaf(parent *cobra.Command, use, short string, args cobra.PositionalArgs, run func(cmd *cobra.Command, args []string) error) *cobra.Command {
	command := &cobra.Command{Use: use, Short: short, Args: args, SilenceUsage: true, SilenceErrors: true, RunE: run}
	helpers.CobraHelper.AddCommandToParent(command, parent)
	return command
}

func (c *commandDefinition) store() (idot.Store, error) {
	d, err := profile.DefaultDir()
	return idot.Store{Dir: d}, err
}

func (c *commandDefinition) homeDir() (string, error) {
	if c.home != "" {
		return filepath.Abs(c.home)
	}
	return os.UserHomeDir()
}

// repoDir returns the repository to work on: --dir, else the registered one.
func (c *commandDefinition) repoDir(s idot.Store) (string, error) {
	if c.dir != "" {
		return filepath.Abs(c.dir)
	}
	cfg, ok, err := s.LoadConfig()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("no dotfiles repository registered: run `envmgr dotfiles clone <source>` or `envmgr dotfiles register <dir>` (or pass --dir)")
	}
	return cfg.Dir, nil
}

// plan loads the manifest of the working repository and resolves it.
func (c *commandDefinition) plan(s idot.Store) (dir, home string, items []idot.Item, applied idot.Applied, err error) {
	if dir, err = c.repoDir(s); err != nil {
		return
	}
	if home, err = c.homeDir(); err != nil {
		return
	}
	m, err := idot.LoadManifest(dir)
	if err != nil {
		return
	}
	if applied, err = s.LoadApplied(); err != nil {
		return
	}
	items, err = idot.BuildPlan(dir, m, runtime.GOOS, home, nil, applied)
	return
}

func (c *commandDefinition) defineSubCommands() {
	var cloneLink bool
	clone := leaf(c.cmd, "clone <source> [dest]", "git clone a dotfiles repository and register it", cobra.RangeArgs(1, 2), func(cmd *cobra.Command, args []string) error {
		home, err := c.homeDir()
		if err != nil {
			return err
		}
		dest := filepath.Join(home, "dotfiles")
		if len(args) == 2 {
			if dest, err = filepath.Abs(args[1]); err != nil {
				return err
			}
		}
		s, err := c.store()
		if err != nil {
			return err
		}
		if err := idot.Clone(args[0], dest, cmd.ErrOrStderr()); err != nil {
			return err
		}
		if err := s.SaveConfig(idot.Config{Dir: dest, Source: args[0]}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: cloned into %s and registered\n", dest)
		if cloneLink {
			c.dir = ""
			return c.link(cmd, false, false)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "envmgr: next: `envmgr dotfiles status`, then `envmgr dotfiles link`")
		return nil
	})
	clone.Flags().BoolVar(&cloneLink, "link", false, "link right after cloning")

	leaf(c.cmd, "register <dir>", "use an existing dotfiles repository", cobra.ExactArgs(1), func(cmd *cobra.Command, args []string) error {
		abs, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		if _, err := idot.LoadManifest(abs); err != nil {
			return err
		}
		s, err := c.store()
		if err != nil {
			return err
		}
		if err := s.SaveConfig(idot.Config{Dir: abs}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "envmgr: using %s\n", abs)
		return nil
	})

	var check bool
	status := leaf(c.cmd, "status", "show what is linked, missing or in the way", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		s, err := c.store()
		if err != nil {
			return err
		}
		dir, _, items, _, err := c.plan(s)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "repository: %s\n", dir)
		bad := 0
		for _, it := range items {
			fmt.Fprintf(out, "%-12s %s -> %s%s\n", it.State, it.Link.Src, it.Dest, detail(it.Detail))
			if it.State != idot.StateOK && it.State != idot.StateCopied && it.State != idot.StateSkipped {
				bad++
			}
		}
		if check && bad > 0 {
			cmd.SilenceErrors = true
			return fmt.Errorf("%d entries are not in place", bad)
		}
		return nil
	})
	status.Flags().BoolVar(&check, "check", false, "exit 1 when any entry is not in place")

	var dryRun, copyMode bool
	link := leaf(c.cmd, "link", "link every entry of the manifest", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		return c.link(cmd, dryRun, copyMode)
	})
	link.Flags().BoolVar(&dryRun, "dry-run", false, "print what would happen, change nothing")
	link.Flags().BoolVar(&copyMode, "copy", false, "copy instead of symlink (for Windows without symlink rights)")

	var uDry, noRestore, force bool
	unlink := leaf(c.cmd, "unlink", "remove what envmgr linked and restore the backups", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		s, err := c.store()
		if err != nil {
			return err
		}
		applied, err := s.LoadApplied()
		if err != nil {
			return err
		}
		if len(applied.Links) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "envmgr: nothing to unlink")
			return nil
		}
		acts := idot.Unlink(&applied, !noRestore, force, idot.Options{DryRun: uDry})
		if !uDry {
			if err := s.SaveApplied(applied); err != nil {
				return err
			}
		}
		return report(cmd, acts)
	})
	unlink.Flags().BoolVar(&uDry, "dry-run", false, "print what would happen, change nothing")
	unlink.Flags().BoolVar(&noRestore, "no-restore", false, "do not put the backups back")
	unlink.Flags().BoolVar(&force, "force", false, "also remove copies made with --copy")

	leaf(c.cmd, "pull", "git pull --ff-only the repository, then link", cobra.NoArgs, func(cmd *cobra.Command, args []string) error {
		s, err := c.store()
		if err != nil {
			return err
		}
		dir, err := c.repoDir(s)
		if err != nil {
			return err
		}
		if err := idot.Pull(dir, cmd.ErrOrStderr()); err != nil {
			return err
		}
		return c.link(cmd, false, false)
	})
}

func (c *commandDefinition) link(cmd *cobra.Command, dryRun, copyMode bool) error {
	s, err := c.store()
	if err != nil {
		return err
	}
	_, _, items, applied, err := c.plan(s)
	if err != nil {
		return err
	}
	acts := idot.Apply(items, &applied, idot.Options{DryRun: dryRun, Copy: copyMode})
	if !dryRun {
		if err := s.SaveApplied(applied); err != nil {
			return err
		}
	}
	return report(cmd, acts)
}

func detail(d string) string {
	if d == "" {
		return ""
	}
	return "  (" + d + ")"
}

// report prints one line per action and fails when any entry failed.
func report(cmd *cobra.Command, acts []idot.Action) error {
	out := cmd.OutOrStdout()
	failed := 0
	for _, a := range acts {
		line := fmt.Sprintf("%-22s %s", a.Op, a.Item.Dest)
		if a.Backup != "" && !strings.Contains(a.Op, "restored") {
			line += "  (backup: " + a.Backup + ")"
		}
		if a.Note != "" {
			line += "  (" + a.Note + ")"
		}
		if a.Err != nil {
			failed++
			line += "  ERROR: " + a.Err.Error()
		}
		fmt.Fprintln(out, line)
	}
	if failed > 0 {
		cmd.SilenceErrors = true
		return fmt.Errorf("%d of %d entries failed", failed, len(acts))
	}
	return nil
}

// CreateCommand builds `envmgr dotfiles` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
