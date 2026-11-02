package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// maskedValue is what a hidden value looks like (same as "profile show").
const maskedValue = "********"

// printable makes a stored value safe to draw: control characters (newlines,
// tabs, ESC sequences that could drive the terminal) are shown escaped.
func printable(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			fmt.Fprintf(&b, `\x%02x`, r)
		case !unicode.IsPrint(r) && r != ' ':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fit truncates s (which may contain styling) to w cells, adding "~" when cut.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "~")
}

// pad truncates or right-pads s to exactly w cells.
func pad(s string, w int) string {
	s = fit(s, w)
	if n := w - ansi.StringWidth(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// block makes lines exactly w cells wide and h lines tall.
func block(lines []string, w, h int) string {
	if h <= 0 || w <= 0 {
		return ""
	}
	out := make([]string, h)
	for i := 0; i < h; i++ {
		if i < len(lines) {
			out[i] = pad(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

// window returns the [start, end) range of n items to show in rows lines so
// that cursor stays visible.
func window(n, cursor, rows int) (int, int) {
	if rows <= 0 || n == 0 {
		return 0, 0
	}
	if n <= rows {
		return 0, n
	}
	start := cursor - rows + 1
	if start < 0 {
		start = 0
	}
	if start > n-rows {
		start = n - rows
	}
	return start, start + rows
}

// wrapLines breaks text into lines of at most w cells; words longer than w
// (such as a long command) are broken, never cut, so nothing is hidden.
func wrapLines(text string, w int) []string {
	if w <= 0 {
		return nil
	}
	return strings.Split(ansi.Wrap(text, w, ""), "\n")
}
