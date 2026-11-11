package node

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// methodHerdr is a herdr socket answering by method; it records what was typed into panes.
func methodHerdr(t *testing.T, answer func(method string, params map[string]any) any) (*herdr.Client, func() []string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	ln, err := herdr.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var typed []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadBytes('\n')
			var req struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			_ = json.Unmarshal(line, &req)
			if req.Method == "agent.prompt" || req.Method == "pane.send_text" {
				mu.Lock()
				typed = append(typed, req.Params["text"].(string))
				mu.Unlock()
			}
			b, _ := json.Marshal(map[string]any{"id": req.ID, "result": answer(req.Method, req.Params)})
			_, _ = c.Write(append(b, '\n'))
			c.Close()
		}
	}()
	return &herdr.Client{Socket: sock}, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), typed...) }
}

// A message to a branch nothing has started yet (fork.go; a stopped agent takes none, #4754) starts it (claude
// --resume) and then types the message. When
// Claude came up with a dialog of its own (start returns as soon as herdr calls it blocked), the message used to be
// typed into the dialog — picking an option. It is refused, nothing typed, like a send to a running pane.
func TestResumedSessionWithADialogIsNotTypedInto(t *testing.T) {
	const id = "dddddddd-0000-0000-0000-000000000004"
	dialog := screenFixture(t, "claude-2.1.312-model-picker-162.ansi")
	h, typed := methodHerdr(t, func(method string, _ map[string]any) any {
		switch method {
		case "agent.list", "workspace.list":
			return map[string]any{"agents": []any{}, "workspaces": []any{}}
		case "workspace.create", "tab.create":
			return map[string]any{"tab": map[string]any{"tab_id": "w9:t1"}, "root_pane": map[string]any{"pane_id": "w9:p1"}}
		case "pane.read":
			return map[string]any{"read": map[string]any{"text": dialog}}
		case "agent.get":
			return map[string]any{"agent": map[string]any{"pane_id": "w9:p1", "agent": "claude", "agent_status": proto.StatusBlocked}}
		}
		return map[string]any{}
	})
	n, home := testNode(t)
	n.hosts, n.dir, n.panes = herdrHosts(h), filepath.Join(home, ".persona"), map[string]string{}
	n.files, n.modes, n.metas, n.bgScans = map[string]*transcript.File{}, map[string]sessionModes{}, map[string]quickMeta{}, map[string]*bgScan{}
	n.state.put(id, sessionState{Cwd: home, Launcher: "claude"})
	n.freshForks = map[string]bool{id: true}
	err := n.send(context.Background(), id, "部署到线上", nil, sendOpts{})
	if code, _ := refusalOf(t, err); code != "dialog_open" {
		t.Fatalf("send: %v", err)
	}
	if got := typed(); len(got) != 0 {
		t.Fatalf("typed into the dialog: %q", got)
	}
}
