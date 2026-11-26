//go:build windows

package node

import (
	"strings"
	"testing"
	"time"

	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
)

// Node.js on Windows cannot reach the Unix socket, so the host also listens on a named pipe, tells the runtimes it
// starts that pipe, and serves a runtime that dials it as it serves one on the socket.
func TestRuntimePipeServesRuntimes(t *testing.T) {
	rh := sharedHost(t)
	if !strings.HasPrefix(rh.pipe, `\\.\pipe\persona-runtime-`) {
		t.Fatalf("pipe %q", rh.pipe)
	}
	if rh.dialPath() != rh.pipe {
		t.Fatalf("runtimes are told %q, not the pipe %q", rh.dialPath(), rh.pipe)
	}
	timeout := 5 * time.Second
	c, err := winio.DialPipe(rh.pipe, &timeout)
	if err != nil {
		t.Fatal(err)
	}
	rt := runtimeOn(t, c, dshHello())
	if p, _ := rt.helloR["protocol"].(float64); int(p) != RuntimeProtocol {
		t.Fatalf("hello over the pipe answered %v", rt.helloR)
	}
	waitFor(t, "the runtime on the pipe listed as ready", func() bool { return len(rh.reg.ready()) == 1 })
	if s := rh.reg.launchers(); len(s) != 1 || s[0].Name != "dsh" {
		t.Fatalf("launchers %+v", s)
	}
}

// Each host gets a pipe of its own, so two nodes (or a name someone made first) never meet.
func TestRuntimePipeNamesDiffer(t *testing.T) {
	a, b := sharedHost(t), sharedHost(t)
	if a.pipe == "" || a.pipe == b.pipe {
		t.Fatalf("pipes %q and %q", a.pipe, b.pipe)
	}
}

// The pipe's DACL is protected and allows this account alone.
func TestRuntimePipeOnlyThisAccount(t *testing.T) {
	sd, err := currentUserOnlySDDL()
	if err != nil {
		t.Fatal(err)
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if want := "D:P(A;;GA;;;" + u.User.Sid.String() + ")"; sd != want {
		t.Fatalf("SDDL %q, want %q", sd, want)
	}
}
