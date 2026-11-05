package tui

import (
	"errors"
	"io"

	"github.com/atotto/clipboard"
	"github.com/muesli/termenv"
)

// ErrCopiedViaTerminal is returned by SystemCopy when the system clipboard was
// unavailable and the text was sent with the OSC 52 escape instead; whether
// it arrived depends on the terminal.
var ErrCopiedViaTerminal = errors.New("sent to the terminal clipboard (OSC 52)")

// SystemCopy returns a copy function that uses the system clipboard and,
// when that is unavailable (no xclip/xsel/wl-copy, SSH session), falls back
// to the OSC 52 terminal escape written to term (the TUI's stderr).
func SystemCopy(term io.Writer) func(string) error {
	return func(text string) error {
		if err := clipboard.WriteAll(text); err == nil {
			return nil
		}
		termenv.NewOutput(term).Copy(text)
		return ErrCopiedViaTerminal
	}
}
