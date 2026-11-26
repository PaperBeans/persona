package node

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

const (
	toolViewsMaxSessions       = 256
	toolViewsMaxPendingPerSess = 64
	toolViewsMaxUsesPerSess    = 64
	toolViewsMaxBoundPerSess   = 128
)

// toolViewTracker captures out-of-band ToolViews from mcp.call / cli.call results BEFORE
// returning sanitized tool outputs to model runtimes (Super Rule 2), then correlates and
// attaches them onto KindToolResult transcript items on outbound page/push paths.
type toolViewTracker struct {
	mu       sync.Mutex
	sessions map[string]*toolViewSessionState
}

type pendingToolView struct {
	tool      string
	connector string
	recorded  time.Time
	views     []wire.View
}

type toolViewSessionState struct {
	touchedAt  time.Time
	pending    []pendingToolView
	recentUses map[string]proto.Item
	useOrder   []string
	bound      map[string][]wire.View
	boundOrder []string
}

func (t *toolViewTracker) entryLocked(id string, now time.Time) *toolViewSessionState {
	if t.sessions == nil {
		t.sessions = map[string]*toolViewSessionState{}
	}
	st := t.sessions[id]
	if st == nil {
		if len(t.sessions) >= toolViewsMaxSessions {
			var oldestKey string
			var oldestTime time.Time
			for k, v := range t.sessions {
				if oldestKey == "" || v.touchedAt.Before(oldestTime) {
					oldestKey, oldestTime = k, v.touchedAt
				}
			}
			if oldestKey != "" {
				delete(t.sessions, oldestKey)
			}
		}
		st = &toolViewSessionState{}
		t.sessions[id] = st
	}
	st.touchedAt = now
	return st
}

func (st *toolViewSessionState) rememberUseLocked(it proto.Item) {
	if it.Kind != proto.KindToolUse || it.ToolUseID == "" {
		return
	}
	if !couldInvokeConnector(it.ToolName) {
		return
	}
	if st.recentUses == nil {
		st.recentUses = map[string]proto.Item{}
	}
	if _, exists := st.recentUses[it.ToolUseID]; exists {
		st.recentUses[it.ToolUseID] = it
		return
	}
	if len(st.useOrder) >= toolViewsMaxUsesPerSess {
		evict := st.useOrder[0]
		st.useOrder = st.useOrder[1:]
		delete(st.recentUses, evict)
	}
	st.recentUses[it.ToolUseID] = it
	st.useOrder = append(st.useOrder, it.ToolUseID)
}

func (st *toolViewSessionState) bindLocked(itemID string, views []wire.View) {
	if itemID == "" || len(views) == 0 {
		return
	}
	if st.bound == nil {
		st.bound = map[string][]wire.View{}
	}
	if _, exists := st.bound[itemID]; exists {
		return
	}
	if len(st.boundOrder) >= toolViewsMaxBoundPerSess {
		evict := st.boundOrder[0]
		st.boundOrder = st.boundOrder[1:]
		delete(st.bound, evict)
	}
	st.bound[itemID] = cloneToolViews(views)
	st.boundOrder = append(st.boundOrder, itemID)
}

