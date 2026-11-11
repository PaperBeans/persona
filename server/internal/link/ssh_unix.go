//go:build unix

package link

import (
	"syscall"

	"github.com/nwverse/persona/server/internal/osproc"
)

func sshProcAttr() *syscall.SysProcAttr {
	return osproc.SessionGroupAttr()
}
