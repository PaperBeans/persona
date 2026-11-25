//go:build !windows

package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestRemoteAgentMemoryRejectsFIFO(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)

	remoteHost := dialRuntime(t, n, map[string]any{
		"runtime": "pac", "name": "PAC", "launcher": "pac",
		"caps": map[string]any{"start": true, "send": true, "interrupt": true, "assistant": true, "remote_agent": true},
	})
	cwd := identity.AssistantDir(n.home)
	remoteHost.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			return map[string]any{"session": map[string]any{"session_id": "pac-fifo-1", "cwd": cwd, "status": "idle"}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "pac", "cwd": cwd, "text": "hello"}); err != nil {
		t.Fatal(err)
	}
	memDir := n.memoryDir(cwd)
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fifoPath := filepath.Join(memDir, "fifo.md")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	call := func(tool string, args map[string]any) proto.ToolResult {
		t.Helper()
		rawArgs, _ := json.Marshal(args)
		var res proto.ToolResult
		if err := remoteHost.peer.Call(context.Background(), "mcp.call", map[string]any{
			"name": tool, "args": json.RawMessage(rawArgs), "session_id": "pac-fifo-1", "assistant": true,
		}, &res); err != nil {
			t.Fatalf("mcp.call %s: %v", tool, err)
		}
		return res
	}

	if res := call("memory_read", map[string]any{"name": "fifo.md"}); !res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "regular file") {
		t.Fatalf("expected FIFO memory_read to fail promptly with regular file error, got: %+v", res)
	}
	if res := call("memory_write", map[string]any{"name": "fifo.md", "content": "nope"}); !res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "regular file") {
		t.Fatalf("expected FIFO memory_write to fail promptly with regular file error, got: %+v", res)
	}
}
