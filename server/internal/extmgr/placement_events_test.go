package extmgr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
)

// placement.pending reaches a running extension only when it subscribed to the topic and was explicitly granted
// the named-only tasks.schedule scope: todos.read and legacy hub_api are not enough.
func TestPlacementPendingEventsReachOnlyAuthorizedExtensions(t *testing.T) {
	for _, tc := range []struct {
		name, permissions string
		allowed           bool
	}{
		{"scheduler", `{"hub":["tasks.schedule"]}`, true},
		{"todos-reader", `{"hub":["todos.read"]}`, false},
		{"legacy-hub-api", `{"hub_api":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := extensionTestHub(t)
			mux := h.routes()
			token := h.cfg.Token
			dir := uiPackage(t)
			path := filepath.Join(dir, extension.ManifestFile)
			raw, _ := os.ReadFile(path)
			var manifest map[string]any
			_ = json.Unmarshal(raw, &manifest)
			var permissions map[string]any
			_ = json.Unmarshal([]byte(tc.permissions), &permissions)
			permissions["launcher_keys"] = true
			manifest["permissions"] = permissions
			manifest["contributes"].(map[string]any)["events"] = []any{map[string]any{
				"id":        "placement",
				"subscribe": []string{"placement.pending", "agent.finished"},
			}}
			raw, _ = json.Marshal(manifest)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			code, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", token, `{"source":"dir:`+dir+`"}`)
			if code != 200 {
				t.Fatalf("preview %d %v", code, pv)
			}
			code, out := call(t, mux, "POST", "/api/v1/extensions/-/install", token, fmt.Sprintf(`{"preview_id":%q,"permissions_digest":%q}`, pv["preview_id"], pv["permissions_digest"]))
			if code != 200 {
				t.Fatalf("install %d %v", code, out)
			}
			waitLong(t, "placement subscriber running", func() bool {
				p := h.extensions.get("example-ui")
				return p != nil && p.proc != nil && p.proc.Ready()
			})
			// Include extra untrusted fields in raw JSON to verify only opaque scheduling identifiers pass through.
			rawEv := json.RawMessage(`{"plan_id":"plan_sched_1","todo_id":"todo_sched_1","batch_size":3,"prompt":"secret prompt","title":"secret title"}`)
			if !h.AssistantSaid(assistantwire.EventPlacementPending, rawEv) {
				t.Fatal("placement.pending was not claimed by extmgr")
			}
			view := func() string {
				_, out := call(t, mux, "POST", "/api/v1/extensions/example-ui/-/ui/home", token, `{}`)
				return fmt.Sprint(out["view"])
			}
			waitEventQueuesEmpty(t, h)
			if !tc.allowed {
				h.extensions.emit("agent.finished", map[string]any{})
				waitLong(t, "sentinel event delivered", func() bool { return strings.Contains(view(), "value:1") })
				if v := view(); strings.Contains(v, "placement.pending") || strings.Contains(v, "plan_sched_1") {
					t.Fatalf("placement.pending reached an unauthorized extension: %s", v)
				}
				return
			}
			h.extensions.emit("agent.finished", map[string]any{})
			waitLong(t, "placement event delivered", func() bool { return strings.Contains(view(), "value:2") })
			v := view()
			for _, want := range []string{`<placement.pending `, `"plan_id":"plan_sched_1"`, `"todo_id":"todo_sched_1"`, `"batch_size":3`} {
				if !strings.Contains(v, want) {
					t.Fatalf("placement.pending lacks %s: %s", want, v)
				}
			}
			for _, leak := range []string{"secret prompt", "secret title", `"prompt"`, `"title"`} {
				if strings.Contains(v, leak) {
					t.Fatalf("placement.pending leaked %s: %s", leak, v)
				}
			}
		})
	}
}

func TestPlacementPendingEventsRejectInvalidPayloads(t *testing.T) {
	h := newTestManager(t)
	h.extensions = &extensions{}
	for _, raw := range []string{
		`{}`,
		`[]`,
		`null`,
		`{"plan_id":"p1","todo_id":"t1"}`,
		`{"plan_id":"p1","todo_id":"t1","batch_size":0}`,
		`{"plan_id":"","todo_id":"t1","batch_size":1}`,
		`{"plan_id":"p1","todo_id":"","batch_size":1}`,
	} {
		h.AssistantSaid(assistantwire.EventPlacementPending, json.RawMessage(raw))
	}
	q := &h.extensions.todoEvents
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running || len(q.pending) > 0 {
		t.Fatalf("an invalid placement.pending event was queued: %+v", q.pending)
	}
}
