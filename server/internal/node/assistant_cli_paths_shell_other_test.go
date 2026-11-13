//go:build !windows

package node

import (
	"os/exec"
	"testing"
)

// shellRun runs script with a POSIX sh ("$0" persona, no arguments) and returns its standard output. On Windows it is
// Git Bash started from this native process (assistant_cli_paths_shell_windows_test.go).
func shellRun(t *testing.T, script string) (string, error) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh: %v", err)
	}
	out, err := exec.Command(sh, "-c", script, "persona").Output()
	return string(out), err
}
