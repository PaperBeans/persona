package transcript

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The agent's plan (docs/integrations/agent-goal.md "Plan"): Claude Code keeps a task list, the TodoWrite list (each call writes
// the whole list) or, with the newer task tools, items TaskCreate adds and TaskUpdate edits by id (TaskList reads
// them all back). Meta.Plan counts the latest list. The list is rebuilt from the calls' successful results the way
// the transcript view folds them (foldTaskSnapshots), so a call that failed changes nothing.

const claudeDesktopPlan = "mcp__ccd_turn__update_plan"
const claudeDesktopWrapUp = "mcp__ccd_turn__wrap_up"

// planTools are the tools whose results change the plan.
var planTools = map[string]bool{claudeDesktopPlan: true, "TodoWrite": true, "TaskCreate": true, "TaskUpdate": true, "TaskList": true}

// PlanMarkers are the bytes a line calling a plan tool contains, for readers that skip the other lines. A call's
// result line does not name its tool: such readers also take the lines holding Meta.PlanPending's ids.
// `update_plan"` matches Claude Desktop's mcp__ccd_turn__update_plan as well as Codex's update_plan and
// functions.update_plan without expanding the node's Aho-Corasick marker table.
var PlanMarkers = [][]byte{[]byte(`"name":"TodoWrite"`), []byte(`"name":"TaskCreate"`), []byte(`"name":"TaskUpdate"`),
	[]byte(`"name":"TaskList"`), []byte(`update_plan"`), []byte(`"plan_update"`)}

// planCurrentLen (runes): the step in progress is one line on a task card and in the snapshot.
const planCurrentLen = 120

// planItem is one item of the plan; activeForm is what the agent says while it works on it ("Running the tests").
type planItem struct {
	id, subject, activeForm, status string
}

// trackPlan follows one parsed item: a plan tool's call waits for its result, which applies it.
func (m *Meta) trackPlan(it proto.Item) {
	if m.Codex || it.ToolUseID == "" {
		return
	}
	switch {
	case it.Kind == proto.KindToolUse && planTools[it.ToolName]:
		if m.planUses == nil {
			m.planUses = map[string]proto.Item{}
		}
		if len(m.planUses) >= planPendingMax { // calls whose results never came (an interrupted turn)
			clear(m.planUses)
		}
		m.planUses[it.ToolUseID] = it
		m.pendingIDs()
	case it.Kind == proto.KindToolResult:
		use, ok := m.planUses[it.ToolUseID]
		if !ok {
			return
		}
		delete(m.planUses, it.ToolUseID)
		m.pendingIDs()
		at := it.TS
		if at.IsZero() {
			at = use.TS
		}
		if text := strings.TrimSpace(it.Text); use.ToolName == "TaskUpdate" && strings.HasPrefix(text, "Task #") && strings.HasSuffix(text, " not found") {
			return // Claude Code answers an unknown id with text, not an error: nothing changed
		}
		if use.ToolName == claudeDesktopPlan && !it.IsError && strings.HasPrefix(strings.TrimSpace(it.Text), "Plan updated") {
			if slices.Contains(m.desktopPlanDeltas, it.ToolUseID) {
				return
			}
			if m.applyDesktopPlanDelta(use.ToolInput) {
				m.desktopPlanDeltas = append(slices.Clone(m.desktopPlanDeltas), it.ToolUseID)
				m.countPlan(at)
				return
			}
		}
		var activeForm string
		if use.ToolName == "TaskCreate" || use.ToolName == "TaskUpdate" {
			if s := taskObject(use.ToolInput).str("activeForm"); s != nil {
				activeForm = *s
			}
		}
		for _, snap := range foldTaskSnapshots([]proto.Item{it}, map[string]proto.Item{it.ToolUseID: use}) {
			m.applyPlan(snap, activeForm)
		}
		m.countPlan(at)
	}
}

// Claude Desktop numbers steps from one. A successful incremental receipt edits the
// previous checklist; a turn ending does not itself complete any steps.
func (m *Meta) applyDesktopPlanDelta(raw json.RawMessage) bool {
	var delta struct {
		Items   json.RawMessage
		Advance json.RawMessage
		Done    []int
		Start   *int
	}
	if json.Unmarshal(raw, &delta) != nil || delta.Items != nil {
		return false
	}
	advance := string(delta.Advance) == "true" || string(delta.Advance) == `"true"`
	if !advance && len(delta.Done) == 0 && delta.Start == nil {
		return false
	}
	// Other task tools can share the session; the indices apply only to the desktop list.
	var indices []int
	for i, step := range m.planItems {
		if strings.HasPrefix(step.id, "todo:") {
			indices = append(indices, i)
		}
	}
	for _, n := range delta.Done {
		if n < 1 || n > len(indices) {
			return true
		}
	}
	if delta.Start != nil && (*delta.Start < 1 || *delta.Start > len(indices)) {
		return true
	}
	m.planItems = slices.Clone(m.planItems)
	for _, n := range delta.Done {
		m.planItems[indices[n-1]].status = "completed"
	}
	if delta.Start != nil {
		m.planItems[indices[*delta.Start-1]].status = "in_progress"
	}
	if advance {
		next := 0
		for i, idx := range indices {
			if m.planItems[idx].status == "in_progress" {
				m.planItems[idx].status = "completed"
				next = i + 1
				break
			}
		}
		for i := range indices {
			idx := indices[(next+i)%len(indices)]
			if m.planItems[idx].status == "pending" {
				m.planItems[idx].status = "in_progress"
				break
			}
		}
	}
	return true
}

