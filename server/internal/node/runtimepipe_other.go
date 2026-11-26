//go:build !windows

package node

import "context"

// listenPipe: runtimes dial the Unix socket here (runtimepipe_windows.go).
func (rh *RuntimeHost) listenPipe(context.Context) {}