// record sanitizes and queues non-empty views returned for toolName in sessionID.
func (t *toolViewTracker) record(sessionID, toolName string, views []wire.View) {
	if t == nil || sessionID == "" || len(views) == 0 {
		return
	}
	clean := connector.SanitizeToolViews(views)
	if len(clean) == 0 {
		return
	}
	conn, _, _ := strings.Cut(toolName, "_")
	if base, ok := strings.CutSuffix(toolName, "_cli"); ok && base != "" {
		conn = base
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.entryLocked(sessionID, now)
	if len(st.pending) >= toolViewsMaxPendingPerSess {
		st.pending = st.pending[len(st.pending)-toolViewsMaxPendingPerSess+1:]
	}
	st.pending = append(st.pending, pendingToolView{
		tool:      toolName,
		connector: conn,
		recorded:  now,
		views:     clean,
	})
}

// stripResult records res.ToolViews for sessionID/toolName and ALWAYS zeroes res.ToolViews before returning to model runtimes.
func (t *toolViewTracker) stripResult(sessionID, toolName string, res *proto.ToolResult) {
	if res == nil || len(res.ToolViews) == 0 {
		return
	}
	t.record(sessionID, toolName, res.ToolViews)
	res.ToolViews = nil
}

// stripRaw records and removes any top-level "tool_views" field from raw mcp.call JSON responses.
func (t *toolViewTracker) stripRaw(sessionID, toolName string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !bytes.Contains(raw, []byte(`"tool_views"`)) {
		return raw
	}
	var tr proto.ToolResult
	if json.Unmarshal(raw, &tr) == nil && len(tr.ToolViews) > 0 {
		t.record(sessionID, toolName, tr.ToolViews)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	if _, ok := obj["tool_views"]; !ok {
		return raw
	}
	delete(obj, "tool_views")
	cleaned, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return cleaned
}

func (t *toolViewTracker) attach(sessionID string, items []proto.Item, allowBind bool, f *transcript.File) bool {
	if t == nil || sessionID == "" || len(items) == 0 {
		return false
	}
	var extraUses map[string]proto.Item
	if allowBind && f != nil {
		pageUses := map[string]bool{}
		for _, it := range items {
			if it.Kind == proto.KindToolUse && it.ToolUseID != "" {
				pageUses[it.ToolUseID] = true
			}
		}
		for _, it := range items {
			if it.Kind == proto.KindToolResult && it.ToolUseID != "" && !pageUses[it.ToolUseID] {
				if use, ok := f.ToolUse(it.ToolUseID); ok {
					if extraUses == nil {
						extraUses = map[string]proto.Item{}
					}
					extraUses[it.ToolUseID] = use
				}
			}
		}
	}
	return t.attachWithUses(sessionID, items, allowBind, extraUses)
}

func (t *toolViewTracker) attachWithUses(sessionID string, items []proto.Item, allowBind bool, extraUses map[string]proto.Item) bool {
	if t == nil || sessionID == "" || len(items) == 0 {
		return false
	}
	batchUses := map[string]proto.Item{}
	hasCandidateResult := false
	hasRelevantUse := false
	for _, it := range items {
		switch it.Kind {
		case proto.KindToolUse:
			if it.ToolUseID != "" {
				batchUses[it.ToolUseID] = it
				if couldInvokeConnector(it.ToolName) {
					hasRelevantUse = true
				}
			}
		case proto.KindToolResult:
			hasCandidateResult = true
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.sessions[sessionID]
	if st == nil {
		if !allowBind || !hasRelevantUse {
			return false
		}
		st = t.entryLocked(sessionID, time.Now())
	} else {
		st.touchedAt = time.Now()
	}
	if allowBind {
		for _, it := range items {
			if it.Kind == proto.KindToolUse {
				st.rememberUseLocked(it)
			}
		}
	}
	if !hasCandidateResult || (len(st.bound) == 0 && (!allowBind || len(st.pending) == 0)) {
		return false
	}

	changed := false
	for i := range items {
		it := &items[i]
		if it.Kind != proto.KindToolResult {
			continue
		}
		if len(it.ToolViews) > 0 {
			st.bindLocked(it.ID, it.ToolViews)
			continue
		}
		if it.ID != "" {
			if bound, ok := st.bound[it.ID]; ok {
				it.ToolViews = cloneToolViews(bound)
				changed = true
				continue
			}
		}
		if !allowBind || len(st.pending) == 0 {
			continue
		}
		use, hasUse := batchUses[it.ToolUseID]
		if !hasUse && extraUses != nil {
			use, hasUse = extraUses[it.ToolUseID]
		}
		if !hasUse && st.recentUses != nil {
			use, hasUse = st.recentUses[it.ToolUseID]
		}
		if !hasUse && it.ToolName != "" {
			use, hasUse = proto.Item{ToolName: it.ToolName, ToolUseID: it.ToolUseID}, true
		}
		if !hasUse {
			continue
		}
		matched := -1
		for k, p := range st.pending {
			if pendingMatchesUse(p, use) {
				matched = k
				break
			}
		}
		if matched < 0 {
			continue
		}
		p := st.pending[matched]
		st.pending = append(st.pending[:matched], st.pending[matched+1:]...)
		it.ToolViews = cloneToolViews(p.views)
		st.bindLocked(it.ID, p.views)
		changed = true
	}
	return changed
}

func (t *toolViewTracker) hasSession(id string) bool {
	if t == nil || id == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[id] != nil
}

func (t *toolViewTracker) attachRaw(sessionID string, raw json.RawMessage, field string, allowBind bool) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	if !t.hasSession(sessionID) && !bytes.Contains(raw, []byte(`"tool_use"`)) {
		return raw
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	var items []proto.Item
	if json.Unmarshal(obj[field], &items) != nil || len(items) == 0 {
		return raw
	}
	if !t.attachWithUses(sessionID, items, allowBind, nil) {
		return raw
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return raw
	}
	obj[field] = encoded
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

func (t *toolViewTracker) attachItemRaw(sessionID string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !t.hasSession(sessionID) {
		return raw
	}
	var it proto.Item
	if json.Unmarshal(raw, &it) != nil || (it.ID == "" && it.Kind == "") {
		return raw
	}
	items := []proto.Item{it}
	if !t.attachWithUses(sessionID, items, false, nil) || len(items[0].ToolViews) == 0 {
		return raw
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return raw
	}
	encoded, err := json.Marshal(items[0].ToolViews)
	if err != nil {
		return raw
	}
	obj["tool_views"] = encoded
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

func cloneToolViews(views []wire.View) []wire.View {
	if len(views) == 0 {
		return nil
	}
	return append([]wire.View(nil), views...)
}

func couldInvokeConnector(toolName string) bool {
	if toolName == "" {
		return false
	}
	toolName = compat.CanonicalMCPToolName(toolName)
	if strings.HasPrefix(toolName, "mcp__") || strings.Contains(toolName, "_") {
		return true
	}
	return isShellTool(toolName)
}

func isShellTool(toolName string) bool {
	switch strings.ToLower(toolName) {
	case "bash", "shell", "shell_command", "exec_command", "run_command":
		return true
	}
	return false
}

func pendingMatchesUse(p pendingToolView, use proto.Item) bool {
	if p.tool == "" {
		return false
	}
	name := compat.CanonicalMCPToolName(use.ToolName)
	if strings.HasPrefix(name, "mcp__") {
		if i := strings.LastIndex(name, "__"); i > len("mcp_") {
			name = name[i+2:]
		}
	}
	if name == p.tool {
		return true
	}
	if !isShellTool(use.ToolName) {
		return false
	}
	input := string(use.ToolInput)
	if containsToken(input, p.tool) {
		return true
	}
	return p.connector != "" && containsToken(input, p.connector)
}

func containsToken(haystack, needle string) bool {
	if needle == "" || haystack == "" {
		return false
	}
	offset := 0
	for {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(needle)
		leftOK := start == 0 || !isIdentChar(haystack[start-1])
		rightOK := end == len(haystack) || !isIdentChar(haystack[end])
		if leftOK && rightOK {
			return true
		}
		offset = start + len(needle)
	}
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}
