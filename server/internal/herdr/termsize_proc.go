//go:build !windows && !darwin

package herdr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ttyOf is the device of the terminal pid runs on: its stdin's from /proc (Linux), else what ps says (another Unix).
func ttyOf(ctx context.Context, pid int) (string, error) {
	if link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/0"); err == nil && isTTYPath(link) {
		return link, nil
	}
	out, err := exec.CommandContext(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", fmt.Errorf("ps: %w", err)
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" || strings.Trim(tty, "?") == "" || strings.ContainsAny(tty, " .") {
		return "", fmt.Errorf("process %d has no terminal", pid)
	}
	name := "/dev/" + tty
	if !isTTYPath(name) {
		return "", fmt.Errorf("process %d: not a terminal: %s", pid, name)
	}
	return name, nil
}
