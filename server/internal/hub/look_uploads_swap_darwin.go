//go:build darwin

package hub

import "golang.org/x/sys/unix"

// swapLookDirs atomically exchanges newDir and existingDir so concurrent readers never observe the look missing.
func swapLookDirs(newDir, existingDir string) error {
	return unix.RenamexNp(newDir, existingDir, unix.RENAME_SWAP)
}
