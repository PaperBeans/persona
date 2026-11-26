//go:build windows

package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"

	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"

	"github.com/nwverse/persona/server/internal/acceptloop"
)

// listenPipe also serves runtimes on a named pipe. Node.js on Windows dials a path as a named pipe and cannot reach
// the Unix socket at rh.path (connect EACCES), so a companion the node starts (agybridge.go, opencodebridge.go,
// paseobridge.go) never connected and its launcher stayed the plain CLI's. Runtimes are told the pipe (dialPath).
//
// The name is random for each run and handed only to processes the node starts, after the node owns the pipe:
// another account cannot create it first and wait for a runtime to dial it. Only this account may open it or add an
// instance, remote clients are refused (go-winio sets PIPE_REJECT_REMOTE_CLIENTS), and the handles are not
// inheritable, so no process the node starts keeps a connection open (#7765). The other end's process stays unknown
// (peerPID), as on the socket here. Without the pipe, runtimes are told the socket, as before.
func (rh *RuntimeHost) listenPipe(ctx context.Context) {
	name, err := runtimePipeName()
	if err != nil {
		rh.logger.Printf("runtime pipe: %v; runtimes on Node.js cannot connect", err)
		return
	}
	sd, err := currentUserOnlySDDL()
	if err != nil {
		rh.logger.Printf("runtime pipe: %v; runtimes on Node.js cannot connect", err)
		return
	}
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: sd})
	if err != nil {
		rh.logger.Printf("runtime pipe: %v; runtimes on Node.js cannot connect", err)
		return
	}
	rh.pipe = name
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go acceptloop.Run(ctx, ln, rh.logger.Printf, func(c net.Conn) { go rh.serve(ctx, c) })
}

// runtimePipeName is a fresh pipe name: \\.\pipe\persona-runtime- and 16 random hex digits.
func runtimePipeName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("pipe name: %w", err)
	}
	return `\\.\pipe\persona-runtime-` + hex.EncodeToString(b), nil
}

// currentUserOnlySDDL is a protected DACL that gives this process's account, and nobody else, full access.
func currentUserOnlySDDL() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	return "D:P(A;;GA;;;" + u.User.Sid.String() + ")", nil
}
