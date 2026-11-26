package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

type codexPlanCall struct {
	id    string
	todos json.RawMessage
}

// codexTodos maps the execution checklist to the same TodoWrite input the clients already replay and FoldWork preserves.
func codexTodos(raw json.RawMessage) json.RawMessage {
	var args struct {
		Plan []struct{ Step, Status string }
	}
	if json.Unmarshal(raw, &args) != nil || args.Plan == nil {
		return nil
	}
	type todo struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	list := make([]todo, 0, len(args.Plan))
	for _, step := range args.Plan {
		if strings.TrimSpace(step.Step) == "" {
			return nil
		}
		switch step.Status {
		case "pending", "in_progress", "completed":
		default:
			return nil
		}
		list = append(list, todo{step.Step, step.Status})
	}
	data, _ := json.Marshal(map[string]any{"todos": list})
	return data
}

func (m *Meta) codexPlanResponse(seq int, l codexLine) []proto.Item {
	if len(m.codexPlanCalls) == 0 && !bytes.Contains(l.Payload, []byte("update_plan")) {
		return nil
	}
	var p struct {
		Type, Name, Arguments string
		CallID                string `json:"call_id"`
	}
	if json.Unmarshal(l.Payload, &p) != nil || p.CallID == "" {
		return nil
	}
	if p.Type == "function_call" && (p.Name == "update_plan" || p.Name == "functions.update_plan") {
		if data := codexTodos([]byte(p.Arguments)); data != nil {
			calls := slices.Clone(m.codexPlanCalls)
			calls = slices.DeleteFunc(calls, func(c codexPlanCall) bool { return c.id == p.CallID })
			if len(calls) >= planPendingMax {
				calls = calls[1:]
			}
			m.codexPlanCalls = append(calls, codexPlanCall{p.CallID, data})
			m.pendingIDs()
		}
		return nil
	}
	if p.Type != "function_call_output" {
		return nil
	}
	for i, call := range m.codexPlanCalls {
		if call.id != p.CallID {
			continue
		}
		m.codexPlanCalls = slices.Delete(slices.Clone(m.codexPlanCalls), i, i+1)
		m.pendingIDs()
		var result struct{ Output json.RawMessage }
		if json.Unmarshal(l.Payload, &result) != nil {
			return nil
		}
		var text string
		if json.Unmarshal(result.Output, &text) != nil {
			var out struct {
				Content string
				Success *bool
			}
			if json.Unmarshal(result.Output, &out) != nil || (out.Success != nil && !*out.Success) {
				return nil
			}
			text = out.Content
		}
		if strings.TrimSpace(text) == "Plan updated" {
			return m.codexPlanRevision(seq, l.Timestamp, call.id, call.todos)
		}
		break
	}
	return nil
}

func (m *Meta) codexPlanEvent(seq int, at time.Time, raw json.RawMessage) []proto.Item {
	todos := codexTodos(raw)
	if todos == nil {
		return nil
	}
	// The native event confirms this call. Its later wire output may arrive after another plan.
	for i, pending := range m.codexPlanCalls {
		if bytes.Equal(pending.todos, todos) {
			m.codexPlanCalls = slices.Delete(slices.Clone(m.codexPlanCalls), i, i+1)
			m.pendingIDs()
			break
		}
	}
	return m.codexPlanRevision(seq, at, "", todos)
}

func (m *Meta) codexPlanRevision(seq int, at time.Time, id string, todos json.RawMessage) []proto.Item {
	if todos == nil || bytes.Equal(todos, m.codexLastPlan) {
		return nil
	}
	m.codexLastPlan = todos
	if id == "" {
		id = fmt.Sprintf("codex-plan-%d", seq)
	}
	items := codexTool(m, func(part int, id, role, kind string) proto.Item {
		return proto.Item{ID: fmt.Sprintf("%s#%d", id, part), Seq: seq, TS: at, Role: role, Kind: kind}
	}, id, "TodoWrite", todos, "Plan updated", false)
	full := items[0]
	full.ToolInput = todos
	for _, change := range foldTaskSnapshots(items, map[string]proto.Item{id: full}) {
		m.applyPlan(change, "")
	}
	m.countPlan(at)
	m.codexWorked(items)
	return m.codexModel(items)
}
