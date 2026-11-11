package assistant

import (
	"context"
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestModelCodexAgentCategoryAcceptsExplicitEffort(t *testing.T) {
	s, f, _ := modelService(t)
	f.mu.Lock()
	f.launcherList = []proto.Launcher{{Name: "codex", Agent: "codex"}}
	f.mu.Unlock()
	r := modelRequest(s, http.MethodPatch, "agent.new", `{"revision":0,"provider":"launcher.codex","model":"specific","effort":"high"}`)
	if r.Code != http.StatusOK {
		t.Fatalf("save: %d %s", r.Code, r.Body.String())
	}
	ctx := context.Background()
	b, err := s.resolveModel(ctx, "agent.new")
	if err != nil || b.Provider != "launcher.codex" || b.Model != "specific" || b.Effort != "high" {
		t.Fatalf("saved binding: %+v, %v", b, err)
	}
	if inherited, err := s.resolveModel(ctx, "inbox.agent"); err != nil || inherited != b {
		t.Fatalf("child did not inherit the Agents binding: %+v, %v", inherited, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.Method == assistantwire.DeviceLaunchersModelCheck {
			return
		}
	}
	t.Fatal("saved binding without exercising online launcher validation")
}
