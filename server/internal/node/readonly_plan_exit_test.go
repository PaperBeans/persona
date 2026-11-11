package node

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The manager rejects a read-only task's plan exit without a card. Claude must receive a tool denial,
// not a bare allow or a permission-mode change, so it can finish its report in the original mode.
func TestReadonlyPlanExitDenialKeepsClaudeReadOnly(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.answer = func(ix proto.Interaction) proto.Resolution {
		if ix.Kind != proto.IxPlan || ix.ToolName != "ExitPlanMode" {
			t.Errorf("plan exit interaction = %+v", ix)
		}
		approve := false
		return proto.Resolution{Approve: &approve, Feedback: "This task is read-only. Return findings to your supervisor."}
	}
	out, _ := runPermissionHook(t, n, hookInput{HookEventName: "PermissionRequest", SessionID: decidedSession,
		PermissionMode: "plan", ToolName: "ExitPlanMode", ToolInput: json.RawMessage(`{"plan":"Inspection complete."}`)})
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Output struct {
			Event    string                     `json:"hookEventName"`
			Decision map[string]json.RawMessage `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	d := response.Output.Decision
	if response.Output.Event != "PermissionRequest" || string(d["behavior"]) != `"deny"` ||
		!strings.Contains(string(d["message"]), "read-only") || len(d["updatedPermissions"]) != 0 || len(d["updatedInput"]) != 0 {
		t.Fatalf("read-only plan exit changed permissions or lost denial guidance: %s", raw)
	}
}
