//go:build linux || darwin

package diskmap

import (
	"io/fs"
	"syscall"
)

// statOf reads what the walker needs of an entry: its device, inode, link count and allocated blocks (st_blocks is
// in 512-byte units on both systems). A symbolic link is never followed: info is an lstat.
func statOf(info fs.FileInfo) (fileStat, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileStat{}, false
	}
	dev := uint64(st.Dev) // int32 on darwin, uint64 on linux
	return fileStat{
		id:      fileID{dev: dev, ino: st.Ino},
		dev:     dev,
		links:   uint64(st.Nlink), // uint16 on darwin
		dir:     info.IsDir(),
		link:    info.Mode()&fs.ModeSymlink != 0,
		regular: info.Mode().IsRegular(),
		bytes:   st.Blocks * 512,
		mtime:   info.ModTime().Unix(),
	}, true
}

// volumeAt is the filesystem holding path, like df: free is what an ordinary user can still write, used what is
// taken (total − every free block).
func volumeAt(path string) (volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return volume{}, err
	}
	unit := blockUnit(&st)
	total := int64(st.Blocks) * unit
	return volume{total: total, free: int64(st.Bavail) * unit, used: total - int64(st.Bfree)*unit}, nil
}
