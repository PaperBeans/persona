//go:build !darwin && !linux

package mcpserver

import (
	"io/fs"
	"os"
)

// openClipboardFile opens path for reading. Windows has no O_NOFOLLOW; the path was resolved just before.
func openClipboardFile(path string) (*os.File, error) { return os.Open(path) }

// ownedByCaller: Windows has no owner id in the file's stat; the home directory's permissions decide.
func ownedByCaller(fs.FileInfo) bool { return true }
