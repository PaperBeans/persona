//go:build !windows

package update

import (
	"os"
	"syscall"
)

// lockAndroidDir takes the lock file android_apk_atomic.py publishes under (fcntl.flock: the same flock(2) lock).
func lockAndroidDir(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
