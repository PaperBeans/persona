//go:build windows

package node

import (
	"os"
	"os/exec"
	"testing"
)

// shellRun runs script the way assistantCLICommand's does: Git Bash started by this native Windows process with
// `-ic script persona`, crossing the argument boundary of #5487. The home is empty so no ~/.bashrc prints into the
// output. Returns its standard output.
func shellRun(t *testing.T, script string) (string, error) {
	t.Helper()
	bash := gitBash()
	if bash == "" {
		t.Skip("no Git Bash (Git for Windows)")
	}
	home := t.TempDir()
	cmd := exec.Command(bash, "-ic", script, "persona")
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	out, err := cmd.Output()
	return string(out), err
}
