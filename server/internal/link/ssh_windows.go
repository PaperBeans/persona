//go:build windows

package link

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func sshProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
}
