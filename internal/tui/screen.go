package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Screen is one tab of the TUI. The App owns the frame (header with the tab
// list, status line, help bar, help overlay) and forwards everything else to
// the active screen. To add a screen (remotes, parents, ...), implement this
// interface and append it in New; see .claude/instructions/add-tui-screen.md.
type Screen interface {
	// Title is the tab label.
	Title() string
	// Update handles a message. Key messages arrive only when the App does not
	// consume them itself (ctrl+c always; q, esc, ?, tab, shift+tab while
	// Capturing is false). To end the program with output, return a command
	// that yields ExitMsg.
	Update(msg tea.Msg) (Screen, tea.Cmd)
	// View renders the screen body in exactly width x height cells (or less).
	View(width, height int) string
	// Capturing reports that the screen owns every key (text input, y/n
	// confirmation, notice): the App then does not treat q, esc, ?, tab as
	// global keys.
	Capturing() bool
	// Status is the one-line status shown above the help bar; isErr styles it
	// as an error.
	Status() (text string, isErr bool)
	// ShortHelp lists the key hints of the current mode for the bottom bar.
	ShortHelp() []Hint
	// FullHelp lists every key of the screen for the ? overlay.
	FullHelp() []Hint
}

// Hint is one key and what it does, for the help bar and overlay.
type Hint struct {
	Key  string
	Desc string
}

// ExitMsg ends the program; the command prints Result.Code on stdout.
type ExitMsg struct{ Result Result }

// exitCmd returns a command that ends the program with r.
func exitCmd(r Result) tea.Cmd {
	return func() tea.Msg { return ExitMsg{Result: r} }
}

// KeyClaimer is optionally implemented by a screen that wants one of the
// App's global keys (tab, shift+tab) in its current state, e.g. to switch
// sections of a detail pane. The App then forwards the key to the screen.
type KeyClaimer interface {
	ClaimsKey(key string) bool
}
