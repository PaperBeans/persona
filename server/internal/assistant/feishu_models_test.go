package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestFeishuModelCommands(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	ctx := context.Background()
	s, f := pickFixture(t)
	f.agentModels = map[string][]proto.AgentModel{"gw": {{ID: "m1"}, {ID: "m2"}}}
	var note string
	f.answerApp(AppFeishu, FeishuText, func(raw json.RawMessage) (any, error) {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &p)
		note = p.Text
		return map[string]any{}, nil
	})
	s.larkModule().SetTarget(botTarget{Kind: targetMain})
	said := 0
	command := func(text string) {
		t.Helper()
		note = ""
		before := len(sendOnces(f))
		said++
		botSay(t, s, f, FeishuFeatures{Main: true}, fmt.Sprintf("om_%d", said), text, "", "")
		if sent := len(sendOnces(f)) - before; sent != 0 || note == "" { // handled: answered with a note, nothing sent on
			t.Fatalf("%s: sent %d, note %q", text, sent, note)
		}
	}
	command("/modellist")
	if !strings.Contains(note, "m1") || !strings.Contains(note, "m2") {
		t.Fatalf("list: %q", note)
	}
	command("/modelchange absent")
	if _, ok := s.modelPickOf(ctx, "server", "main"); ok {
		t.Fatal("unknown model was saved")
	}
	command("/modelchange m2")
	pick, ok := s.modelPickOf(ctx, "server", "main")
	if !ok || pick.Launcher != "gw" || pick.Model != "m2" {
		t.Fatalf("pick: %+v %v; note %q", pick, ok, note)
	}
	p, err := send(t, s, "main", map[string]any{})
	if err != nil || p["model"] != "m2" {
		t.Fatalf("next message: %v %v", p, err)
	}
	s.larkModule().SetTarget(botTarget{Kind: targetAgent, MachineID: "server", SessionID: "agent"})
	command("/modelchange m1")
	pick, _ = s.modelPickOf(ctx, "server", "main")
	if pick.Model != "m2" {
		t.Fatalf("agent target changed main: %+v", pick)
	}
	s.larkModule().SetTarget(botTarget{Kind: targetPersona, MachineID: "server", SessionID: "side"})
	command("/modelchange m1")
	pick, _ = s.modelPickOf(ctx, "server", "main")
	if pick.Model != "m2" {
		t.Fatalf("side target changed main: %+v", pick)
	}
}
