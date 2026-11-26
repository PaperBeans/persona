//go:build !linux && !darwin

package hub

import (
	"os"
	"path/filepath"
)

// swapLookDirs replaces existingDir with newDir on platforms without an atomic directory-exchange syscall.
func swapLookDirs(newDir, existingDir string) error {
	oldTmp := filepath.Join(filepath.Dir(existingDir), "."+filepath.Base(existingDir)+".old-"+randomHex(4))
	if err := os.Rename(existingDir, oldTmp); err != nil {
		return err
	}
	if err := os.Rename(newDir, existingDir); err != nil {
		_ = os.Rename(oldTmp, existingDir)
		return err
	}
	// Move the old directory back onto newDir so the caller's deferred cleanup removes it uniformly.
	if err := os.Rename(oldTmp, newDir); err != nil {
		_ = os.RemoveAll(oldTmp)
	}
	return nil
}
