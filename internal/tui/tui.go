// Package tui is envmgr's terminal UI (Bubble Tea).
//
// A child process cannot change its parent shell, so the TUI draws on stderr
// and, when the user activates or deactivates a profile, ends the program
// with Result.Code: the same shell code "envmgr use" / "envmgr deactivate"
// print, which the caller writes to stdout for the shell to evaluate
// (eval "$(envmgr tui)", envmgr tui --shell powershell | Invoke-Expression).
// When stdout is a terminal (nothing would evaluate the code) the TUI shows
// how to wrap the command instead of exiting with code.
//
// The App is a frame (header, tabs, status, help bar, help overlay) around a
// list of Screen values; v1 has one screen, Profiles.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	"github.com/dev-vinicius-andrade/environment-manager/internal/shell"
)

// Minimum size for the full two-pane layout; smaller terminals get one pane.
const (
	MinWidth  = 60
	MinHeight = 15

	// below this the TUI only shows a "too small" message.
	tinyWidth  = 24
	tinyHeight = 6
)

// Action is what the user chose when the program ended with output.
type Action string

const (
	ActionNone       Action = ""
	ActionActivate   Action = "activate"
	ActionDeactivate Action = "deactivate"
)

// Result is the outcome of a run. Code is empty unless the user activated or
// deactivated a profile; it is the only thing the command writes to stdout.
type Result struct {
	Action  Action
	Profile string
	Code    string
	// Message is a one-line confirmation for stderr (after the TUI closed).
	Message string
}

// Options configures the TUI.
type Options struct {
	Store *profile.Store
	// Shell is the syntax of the code produced on activate/deactivate.
	Shell shell.Kind
	// Lookup reads the calling shell's environment (shell.OSLookup).
	Lookup shell.LookupFunc
	// StdoutIsTerminal: stdout is not captured, so the TUI must not exit with
	// shell code; it shows how to wrap the command instead.
	StdoutIsTerminal bool
	// Copy puts text on the clipboard (nil: copying is unavailable).
	Copy func(string) error
	// Styles; zero value means plain (no colors), as used by tests.
	Styles *Styles
	// Command is how the user invokes envmgr in one-liners (default "envmgr").
	Command string
}

// env is what every screen shares.
type env struct {
	store       *profile.Store
	kind        shell.Kind
	lookup      shell.LookupFunc
	stdoutTTY   bool
	copy        func(string) error
	styles      Styles
	command     string
	activeName  string
	activeState shell.State
}

// App is the root Bubble Tea model.
type App struct {
	env      *env
	screens  []Screen
	active   int
	width    int
	height   int
	showHelp bool
	result   Result
	done     bool
}

// New builds the TUI model. It reads the store synchronously so the first
// frame (and tests) see the profiles immediately.
func New(o Options) *App {
	e := &env{
		store:     o.Store,
		kind:      o.Shell,
		lookup:    o.Lookup,
		stdoutTTY: o.StdoutIsTerminal,
		copy:      o.Copy,
		command:   o.Command,
	}
	if e.kind == "" {
		e.kind = shell.Detect()
	}
	if e.lookup == nil {
		e.lookup = shell.OSLookup
	}
	if e.command == "" {
		e.command = "envmgr"
	}
	if o.Styles != nil {
		e.styles = *o.Styles
	} else {
		e.styles = NewStyles(nil)
	}
	e.activeState = shell.ReadState(e.lookup)
	e.activeName = e.activeState.Profile
	return &App{
		env: e,
		// Extension point: append further screens (remotes, parents) here.
		screens: []Screen{newProfilesScreen(e)},
	}
}

// Result returns the outcome once the program has ended.
func (a *App) Result() Result { return a.result }

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil
	case ExitMsg:
		a.result = msg.Result
		a.done = true
		return a, tea.Quit
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return a.quit()
		}
		if a.showHelp {
			a.showHelp = false
			return a, nil
		}
		if !a.screens[a.active].Capturing() {
			switch msg.String() {
			case "q", "esc":
				return a.quit()
			case "?":
				a.showHelp = true
				return a, nil
			case "tab":
				a.active = (a.active + 1) % len(a.screens)
				return a, nil
			case "shift+tab":
				a.active = (a.active + len(a.screens) - 1) % len(a.screens)
				return a, nil
			}
		}
	}
	s, cmd := a.screens[a.active].Update(msg)
	a.screens[a.active] = s
	return a, cmd
}

