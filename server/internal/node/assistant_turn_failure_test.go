package node

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// #4669: a Claude assistant turn that failed is the session's turn error, so the apps show it failed and the push
// says so: a result flagged is_error, or one whose whole answer is an auth error line. A reply, an interrupt and a
// new turn are not.
func TestAssistantTurnFailureFromResult(t *testing.T) {
	cases := []struct {
		name, line, text, kind string
	}{
		{"auth error flagged is_error", `{"type":"result","subtype":"success","is_error":true,"result":"Invalid API key · Fix external API key"}`,
			"Invalid API key · Fix external API key", proto.TurnErrorUser},
		{"auth error only, not flagged", `{"type":"result","subtype":"success","is_error":false,"result":"Invalid API key · Fix external API key"}`,
			"Invalid API key · Fix external API key", proto.TurnErrorUser},
		{"upstream error flagged is_error", `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: 401 bad credentials sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
			"API Error: 401", ""},
		{"an ordinary reply", `{"type":"result","subtype":"success","is_error":false,"result":"Done: the build passes."}`, "", ""},
		{"a reply that talks about a key", `{"type":"result","subtype":"success","is_error":false,"result":"I rotated the invalid API key.\nThe deploy works again."}`, "", ""},
		{"an interrupt", `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"[Request interrupted by user]"}`, "", ""},
		{"is_error with no words", `{"type":"result","subtype":"error_during_execution","is_error":true,"result":""}`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := assistantNode(t)
			p := &assistantProc{id: "11111111-2222-3333-4444-555555555555", busy: true}
			n.readAssistant(p, strings.NewReader(`{"type":"stream_event","event":{"type":"message_start"}}`+"\n"+c.line+"\n"))
			p.mu.Lock()
			text, kind := p.turnError, p.turnErrorKind
			p.mu.Unlock()
			if c.text == "" {
				if text != "" {
					t.Fatalf("turn error = %q, want none", text)
				}
				return
			}
			if !strings.HasPrefix(text, c.text) || kind != c.kind {
				t.Fatalf("turn error = %q (%q), want %q… (%q)", text, kind, c.text, c.kind)
			}
			if strings.Contains(text, "sk-ant-") {
				t.Fatalf("turn error keeps a key: %q", text)
			}
			// The next turn starts clean.
			n.readAssistant(p, strings.NewReader(`{"type":"stream_event","event":{"type":"message_start"}}`+"\n"))
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.turnError != "" {
				t.Fatalf("a new turn kept the last one's error %q", p.turnError)
			}
		})
	}
}
