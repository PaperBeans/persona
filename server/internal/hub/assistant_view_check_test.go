package hub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestAssistantViewCheckTrustedSession(t *testing.T) {
	_, n := conversationsHub(t)
	editSessions(n.m, func(list []proto.Session) []proto.Session {
		return append(list, proto.Session{SessionID: "terminal-example", Cwd: convDir, PaneID: "window:pane", Assistant: true})
	})
	invalid := "```persona-view\n{\"sections\":[]}\n```"
	valid := "```persona-view\n{\"fallback\":\"Complete\",\"sections\":[]}\n```"
	for _, tc := range []struct {
		name, session, message string
		block                  bool
	}{
		{"trusted-invalid", "trip-0002", invalid, true},
		{"trusted-valid", "trip-0002", valid, false},
		{"ordinary", "agent-0005", invalid, false},
		{"pane", "terminal-example", invalid, false},
		{"unknown", "unknown-example", invalid, false},
		{"plain", "trip-0002", "All complete", false},
		{"incomplete", "trip-0002", "```persona-view\n{\"fallback\":\"Complete\",\"sections\":[]}", true},
		{"bounded", "trip-0002", strings.Repeat("x", 1<<20) + "persona-view", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(assistantwire.ViewCheckRequest{SessionID: tc.session, Message: tc.message})
			out, err := n.m.onRequest(context.Background(), assistantwire.MethodViewCheck, raw)
			if err != nil {
				t.Fatal(err)
			}
			result, ok := out.(assistantwire.ViewCheckResponse)
			if !ok || (result.Reason != "") != tc.block {
				t.Fatalf("check result: %v", out)
			}
			if tc.block && (!strings.Contains(result.Reason, "这一轮") || !strings.Contains(result.Reason, "fallback") || strings.Contains(result.Reason, tc.message)) {
				t.Fatal("repair feedback did not provide the bounded template instructions")
			}
		})
	}
}
