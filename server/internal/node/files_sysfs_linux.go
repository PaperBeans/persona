package node

import "golang.org/x/sys/unix"

// Check the open descriptor, not the pathname: sysfs can be mounted or bind-mounted anywhere.
func fileReadIsSysfs(f fileReadHandle) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return false, err
	}
	return st.Type == unix.SYSFS_MAGIC, nil
}
