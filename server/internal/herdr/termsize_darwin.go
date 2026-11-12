//go:build darwin

package herdr

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// ttyNames caches the /dev entry of each terminal device seen (macOS names them ttysNNN).
var ttyNames struct {
	sync.Mutex
	byDev map[int32]string
}

// ttyOf is the device of the terminal pid runs on: the kernel's record of it (kern.proc.pid, e_tdev), named by the /dev
// entry with that device number. No process is started (the node asks every couple of seconds while it mirrors a
// pane, docs/integrations/terminal.md section 1), and the name found is checked against the number on every call.
func ttyOf(_ context.Context, pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("process %d: %w", pid, err)
	}
	if int(kp.Proc.P_pid) != pid {
		return "", fmt.Errorf("no process %d", pid)
	}
	dev := kp.Eproc.Tdev
	if dev == -1 || dev == 0 { // NODEV: no controlling terminal
		return "", fmt.Errorf("process %d has no terminal", pid)
	}
	ttyNames.Lock()
	name := ttyNames.byDev[dev]
	ttyNames.Unlock()
	if name != "" && isCharDev(name, dev) {
		return name, nil
	}
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "ttys") {
			continue
		}
		if p := "/dev/" + e.Name(); isCharDev(p, dev) {
			ttyNames.Lock()
			if ttyNames.byDev == nil || len(ttyNames.byDev) > 4096 {
				ttyNames.byDev = map[int32]string{}
			}
			ttyNames.byDev[dev] = p
			ttyNames.Unlock()
			return p, nil
		}
	}
	return "", fmt.Errorf("process %d: no /dev entry for its terminal", pid)
}

func isCharDev(path string, dev int32) bool {
	var st unix.Stat_t
	return unix.Stat(path, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFCHR && st.Rdev == dev
}
