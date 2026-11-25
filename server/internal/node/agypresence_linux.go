package node

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// agyProcLocksPath is the Linux kernel lock table inspected by agyPresenceLockState; tests may override it.
var agyProcLocksPath = "/proc/locks"

// agyPresenceLocks reads the process's open files from /proc.
func agyPresenceLocks(pid int) string {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if target, err := os.Readlink(filepath.Join(dir, e.Name())); err == nil {
			files = append(files, target)
		}
	}
	return agyPresenceConversation(files)
}

// agyPresenceLockState tests presence/<conversation>.lock without taking flock: it stats the lock file and matches its
// (major, minor, inode) against /proc/locks (#14561). If /proc/locks cannot be read, it returns agyLockUnknown so the
// caller falls back to transcript activity instead of calling flock.
func agyPresenceLockState(path string) agyLockState {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return agyLockMissing
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return agyLockUnknown
	}
	data, err := os.ReadFile(agyProcLocksPath)
	if err != nil {
		return agyLockUnknown
	}
	if procLocksHasDevIno(data, unix.Major(uint64(st.Dev)), unix.Minor(uint64(st.Dev)), uint64(st.Ino)) {
		return agyLockHeld
	}
	return agyLockFree
}
