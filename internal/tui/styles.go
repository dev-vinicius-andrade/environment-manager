package tui

import (
	"io"
	"os"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Styles holds every lipgloss style the TUI uses. All styles come from one
// renderer bound to the TUI's output (stderr), so color detection does not
// depend on stdout, which is usually captured by the shell.
type Styles struct {
	r *lipgloss.Renderer

	Title       lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	HeaderInfo  lipgloss.Style
	Pane        lipgloss.Style
	PaneFocused lipgloss.Style
	PaneTitle   lipgloss.Style
	Selected    lipgloss.Style
	Active      lipgloss.Style
	Key         lipgloss.Style
	Dim         lipgloss.Style
	Error       lipgloss.Style
	Notice      lipgloss.Style
	HelpKey     lipgloss.Style
	HelpDesc    lipgloss.Style
}

// NewRenderer returns a renderer for w that honors NO_COLOR (any non-empty
// value disables colors).
func NewRenderer(w io.Writer) *lipgloss.Renderer {
	r := lipgloss.NewRenderer(w)
	if os.Getenv("NO_COLOR") != "" {
		r.SetColorProfile(termenv.Ascii)
	}
	return r
}

// plainRenderer renders no escape sequences at all (tests, non-terminals).
func plainRenderer() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	r.SetHasDarkBackground(true)
	return r
}

// NewStyles builds the styles from r. Colors are adaptive so the TUI reads on
// light and dark terminals; selection and the active profile also have text
// markers ("> ", "*") so nothing depends on color alone.
func NewStyles(r *lipgloss.Renderer) Styles {
	if r == nil {
		r = plainRenderer()
	}
	accent := lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#B4A2FF"}
	muted := lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#9A9A9A"}
	green := lipgloss.AdaptiveColor{Light: "#1D7A35", Dark: "#7EE29A"}
	red := lipgloss.AdaptiveColor{Light: "#B3261E", Dark: "#FF8A80"}
	border := lipgloss.AdaptiveColor{Light: "#BDBDBD", Dark: "#4A4A4A"}
	return Styles{
		r:           r,
		Title:       r.NewStyle().Bold(true).Foreground(accent),
		TabActive:   r.NewStyle().Bold(true).Underline(true),
		TabInactive: r.NewStyle().Foreground(muted),
		HeaderInfo:  r.NewStyle().Foreground(muted),
		Pane:        r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border),
		PaneFocused: r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent),
		PaneTitle:   r.NewStyle().Bold(true),
		Selected:    r.NewStyle().Bold(true).Foreground(accent),
		Active:      r.NewStyle().Foreground(green),
		Key:         r.NewStyle().Bold(true),
		Dim:         r.NewStyle().Foreground(muted),
		Error:       r.NewStyle().Foreground(red),
		Notice:      r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1),
		HelpKey:     r.NewStyle().Bold(true),
		HelpDesc:    r.NewStyle().Foreground(muted),
	}
}

// newInput returns a text input styled with s and a static cursor (no blink
// timer, so models stay deterministic in tests).
func (s Styles) newInput(prompt string, limit int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = prompt
	ti.CharLimit = limit
	ti.PromptStyle = s.r.NewStyle().Bold(true)
	ti.TextStyle = s.r.NewStyle()
	ti.PlaceholderStyle = s.Dim
	ti.CompletionStyle = s.Dim
	ti.Cursor.Style = s.r.NewStyle().Reverse(true)
	ti.Cursor.TextStyle = s.r.NewStyle()
	ti.Cursor.SetMode(cursor.CursorStatic)
	return ti
}
