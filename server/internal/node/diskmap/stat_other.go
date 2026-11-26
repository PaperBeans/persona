//go:build !linux && !darwin && !windows

package diskmap

import (
	"errors"
	"io/fs"
)

// statOf: the usage map is described on macOS, Linux and Windows only.
func statOf(fs.FileInfo) (fileStat, bool) { return fileStat{}, false }

func volumeAt(string) (volume, error) { return volume{}, errors.ErrUnsupported }
