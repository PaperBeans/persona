//go:build unix

package manager

import (
	"os"
	"syscall"
)

// fileIdentity uniquely identifies an executable file on disk via (dev, ino) on Unix plus size and mtime.
type fileIdentity struct {
	Dev       uint64 `json:"dev,omitempty"`
	Ino       uint64 `json:"ino,omitempty"`
	HasDevIno bool   `json:"has_dev_ino,omitempty"`
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mod_time_ns"`
}

func fileIdentityOf(fi os.FileInfo) fileIdentity {
	id := fileIdentity{Size: fi.Size(), ModTimeNS: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st != nil {
		id.Dev = uint64(st.Dev)
		id.Ino = uint64(st.Ino)
		id.HasDevIno = true
	}
	return id
}
