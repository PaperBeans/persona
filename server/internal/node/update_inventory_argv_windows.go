package node

import (
	"errors"

	"golang.org/x/sys/windows"
)

// updateProcessArgv reads the same authoritative command line as the process inventory's PEB scan.
func updateProcessArgv(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, errors.New("invalid process id")
	}
	p, err := readWinProc(uint32(pid), nil)
	if err != nil {
		return nil, err
	}
	if len(p.argv) == 0 {
		return nil, errors.New("incomplete process arguments")
	}
	return p.argv, nil
}

// processExecPath is the executable the kernel ran for pid (QueryFullProcessImageName).
func processExecPath(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("invalid process id")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n := uint32(len(buf))
		err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n)
		if err == nil {
			return windows.UTF16ToString(buf[:n]), nil
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || len(buf) >= 1<<15 {
			return "", err
		}
		buf = make([]uint16, len(buf)*2)
	}
}
