//go:build !windows

package model

import "path/filepath"

// resolveExisting returns the final path of an existing file or directory,
// with symlinks resolved - including macOS's /var -> /private/var. Windows
// needs a different call; see resolve_windows.go.
func resolveExisting(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
