package update

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockAndroidDir takes the publication lock file. The publisher script is Unix only; this keeps two hub processes
// from writing the directory at the same time.
func lockAndroidDir(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{}); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
		f.Close()
	}, nil
}
