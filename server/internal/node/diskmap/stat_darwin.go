package diskmap

import "syscall"

// blockUnit is the size of the blocks statfs counts in.
func blockUnit(st *syscall.Statfs_t) int64 { return int64(st.Bsize) }
