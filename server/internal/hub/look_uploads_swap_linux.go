//go:build linux

package hub

import "golang.org/x/sys/unix"

// swapLookDirs atomically exchanges newDir and existingDir so concurrent readers never observe the look missing.
func swapLookDirs(newDir, existingDir string) error {
	return unix.Renameat2(unix.AT_FDCWD, newDir, unix.AT_FDCWD, existingDir, unix.RENAME_EXCHANGE)
}
