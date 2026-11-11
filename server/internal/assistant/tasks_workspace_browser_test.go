package assistant

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/proto"
)

// Opt-in browser fixture: real plan/settings HTTP handlers and dispatch, with a
// synthetic machine engine. All unrelated UI routes go to the repository mock.
// Run the mock hub on 18772 and this test with TASK_WORKSPACE_QA=1; point Vite's
// PERSONA_HUB_URL at http://127.0.0.1:18773. POST /_qa/stop ends the fixture.
func TestTaskWorkspaceBrowserHarness(t *testing.T) {
	if os.Getenv("TASK_WORKSPACE_QA") != "1" {
		t.Skip("opt-in isolated browser fixture")
	}
	const chat = "b2e4d6f8-0000-4000-8000-000000000001"
	s, f := workspaceTaskService(t)
	f.sessions["macbook"] = nil
	f.hello.Assistant.MachineID = "macbook"
	f.hello.Assistant.Machines = []string{"macbook"}
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "macbook", chat })
	raw := `{"tasks":[
		{"title":"Isolate the login fix","prompt":"Fix the example login","machine":"macbook","cwd":"/code/app","permission":"bypassPermissions","workspace":"new"},
		{"title":"Reuse the review checkout","prompt":"Update the example tests","machine":"macbook","cwd":"/code/app","permission":"bypassPermissions","workspace":{"existing":"/code/other"}},
		{"title":"Read the release notes","prompt":"Summarize the example release","machine":"macbook","cwd":"/code/app","readonly":true,"workspace":"none"}
	]}`
	if _, err := s.toolTaskPlan(context.Background(), chat, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("http://127.0.0.1:18772")
	fallback := httputil.NewSingleHostReverseProxy(target)
	done := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_qa/stop":
			done <- struct{}{}
			apierr.WriteJSON(w, map[string]bool{"stopped": true})
		case r.URL.Path == "/_qa/dispatch":
			f.mu.Lock()
			defer f.mu.Unlock()
			apierr.WriteJSON(w, map[string]any{"starts": f.starts, "worktrees": f.worktrees})
		case r.URL.Path == "/api/v1/workspaces":
			ws := qualifyWorkspace("macbook", proto.Workspace{ID: "ws-one", Source: "herdr", SourceID: "ws-one",
				Title: "Review checkout", Cwd: "/code/other", Branch: "fix/login", Available: true, Online: true})
			apierr.WriteJSON(w, proto.WorkspacesResponse{Workspaces: []proto.Workspace{ws}})
		case r.URL.Path == "/api/v1/assistant",
			strings.HasPrefix(r.URL.Path, "/api/v1/task-plans/"),
			strings.HasPrefix(r.URL.Path, "/api/v1/todos"),
			r.URL.Path == "/api/v1/assistant/conversations/"+chat+"/feed":
			r.Header.Set(RoleHeader, roleFull)
			s.ServeHTTP(w, r)
		default:
			fallback.ServeHTTP(w, r)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:18773")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	defer srv.Close()
	go srv.Serve(listener)
	t.Log("workspace plan/settings fixture ready at http://127.0.0.1:18773")
	select {
	case <-done:
	case <-time.After(30 * time.Minute):
		t.Fatal("browser fixture deadline exceeded")
	}
}
