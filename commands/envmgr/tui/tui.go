package tui

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
	tuiapp "github.com/dev-vinicius-andrade/environment-manager/internal/tui"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	shellName     string
}

// isTerminal reports whether v is an *os.File attached to a terminal.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok || f == nil {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// ShouldLaunch reports whether a bare "envmgr" should open the TUI: stdin
// and stderr must be terminals (stdout may be captured by eval).
func ShouldLaunch() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stderr)
}

// Run starts the TUI with the --shell value shellName ("" = auto-detect) on
// cmd's streams: the UI on stderr, keys from stdin (or the controlling
// terminal when stdin is redirected), and only the shell code of an
// activate/deactivate on stdout.
func Run(cmd *cobra.Command, shellName string) error {
	cmd.SilenceUsage = true
	kind, err := shell.Resolve(shellName)
	if err != nil {
		return err
	}
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	in, out, errOut := cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()

	opts := []tea.ProgramOption{tea.WithOutput(errOut)}
	if ctx := cmd.Context(); ctx != nil {
		opts = append(opts, tea.WithContext(ctx))
	}
	interactive := isTerminal(errOut)
	if _, isFile := errOut.(*os.File); isFile && !interactive {
		return errors.New("envmgr tui needs a terminal on stderr (stdout may be redirected)")
	}
	if interactive {
		// Default input: stdin when it is a terminal, else the controlling
		// terminal (Bubble Tea opens /dev/tty or CONIN$).
		opts = append(opts, tea.WithAltScreen())
	} else {
		// Non-file streams (tests): read keys from the given reader.
		opts = append(opts, tea.WithInput(in))
	}

	o := tuiapp.Options{
		Store:            store,
		Shell:            kind,
		Lookup:           shell.OSLookup,
		StdoutIsTerminal: isTerminal(out),
	}
	if interactive {
		r := tuiapp.NewRenderer(errOut)
		// Query the terminal (colors, background) before Bubble Tea takes
		// over the input, so the answers do not race with key presses.
		r.ColorProfile()
		r.HasDarkBackground()
		st := tuiapp.NewStyles(r)
		o.Styles = &st
		o.Copy = tuiapp.SystemCopy(errOut)
	}

	app := tuiapp.New(o)
	if _, err := tea.NewProgram(app, opts...).Run(); err != nil {
		return err
	}
	res := app.Result()
	if res.Code == "" {
		return nil
	}
	if _, err := io.WriteString(out, res.Code); err != nil {
		return err
	}
	if res.Message != "" {
		fmt.Fprintln(errOut, res.Message)
	}
	return nil
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	return Run(cmd, c.shellName)
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI for profiles",
		Long: `Open the interactive terminal UI: browse, create and edit profiles and
activate or deactivate them.

The UI is drawn on stderr. A program cannot change its parent shell, so
activate/deactivate end the UI and print the shell code on stdout, exactly
like "envmgr use" / "envmgr deactivate". Start it so the shell evaluates it:

  bash/zsh:    eval "$(envmgr tui)"
  fish:        envmgr tui --shell fish | source
  PowerShell:  envmgr tui --shell powershell | Invoke-Expression
  cmd:         envmgr tui --shell cmd > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"

When stdout is a terminal the UI shows the command to run (and can copy it)
instead of printing code. Quitting (q, esc, ctrl+c) prints nothing.
Running "envmgr" with no arguments in a terminal opens the same UI.`,
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
}

func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
