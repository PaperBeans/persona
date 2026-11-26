//go:build !unix

package manager

import "os"

// fileIdentity identifies an executable file on disk via size and mtime on non-Unix platforms.
type fileIdentity struct {
	Dev       uint64 `json:"dev,omitempty"`
	Ino       uint64 `json:"ino,omitempty"`
	HasDevIno bool   `json:"has_dev_ino,omitempty"`
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mod_time_ns"`
}

func fileIdentityOf(fi os.FileInfo) fileIdentity {
	return fileIdentity{Size: fi.Size(), ModTimeNS: fi.ModTime().UnixNano()}
}
