//go:build windows

package diskmap

import (
	"io/fs"
	"syscall"

	"golang.org/x/sys/windows"
)

// The attributes that make an entry something the walker does not size as it is: a reparse point (a symbolic link,
// a junction, a mounted volume) is never followed, and a cloud placeholder whose data is fetched on access holds
// nothing here (it counts as 0).
const (
	attrReparsePoint      = windows.FILE_ATTRIBUTE_REPARSE_POINT
	attrRecallOnOpen      = 0x00040000 // FILE_ATTRIBUTE_RECALL_ON_OPEN
	attrRecallOnDataAcess = 0x00400000 // FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS
	attrOffline           = windows.FILE_ATTRIBUTE_OFFLINE
)

// statOf reads an entry on Windows: sizes are logical bytes (the allocation is not in the directory listing) and hard
// links are not told apart, so every file counts. There is one "device" (0): a mounted volume under a folder is a
// reparse point, which is never walked.
func statOf(info fs.FileInfo) (fileStat, bool) {
	d, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return fileStat{}, false
	}
	attrs := d.FileAttributes
	st := fileStat{
		links:   1,
		dir:     info.IsDir(),
		link:    attrs&attrReparsePoint != 0 || info.Mode()&fs.ModeSymlink != 0,
		regular: info.Mode().IsRegular(),
		bytes:   info.Size(),
		mtime:   info.ModTime().Unix(),
	}
	if st.dir {
		st.bytes = 0
	}
	if attrs&(attrRecallOnOpen|attrRecallOnDataAcess|attrOffline) != 0 {
		st.bytes = 0
	}
	return st, true
}

// volumeAt is the volume holding path: GetDiskFreeSpaceEx's numbers for the calling user.
func volumeAt(path string) (volume, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return volume{}, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return volume{}, err
	}
	return volume{total: int64(total), free: int64(avail), used: int64(total - free)}, nil
}
