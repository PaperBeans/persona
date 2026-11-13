//go:build !windows

package herdr

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ttySize is the window size of the terminal process pid runs on (ttyOf finds it: termsize_darwin.go, termsize_proc.go).
func ttySize(ctx context.Context, pid int) (cols, rows int, err error) {
	name, err := ttyOf(ctx, pid)
	if err != nil {
		return 0, 0, err
	}
	// Read only, and never as this process's controlling terminal; nothing is read from it, so the pane's programs
	// lose no input.
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", name, err)
	}
	if ws.Col == 0 || ws.Row == 0 {
		return 0, 0, fmt.Errorf("%s: no window size", name)
	}
	return int(ws.Col), int(ws.Row), nil
}

func isTTYPath(p string) bool {
	return strings.HasPrefix(p, "/dev/pts/") || strings.HasPrefix(p, "/dev/tty")
}
