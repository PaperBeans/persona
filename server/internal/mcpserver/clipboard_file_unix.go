//go:build darwin || linux

package mcpserver

import (
	"io/fs"
	"os"
	"syscall"
)

// openClipboardFile opens path for reading without following a symlink at its last part (one swapped in after the
// path was resolved) and without waiting on a named pipe.
func openClipboardFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// ownedByCaller: the file belongs to the user this process runs as.
func ownedByCaller(st fs.FileInfo) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && int(s.Uid) == os.Geteuid()
}
