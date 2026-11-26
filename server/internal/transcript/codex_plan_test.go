package transcript

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexExecutionPlanReceipts(t *testing.T) {
	var meta Meta
	seq := 0
	feed := func(typ string, payload map[string]any) []proto.Item {
		seq++
		raw, err := json.Marshal(map[string]any{"timestamp": "2026-11-22T10:00:00Z", "type": typ, "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		return ParseLine(seq, raw, &meta)
	}
	args := `{"plan":[{"step":"Read","status":"completed"},{"step":"Check","status":"in_progress"}]}`
	use := func(id, data string) []proto.Item {
		return feed("response_item", map[string]any{"type": "function_call", "name": "update_plan", "call_id": id, "arguments": data})
	}
	result := func(id string, output any) []proto.Item {
		return feed("response_item", map[string]any{"type": "function_call_output", "call_id": id, "output": output})
	}
	if items := use("a", args); len(items) != 0 || meta.Plan != nil {
		t.Fatal("unconfirmed plan appeared")
	}
	if items := result("unrelated", "Plan updated"); len(items) != 0 {
		t.Fatal("other tool changed the plan")
	}
	items := result("a", "Plan updated")
	if len(items) != 2 || items[0].ToolName != "TodoWrite" || items[0].ToolUseID != items[1].ToolUseID ||
		meta.Plan == nil || meta.Plan.Done != 1 || meta.Plan.Total != 2 || meta.Plan.Current != "Check" {
		t.Fatalf("accepted plan: %#v / %+v", items, meta.Plan)
	}
	folded := foldTaskSnapshots(items, map[string]proto.Item{items[0].ToolUseID: items[0]})
	if len(folded) != 1 || len(folded[0].Entries) != 2 || *folded[0].Entries[1].Status != "in_progress" {
		t.Fatalf("fold lost checklist: %+v", folded)
	}
	var event map[string]any
	_ = json.Unmarshal([]byte(args), &event)
	event["type"] = "plan_update"
	if duplicate := feed("event_msg", event); len(duplicate) != 0 {
		t.Fatal("native mirror repeated the same plan")
	}
	use("b", `{"plan":[]}`)
	if items := result("b", "Invalid request"); len(items) != 0 || meta.Plan == nil {
		t.Fatal("rejected update erased the plan")
	}
	use("clear", `{"plan":[]}`)
	if items := feed("event_msg", map[string]any{"type": "plan_update", "plan": []any{}}); len(items) != 2 || meta.Plan != nil {
		t.Fatal("native clear")
	}
	feed("event_msg", event)
	if items := result("clear", "Plan updated"); len(items) != 0 || meta.Plan == nil {
		t.Fatal("late wire acknowledgement overwrote a newer plan")
	}
	use("bad", `{"plan":[{"status":"in_progress"}]}`)
	if items := result("bad", map[string]any{"content": "Plan updated", "success": true}); len(items) != 0 || meta.Plan.Current != "Check" {
		t.Fatal("malformed update")
	}
}

// A cold history window can begin between the call and its native event.
func TestCodexPlanEventHasStableIdentityAcrossWindows(t *testing.T) {
	warm, cold := Meta{}, Meta{}
	ParseLine(7, []byte(`{"type":"response_item","payload":{"type":"function_call","name":"update_plan","call_id":"call-7","arguments":"{\"plan\":[{\"step\":\"Read\",\"status\":\"pending\"}]}"}}`), &warm)
	if len(warm.codexPlanCalls) != 1 {
		t.Fatal("warm reader did not observe the call")
	}
	event := []byte(`{"type":"event_msg","payload":{"type":"plan_update","plan":[{"step":"Read","status":"pending"}]}}`)
	fromStart, fromEvent := ParseLine(8, event, &warm), ParseLine(8, event, &cold)
	if len(fromStart) != 2 || len(fromEvent) != 2 || fromStart[0].ID != fromEvent[0].ID || fromStart[1].ID != fromEvent[1].ID {
		t.Fatalf("unstable native event: %+v vs %+v", fromStart, fromEvent)
	}
}

func TestCodexPlanMarkersAndPendingIDs(t *testing.T) {
	matchesAny := func(line []byte) bool {
		for _, m := range PlanMarkers {
			if bytes.Contains(line, m) {
				return true
			}
		}
		return false
	}
	hasPending := func(m *Meta, id string) bool {
		for _, p := range m.PlanPending() {
			if string(p) == id {
				return true
			}
		}
		return false
	}

	call1 := []byte(`{"timestamp":"2026-11-22T10:00:00Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","call_id":"call_codex_1","arguments":"{\"plan\":[{\"step\":\"Read\",\"status\":\"completed\"},{\"step\":\"Fix\",\"status\":\"in_progress\"}]}"}}`)
	call2 := []byte(`{"timestamp":"2026-11-22T10:00:01Z","type":"response_item","payload":{"type":"function_call","name":"functions.update_plan","call_id":"call_codex_2","arguments":"{\"plan\":[{\"step\":\"Read\",\"status\":\"completed\"},{\"step\":\"Fix\",\"status\":\"completed\"},{\"step\":\"Test\",\"status\":\"in_progress\"}]}"}}`)
	native2 := []byte(`{"timestamp":"2026-11-22T10:00:02Z","type":"event_msg","payload":{"type":"plan_update","plan":[{"step":"Read","status":"completed"},{"step":"Fix","status":"completed"},{"step":"Test","status":"in_progress"}]}}`)

	claudeDesktop := []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"p1","name":"` + claudeDesktopPlan + `","input":{}}]}}`)

	for name, line := range map[string][]byte{
		"claude_desktop":        claudeDesktop,
		"update_plan":           call1,
		"functions.update_plan": call2,
		"plan_update":           native2,
	} {
		if !matchesAny(line) {
			t.Errorf("PlanMarkers missed %s line", name)
		}
	}

	var m Meta
	ParseLine(1, call1, &m)
	if !hasPending(&m, "call_codex_1") {
		t.Fatalf("PlanPending after update_plan call = %q, want call_codex_1", m.PlanPending())
	}
	ParseLine(2, call2, &m)
	if !hasPending(&m, "call_codex_1") || !hasPending(&m, "call_codex_2") {
		t.Fatalf("PlanPending after functions.update_plan call = %q, want both ids", m.PlanPending())
	}

	out1 := []byte(`{"timestamp":"2026-11-22T10:00:02Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_codex_1","output":"Plan updated"}}`)
	ParseLine(3, out1, &m)
	if hasPending(&m, "call_codex_1") || !hasPending(&m, "call_codex_2") {
		t.Fatalf("PlanPending after output for call_codex_1 = %q, want only call_codex_2", m.PlanPending())
	}
	if m.Plan == nil || m.Plan.Done != 1 || m.Plan.Total != 2 || m.Plan.Current != "Fix" {
		t.Fatalf("Plan after call_codex_1 = %+v", m.Plan)
	}

	// Native plan_update confirms call_codex_2 and must clear it from PlanPending.
	ParseLine(4, native2, &m)
	if len(m.PlanPending()) != 0 {
		t.Fatalf("PlanPending after native plan_update = %q, want empty", m.PlanPending())
	}
	if m.Plan == nil || m.Plan.Done != 2 || m.Plan.Total != 3 || m.Plan.Current != "Test" {
		t.Fatalf("Plan after native plan_update = %+v", m.Plan)
	}

	// Rejected output must also clear the pending id.
	call3 := []byte(`{"timestamp":"2026-11-22T10:00:03Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","call_id":"call_codex_3","arguments":"{\"plan\":[]}"}}`)
	out3 := []byte(`{"timestamp":"2026-11-22T10:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_codex_3","output":"Invalid request"}}`)
	ParseLine(5, call3, &m)
	if !hasPending(&m, "call_codex_3") {
		t.Fatalf("PlanPending after call_codex_3 = %q, want call_codex_3", m.PlanPending())
	}
	ParseLine(6, out3, &m)
	if len(m.PlanPending()) != 0 {
		t.Fatalf("PlanPending after rejected call_codex_3 = %q, want empty", m.PlanPending())
	}
}
