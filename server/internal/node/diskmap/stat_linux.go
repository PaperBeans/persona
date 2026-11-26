package diskmap

import "syscall"

// blockUnit is the size of the blocks statfs counts in: the fragment size where the kernel reports one, as statvfs.
func blockUnit(st *syscall.Statfs_t) int64 {
	if st.Frsize > 0 {
		return st.Frsize
	}
	return st.Bsize
}
