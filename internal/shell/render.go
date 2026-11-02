package shell

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

// Render turns ops into code for shell k, one statement per line.
//
// Every name is re-checked with profile.ValidVar so no unvalidated name is ever
// embedded. Values are always quoted as literals for the target shell; values
// containing NUL are rejected everywhere (environment values cannot hold NUL).
func Render(k Kind, ops []Op) (string, error) {
	var line func(Op) (string, error)
	switch k {
	case Bash, Zsh:
		line = bashLine
	case Fish:
		line = fishLine
	case PowerShell:
		line = powershellLine
	case Cmd:
		line = cmdLine
	default:
		return "", fmt.Errorf("unsupported shell %q", k)
	}
	var b strings.Builder
	for _, o := range ops {
		if !profile.ValidVar(o.Name) {
			return "", fmt.Errorf("invalid variable name %q", o.Name)
		}
		if !o.Unset && strings.ContainsRune(o.Value, 0) {
			return "", fmt.Errorf("value of %s contains a NUL byte", o.Name)
		}
		l, err := line(o)
		if err != nil {
			return "", err
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// bash / zsh: single quotes are fully literal; a quote is written as '\”.
func bashLine(o Op) (string, error) {
	if o.Unset {
		return "unset " + o.Name, nil
	}
	return "export " + o.Name + "=" + BashQuote(o.Value), nil
}

// BashQuote quotes s as one literal POSIX shell word.
func BashQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fish: inside single quotes only \\ and \' are escapes.
func fishLine(o Op) (string, error) {
	if o.Unset {
		return "set -e " + o.Name, nil
	}
	return "set -gx " + o.Name + " " + FishQuote(o.Value), nil
}

// FishQuote quotes s as one literal fish word.
func FishQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(s) + "'"
}

// powershell: the output is pure ASCII and one statement per line, because
// `envmgr use x | Invoke-Expression` evaluates each line separately and Windows
// PowerShell decodes native output with the OEM code page. Printable ASCII goes
// in single quotes (” escapes a quote); every other character, including
// newlines and the typographic quotes PowerShell also treats as quotes, is
// emitted as [char]0xNNNN.
func powershellLine(o Op) (string, error) {
	if o.Unset {
		return "$env:" + o.Name + " = $null", nil
	}
	return "$env:" + o.Name + " = " + PowerShellQuote(o.Value), nil
}

// PowerShellQuote returns a single-line, ASCII-only PowerShell string expression
// that evaluates to s.
func PowerShellQuote(s string) string {
	parts := []string{}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, "'"+lit.String()+"'")
			lit.Reset()
		}
	}
	for _, r := range s {
		if r >= 0x20 && r <= 0x7e {
			if r == '\'' {
				lit.WriteString("''")
			} else {
				lit.WriteRune(r)
			}
			continue
		}
		flush()
		for _, u := range utf16.Encode([]rune{r}) {
			parts = append(parts, fmt.Sprintf("[char]0x%04X", u))
		}
	}
	flush()
	if len(parts) == 0 {
		return "''"
	}
	if !strings.HasPrefix(parts[0], "'") {
		// Make the left operand a string so + concatenates.
		parts = append([]string{"''"}, parts...)
	}
	return strings.Join(parts, " + ")
}

// cmd: the output is a batch script meant to be saved to a .cmd file and run in
// the current cmd session. Values are written unquoted with every cmd
// metacharacter caret-escaped and % doubled, so nothing in a value can start a
// new command. cmd cannot represent newlines in a set statement and decodes the
// file with the console code page, so values must be printable ASCII (or tab).
// Assumes delayed expansion is disabled (the cmd default).
func cmdLine(o Op) (string, error) {
	if o.Unset {
		return "@set " + o.Name + "=", nil
	}
	v, err := CmdQuote(o.Value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", o.Name, err)
	}
	return "@set " + o.Name + "=" + v, nil
}

// CmdQuote escapes s for the value part of an unquoted `set NAME=value` line in
// a batch file.
func CmdQuote(s string) (string, error) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '%':
			b.WriteString("%%")
		case strings.ContainsRune(`^&|<>()"`, r):
			b.WriteByte('^')
			b.WriteRune(r)
		case r == '\t' || (r >= 0x20 && r <= 0x7e):
			b.WriteRune(r)
		default:
			return "", fmt.Errorf("value contains %q, which cmd cannot represent safely (only printable ASCII is supported; use powershell)", r)
		}
	}
	return b.String(), nil
}
