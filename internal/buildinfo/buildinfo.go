// Package buildinfo holds what the release build stamps into the binary
// (-ldflags "-X .../internal/buildinfo.Version=1.2.3 ...").
package buildinfo

import (
	"fmt"
	"runtime"
)

// Repo is the GitHub repository whose Releases `envmgr update` reads.
const Repo = "dev-vinicius-andrade/environment-manager"

// Version is the release version without the leading "v"; "dev" for a build
// that was not stamped (go run, go build, go install).
var Version = "dev"

// Commit and Date are stamped by the release build.
var (
	Commit = "none"
	Date   = "unknown"
)

// IsDev reports whether this binary has no release version.
func IsDev() bool { return Version == "" || Version == "dev" }

// Summary is the one-line description printed by `envmgr version`.
func Summary() string {
	return fmt.Sprintf("envmgr %s (commit %s, built %s, %s, %s/%s)",
		Version, Commit, Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
