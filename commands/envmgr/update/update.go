// Package update implements `envmgr update`: self-update from GitHub Releases.
package update

import (
	stdctx "context"
	"fmt"
	"os"
	"strings"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/buildinfo"
	iupdate "github.com/dev-vinicius-andrade/environment-manager/internal/update"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	check         bool
	version       string
	pre           bool
	force         bool
	// client is replaced by tests.
	client *iupdate.Client
}

func token() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	client := c.client
	if client == nil {
		client = iupdate.NewClient(buildinfo.Repo, token())
	}
	out, errw := cmd.OutOrStdout(), cmd.ErrOrStderr()
	res, err := iupdate.Run(stdctx.Background(), iupdate.Options{
		Client:     client,
		Current:    buildinfo.Version,
		Version:    c.version,
		Prerelease: c.pre,
		Force:      c.force,
		CheckOnly:  c.check,
		Progress:   func(f string, a ...any) { fmt.Fprintf(errw, "envmgr: "+f+"\n", a...) },
	})
	if err != nil {
		return err
	}
	switch {
	case res.Updated:
		fmt.Fprintf(out, "updated envmgr %s -> %s (%s)\n", res.Current, res.Latest, res.Replaced)
		if n := strings.TrimSpace(res.Release.URL); n != "" {
			fmt.Fprintf(out, "release notes: %s\n", n)
		}
	case c.check:
		if res.Newer {
			fmt.Fprintf(out, "envmgr %s is available (you have %s): run `envmgr update`\n", res.Latest, res.Current)
		} else {
			fmt.Fprintf(out, "envmgr %s is the latest release; you are up to date\n", res.Latest)
		}
	default:
		fmt.Fprintf(out, "envmgr %s: %s (latest release: %s)\n", res.Current, res.Reason, res.Latest)
	}
	return nil
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "update",
		Short: "Update envmgr from the latest GitHub release",
		Long: `Download the latest release for this OS and architecture from GitHub, verify its
SHA-256 against the release's checksums.txt, check that the new binary runs and
reports the right version, then replace the running executable.

  envmgr update               install the latest stable release
  envmgr update --check       only say whether a newer release exists
  envmgr update --version 1.2.0   install that release (up or down)
  envmgr update --pre         consider pre-releases as "latest"
  envmgr update --force       reinstall, or install over a build made from source

If envmgr is a symlink, the real file is replaced and the link keeps working.
On Windows the old executable is renamed to envmgr.exe.old and removed by the
next update. envmgr never elevates itself: if the install directory is not
writable, reinstall somewhere you own or run the update from a shell that can
write there. Set GITHUB_TOKEN (or GH_TOKEN) to avoid GitHub's anonymous rate limit.`,
		Args: cobra.NoArgs,
		RunE: c.runCommand,
	}
	command.Flags().BoolVar(&c.check, "check", false, "only check for a newer release")
	command.Flags().StringVar(&c.version, "version", "", "install this release instead of the latest (for example 1.2.0)")
	command.Flags().BoolVar(&c.pre, "pre", false, "include pre-releases when looking for the latest")
	command.Flags().BoolVar(&c.force, "force", false, "install even when not newer, or over a source build")
	c.cmd = command
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

// CreateCommand builds `envmgr update` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{context: context, parentCommand: parentCommand}
	return command.createCommandDefinition()
}
