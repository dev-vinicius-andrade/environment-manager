package current

import (
	"fmt"
	"io"
	"os"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	status        bool
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	name := os.Getenv(shell.EnvProfile)
	if name != "" {
		fmt.Fprintln(cmd.OutOrStdout(), name)
	}
	// The session mode goes to stderr (stdout stays the bare name for scripts):
	// always with --status, otherwise only when stderr is a terminal.
	if c.status || (name != "" && isTerminal(cmd.ErrOrStderr())) {
		fmt.Fprintln(cmd.ErrOrStderr(), Mode(shell.OSLookup))
	}
	return nil
}

// Mode describes how the session picks its profile: "(pinned)" when it chose
// one itself (use, TUI), "(following tray)" otherwise.
func Mode(lookup shell.LookupFunc) string {
	if shell.Pinned(lookup) {
		return "(pinned)"
	}
	return "(following tray)"
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "current",
		Short: "Print the profile active in the current shell",
		Long: `Print the name of the profile active in the current shell (ENVMGR_PROFILE).
Prints nothing, and still exits 0, when no profile is active.

On stderr (when it is a terminal, or always with --status) it also says whether
the shell is "(pinned)" (it ran "envmgr use" or activated from the TUI, so the
machine-wide selection is ignored until "envmgr follow") or "(following tray)".`,
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
	c.cmd.Flags().BoolVar(&c.status, "status", false, "also print (pinned) or (following tray) on stderr")
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
