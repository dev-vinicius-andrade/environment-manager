//go:build windows

package hook

import "golang.org/x/sys/windows"

// documentsDir returns the user's Documents known folder (it may be redirected,
// for example to OneDrive), or "" when it cannot be resolved.
func documentsDir() string {
	p, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	if err != nil {
		return ""
	}
	return p
}