// quit ends the program without output.
func (a *App) quit() (tea.Model, tea.Cmd) {
	a.result = Result{}
	a.done = true
	return a, tea.Quit
}

func (a *App) size() (int, int) {
	w, h := a.width, a.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return w, h
}

// View implements tea.Model.
func (a *App) View() string {
	if a.done {
		return ""
	}
	st := a.env.styles
	w, h := a.size()
	if w < tinyWidth || h < tinyHeight {
		return block(wrapLines(fmt.Sprintf("envmgr: terminal too small (%dx%d). Enlarge it or press q.", w, h), w), w, h)
	}
	scr := a.screens[a.active]

	header := a.header(w)
	statusText, isErr := scr.Status()
	statusStyle := st.Dim
	if isErr {
		statusStyle = st.Error
	}
	status := pad(statusStyle.Render(fit(statusText, w)), w)
	helpBar := a.helpBar(scr, w)

	bodyH := h - 3
	var body string
	if a.showHelp {
		body = a.helpOverlay(scr, w, bodyH)
	} else {
		body = scr.View(w, bodyH)
	}
	return strings.Join([]string{header, body, status, helpBar}, "\n")
}

func (a *App) header(w int) string {
	st := a.env.styles
	left := st.Title.Render("envmgr")
	for i, s := range a.screens {
		if i == a.active {
			left += "  " + st.TabActive.Render(s.Title())
		} else {
			left += "  " + st.TabInactive.Render(s.Title())
		}
	}
	active := a.env.activeName
	if active == "" {
		active = "none"
	}
	right := st.HeaderInfo.Render(fmt.Sprintf("active: %s  shell: %s", active, a.env.kind))
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return pad(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (a *App) helpBar(scr Screen, w int) string {
	st := a.env.styles
	render := func(h Hint) string { return st.HelpKey.Render(h.Key) + " " + st.HelpDesc.Render(h.Desc) }
	// "? help  q quit" always stays visible; screen hints that do not fit
	// are dropped whole (the ? overlay lists everything).
	suffix := ""
	if !scr.Capturing() {
		suffix = render(Hint{"?", "help"}) + "  " + render(Hint{"q", "quit"})
	}
	room := w - lipgloss.Width(suffix)
	if suffix != "" {
		room -= 2
	}
	line := ""
	for _, h := range scr.ShortHelp() {
		part := render(h)
		if line != "" {
			part = "  " + part
		}
		if lipgloss.Width(line)+lipgloss.Width(part) > room {
			break
		}
		line += part
	}
	if suffix != "" {
		if line != "" {
			line += "  "
		}
		line += suffix
	}
	return pad(line, w)
}

// activationHelp explains, in the help overlay, how activation reaches the
// shell.
func (a *App) activationHelp() string {
	return fmt.Sprintf("Activation: a program cannot change its parent shell, so the TUI draws on stderr and prints the activation code on stdout when it exits. Start it as: %s to let activate/deactivate change this shell. Keys q, esc and ctrl+c quit without printing anything.",
		WrapCommand(a.env.kind, a.env.command+" tui --shell "+string(a.env.kind)))
}

func (a *App) helpOverlay(scr Screen, w, h int) string {
	st := a.env.styles
	lines := wrapLines(a.activationHelp(), w)
	lines = append(lines, "", st.PaneTitle.Render("Keys")+st.Dim.Render("  (any key closes)"))
	all := append(scr.FullHelp(),
		Hint{"tab / shift+tab", "next / previous tab"},
		Hint{"?", "toggle this help"},
		Hint{"q, esc, ctrl+c", "quit without output"},
	)
	kw := 0
	for _, hn := range all {
		if l := lipgloss.Width(hn.Key); l > kw {
			kw = l
		}
	}
	for _, hn := range all {
		lines = append(lines, "  "+st.HelpKey.Render(pad(hn.Key, kw))+"  "+st.HelpDesc.Render(hn.Desc))
	}
	return block(lines, w, h)
}

// WrapCommand returns the one-liner that makes shell k evaluate the stdout
// of cmdline (which must already contain --shell when needed).
func WrapCommand(k shell.Kind, cmdline string) string {
	switch k {
	case shell.Fish:
		return cmdline + " | source"
	case shell.PowerShell:
		return cmdline + " | Invoke-Expression"
	case shell.Cmd:
		return cmdline + ` > "%TEMP%\envmgr.cmd" && "%TEMP%\envmgr.cmd"`
	default:
		return `eval "$(` + cmdline + `)"`
	}
}
