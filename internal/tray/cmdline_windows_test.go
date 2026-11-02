//go:build windows

package tray

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

// The elevated command line must parse back to the same arguments.
func TestWindowsCommandLineRoundTrip(t *testing.T) {
	for _, args := range [][]string{
		ElevatedApplyArgs(`C:\Users\Jo Doe\AppData\Roaming\envmgr`, `C:\tmp dir\hosts`, "dev"),
		{"a", `C:\trailing slash\`, `quote"inside`, `two\\"x`, "", "tab\there", `\\server\share`},
	} {
		got, err := windows.DecomposeCommandLine("envmgr.exe " + WindowsCommandLine(args))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got[1:], args) {
			t.Fatalf("got %q want %q", got[1:], args)
		}
	}
}
