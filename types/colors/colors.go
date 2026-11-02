package colors

import "github.com/fatih/color"

type DefaultColors struct {
	Success *color.Color
	Error   *color.Color
	Info    *color.Color
	White   *color.Color
	Gray    *color.Color
	Blue    *color.Color
}

var Default = DefaultColors{
	Success: color.New(color.FgGreen, color.Bold),
	Error:   color.New(color.FgRed, color.Bold),
	Info:    color.New(color.FgCyan, color.Bold),
	White:   color.New(color.FgHiWhite, color.Bold),
	Gray:    color.New(color.FgHiBlack, color.Bold),
	Blue:    color.New(color.FgBlue, color.Bold),
}
