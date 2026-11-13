//go:build windows

package herdr

import (
	"context"
	"errors"
)

// ttySize: Windows has no ttys to ask (and herdr no terminal control there).
func ttySize(context.Context, int) (int, int, error) {
	return 0, 0, errors.New("herdr: terminal size is not readable on Windows")
}
