//go:build windows

package model

import (
	"strings"

	"golang.org/x/sys/windows"
)

// Flags for GetFinalPathNameByHandle, as defined in fileapi.h. x/sys/windows
// exposes the function but not these names. Both are zero: the API's default.
const (
	fileNameNormalized = 0x0 // FILE_NAME_NORMALIZED: the normalized final name
	volumeNameDOS      = 0x0 // VOLUME_NAME_DOS: answer with a drive letter
)

// resolveExisting returns the final path of an existing file or directory as
// Windows itself sees it. GetFinalPathNameByHandle resolves everything that
// lets two spellings name one directory: symlinks, junctions and other mount
// points, 8.3 short names (RUNNER~1), and case. filepath.EvalSymlinks is not
// enough on Windows: since Go 1.23 it deliberately stops at mount points, so a
// path through a junction came back unresolved, or failed to resolve at all.
func resolveExisting(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	// FILE_FLAG_BACKUP_SEMANTICS is what lets CreateFile open a directory.
	// Zero desired access asks for a handle only: no read or write rights,
	// and the share flags never block anyone else's use of the path.
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)),
			fileNameNormalized|volumeNameDOS)
		if err != nil {
			return "", err
		}
		// A return larger than the buffer is the size it needs, not a length.
		if int(n) >= len(buf) {
			buf = make([]uint16, n+1)
			continue
		}
		return stripExtendedPrefix(windows.UTF16ToString(buf[:n])), nil
	}
}

// stripExtendedPrefix turns the extended-length form GetFinalPathNameByHandle
// answers in (\\?\C:\x, \\?\UNC\server\share\x) back into the ordinary one
// (C:\x, \\server\share\x), which is how git and users spell the same path.
func stripExtendedPrefix(p string) string {
	const unc, ext = `\\?\UNC\`, `\\?\`
	switch {
	case strings.HasPrefix(p, unc):
		return `\\` + p[len(unc):]
	case strings.HasPrefix(p, ext):
		return p[len(ext):]
	}
	return p
}
