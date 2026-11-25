//go:build !linux

package node

import "os"

// agyPresenceLocks: only Linux shows a process's open files without another program; elsewhere a herdr-core terminal's
// agy conversation stays unknown, and agyPresenceHeld still guards the companion without taking flock.
func agyPresenceLocks(pid int) string { return "" }

// agyPresenceLockState: non-Linux platforms have no readable kernel lock table, and taking flock races with a starting
// agy CLI (#14561). Report only whether the presence file exists so the caller uses the non-locking fallback.
func agyPresenceLockState(path string) agyLockState {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return agyLockMissing
	}
	return agyLockUnknown
}