// PlanPending is the ids of plan tool calls still waiting for their result (nil when none is), for readers that skip
// lines: the result line holds its call's id. Callers must not change it.
func (m *Meta) PlanPending() [][]byte { return m.planPending }

// planPendingMax bounds the calls waiting for a result.
const planPendingMax = 16

func (m *Meta) pendingIDs() {
	m.planPending = nil // a new slice: a copy of the Meta may hold the old one
	for id := range m.planUses {
		m.planPending = append(m.planPending, []byte(id))
	}
	for _, call := range m.codexPlanCalls {
		m.planPending = append(m.planPending, []byte(call.id))
	}
}

// applyPlan applies one result to the list, as the clients rebuild a folded run's tasks: TodoWrite replaces the todo
// items, TaskList the task items; a field a result leaves out keeps its earlier value; a deleted task goes.
func (m *Meta) applyPlan(snap proto.FoldTaskSnapshot, activeForm string) {
	m.planItems = slices.Clone(m.planItems) // a copy of the Meta may hold the old list
	switch snap.Replace {
	case "todo":
		m.desktopPlanDeltas = nil
		m.planItems = dropItems(m.planItems, func(p planItem) bool { return strings.HasPrefix(p.id, "todo:") })
	case "task":
		keep := map[string]bool{}
		for _, e := range snap.Entries {
			keep[e.ID] = true
		}
		m.planItems = dropItems(m.planItems, func(p planItem) bool { return strings.HasPrefix(p.id, "task:") && !keep[p.id] })
	}
	for _, e := range snap.Entries {
		i := -1
		for j, p := range m.planItems {
			if p.id == e.ID {
				i = j
				break
			}
		}
		if e.Status != nil && *e.Status == "deleted" {
			if i >= 0 {
				m.planItems = append(m.planItems[:i], m.planItems[i+1:]...)
			}
			continue
		}
		var next planItem
		if i >= 0 {
			next = m.planItems[i]
		}
		next.id = e.ID
		// TaskList's text lines add the owner and blockers after the subject: an item already known keeps its own.
		if e.Subject != nil && (snap.Replace != "task" || i < 0) {
			next.subject = *e.Subject
		}
		switch {
		case strings.HasPrefix(e.ID, "todo:"): // a todo's description is its activeForm
			next.activeForm = ""
			if e.Description != nil {
				next.activeForm = *e.Description
			}
		case activeForm != "":
			next.activeForm = activeForm
		}
		switch {
		case e.Status != nil:
			next.status = *e.Status
		case i < 0:
			next.status = snap.FallbackStatus
		}
		if i >= 0 {
			m.planItems[i] = next
		} else {
			m.planItems = append(m.planItems, next)
		}
	}
}

func dropItems(items []planItem, drop func(planItem) bool) []planItem {
	out := items[:0]
	for _, p := range items {
		if !drop(p) {
			out = append(out, p)
		}
	}
	return out
}

// countPlan sets Meta.Plan from the list: nil when it is empty, and a new At only when what it shows changes.
func (m *Meta) countPlan(at time.Time) {
	if len(m.planItems) == 0 {
		m.Plan = nil
		return
	}
	next := proto.PlanInfo{Total: len(m.planItems)}
	for _, p := range m.planItems {
		switch p.status {
		case "completed":
			next.Done++
		case "in_progress":
			if next.Current == "" {
				next.Current = planLine(p)
			}
		}
	}
	if was := m.Plan; was != nil && was.Done == next.Done && was.Total == next.Total && was.Current == next.Current {
		return
	}
	next.At = at
	m.Plan = &next
}

// planLine is an item in progress on one line: its activeForm, else its subject.
func planLine(p planItem) string {
	s := strings.TrimSpace(p.activeForm)
	if s == "" {
		s = strings.TrimSpace(p.subject)
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if r := []rune(s); len(r) > planCurrentLen {
		return string(r[:planCurrentLen-1]) + "…"
	}
	return s
}
