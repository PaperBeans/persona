package node

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const segmentInputReceiptCap = 64

type segmentStarts struct {
	mu   sync.Mutex
	seen map[string]bool
}

type segmentInputs struct {
	mu   sync.Mutex
	seen map[string][]string
}

type segmentTitleKey struct{}

// isSyntheticSegmentMsgID reports whether id was synthesized by the logical
// conversation queue (logical_calls.go) solely to track segment delivery and
// reconciliation; it must never be forwarded to runtimes or tagged onto
// transcript lines as an owner-authored client_msg_id.
func isSyntheticSegmentMsgID(id string) bool {
	return strings.HasPrefix(id, "segment-once:") || strings.HasPrefix(id, "segment-input:")
}

func appendSegmentInput(list []string, client string) ([]string, bool) {
	if client == "" || slices.Contains(list, client) {
		return list, false
	}
	next := make([]string, 0, min(len(list)+1, segmentInputReceiptCap))
	if len(list) >= segmentInputReceiptCap {
		next = append(next, list[len(list)-(segmentInputReceiptCap-1):]...)
	} else {
		next = append(next, list...)
	}
	next = append(next, client)
	return next, true
}

func mergeSegmentInputs(older, newer []string) []string {
	var out []string
	for _, id := range older {
		out, _ = appendSegmentInput(out, id)
	}
	for _, id := range newer {
		out, _ = appendSegmentInput(out, id)
	}
	return out
}

// recordSegmentInput records a positively accepted logical client_msg_id on
// sessionID so assistant.segment.input can reconcile a lost RPC reply without
// tagging relay or internal transcript items as owner messages.
func (n *Node) recordSegmentInput(sessionID, clientMsgID string) {
	if sessionID == "" || clientMsgID == "" {
		return
	}
	n.segmentInputs.mu.Lock()
	if n.segmentInputs.seen == nil {
		n.segmentInputs.seen = map[string][]string{}
	}
	if next, changed := appendSegmentInput(n.segmentInputs.seen[sessionID], clientMsgID); changed {
		n.segmentInputs.seen[sessionID] = next
	}
	n.segmentInputs.mu.Unlock()
	if n.state != nil {
		n.state.update(sessionID, func(st *sessionState) bool {
			next, changed := appendSegmentInput(st.SegmentInputs, clientMsgID)
			if !changed {
				return false
			}
			st.SegmentInputs = next
			return true
		})
	}
}

func (n *Node) segmentInputAccepted(sessionID, clientMsgID string) bool {
	if sessionID == "" || clientMsgID == "" {
		return false
	}
	n.segmentInputs.mu.Lock()
	found := slices.Contains(n.segmentInputs.seen[sessionID], clientMsgID)
	n.segmentInputs.mu.Unlock()
	if found {
		return true
	}
	if n.state != nil {
		if st, ok := n.state.get(sessionID); ok && slices.Contains(st.SegmentInputs, clientMsgID) {
			return true
		}
	}
	return false
}

// flushSegmentInputs folds receipts accepted before state.load completed into
// loaded sessionState records so they are persisted via state.put.
func (n *Node) flushSegmentInputs() {
	if n.state == nil {
		return
	}
	n.segmentInputs.mu.Lock()
	if len(n.segmentInputs.seen) == 0 {
		n.segmentInputs.mu.Unlock()
		return
	}
	pending := make(map[string][]string, len(n.segmentInputs.seen))
	for sid, ids := range n.segmentInputs.seen {
		if len(ids) > 0 {
			pending[sid] = slices.Clone(ids)
		}
	}
	n.segmentInputs.mu.Unlock()
	for sid, ids := range pending {
		n.state.update(sid, func(st *sessionState) bool {
			merged := mergeSegmentInputs(st.SegmentInputs, ids)
			if slices.Equal(st.SegmentInputs, merged) {
				return false
			}
			st.SegmentInputs = merged
			return true
		})
	}
}

// A positive persisted receipt can reconcile a lost manager reply. Absence is
// deliberately unknown after a node restart: it never authorizes a second start.
func (n *Node) segmentReceipt(operation string) (string, bool) {
	n.state.mu.Lock()
	defer n.state.mu.Unlock()
	for id, st := range n.state.Sessions {
		if st.SegmentOperation == operation {
			return id, true
		}
	}
	return "", false
}
func (n *Node) assistantSegment(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil {
		return nil, errors.New("invalid segment parameters")
	}
	if method == "assistant.segment.retire" || method == "assistant.segment.restore" {
		id, _ := p["session_id"].(string)
		unlock, ok := n.typing.tryLock(id)
		if !ok {
			return nil, refusal(assistantwire.CodeConversationBusy, i18n.T(i18n.From(ctx), "rpc-errors.assistant_supervision_busy"), nil)
		}
		defer unlock()
		life, ok := n.assistantLife.tryLock(id)
		if !ok {
			return nil, refusal(assistantwire.CodeConversationBusy, i18n.T(i18n.From(ctx), "rpc-errors.assistant_supervision_busy"), nil)
		}
		defer life()
		retired := method == "assistant.segment.retire"
		if retired {
			n.mu.Lock()
			process := n.procs[id]
			n.mu.Unlock()
			if process != nil {
				process.mu.Lock()
				busy := process.busy || process.compacting || len(process.tasks) > 0
				process.mu.Unlock()
				if busy {
					return nil, refusal(assistantwire.CodeConversationBusy, i18n.T(i18n.From(ctx), "rpc-errors.assistant_supervision_busy"), nil)
				}
			}
			if h, _, _ := n.runtimes.belongs(id); h != nil {
				status := n.runtimeStatus(h, id)
				if status == proto.StatusWorking || status == proto.StatusBlocked {
					return nil, refusal(assistantwire.CodeConversationBusy, i18n.T(i18n.From(ctx), "rpc-errors.assistant_supervision_busy"), nil)
				}
			}
			n.cancelAssistantRecovery(id)
		}
		n.state.update(id, func(st *sessionState) bool { st.SegmentRetired = retired; return true })
		return map[string]bool{"ok": true}, nil
	}
	if method == "assistant.segment.input" {
		id, _ := p["session_id"].(string)
		client, _ := p["client_msg_id"].(string)
		if id == "" || client == "" {
			return map[string]bool{"accepted": false}, nil
		}
		accepted := n.segmentInputAccepted(id, client)
		if !accepted {
			n.echoes.mu.Lock()
			for _, r := range n.echoes.recs[id] {
				if r.ClientMsgID == client && r.Accepted {
					accepted = true
					break
				}
			}
			n.echoes.mu.Unlock()
		}
		return map[string]bool{"accepted": accepted}, nil
	}
	operation, _ := p["operation_id"].(string)
	launcher, _ := p["launcher"].(string)
	if method == "assistant.segment.check" {
		agent := ""
		runtime := n.assistantRuntime(launcher)
		if runtime == nil {
			switch {
			case launcher == "claude" || launcher == "codex":
				agent = launcher
			default:
				if provider, known := n.managedLauncher(launcher); known {
					agent = provider.AgentType()
				} else if !proto.IsExternalLauncher(launcher) {
					agent = n.launcherProbeCached(ctx, launcher).Agent
				} else {
					var err error
					agent, err = n.assistantAgent(ctx, launcher)
					if err != nil {
						return nil, err
					}
				}
			}
		}
		engine := proto.AssistantExecutionIdentity(agent, "", "", launcher)
		policy := agent == "claude" || agent == "codex"
		registered := policy
		if h := runtime; h != nil {
			registered = true
			h.mu.Lock()
			agent = h.hello.Agent
			if agent == "" {
				agent = h.hello.Runtime
			}
			for _, l := range h.hello.Launchers {
				if l.Name == launcher && l.Agent != "" {
					agent = l.Agent
					break
				}
			}
			engine = proto.AssistantExecutionIdentity(agent, proto.SourceRuntime, h.hello.Runtime, launcher)
			h.mu.Unlock()
			policy = h.supervisionPolicy()
		}
		if engine == "" || !registered {
			return nil, refusal("unknown_engine", i18n.T(i18n.From(ctx), "assistant-conversation_segments.unknown_engine"), nil)
		}
		return map[string]any{"engine": engine, "registered": registered, "supervision_policy": policy, "prepare": true}, nil
	}
	if operation == "" || len(operation) > 128 {
		return nil, errors.New("invalid segment operation")
	}
	if id, ok := n.segmentReceipt(operation); ok {
		return map[string]any{"session_id": id, "prepared": true}, nil
	}
	if method == "assistant.segment.receipt" {
		return map[string]any{"unknown": true}, nil
	}
	n.segmentStarts.mu.Lock()
	if n.segmentStarts.seen == nil {
		n.segmentStarts.seen = map[string]bool{}
	}
	if n.segmentStarts.seen[operation] {
		n.segmentStarts.mu.Unlock()
		return map[string]any{"unknown": true}, nil
	}
	n.segmentStarts.seen[operation] = true
	n.segmentStarts.mu.Unlock()
	// The preparation API accepts no input. The manager's role policy (supervision_instructions) is kept: the first
	// input after epoch activation carries the same policy, so the prepared process needs no restart, which for a
	// Codex thread with no turn yet would start another thread under a new id.
	title, _ := p["title"].(string)
	if title = strings.TrimSpace(title); title == "" {
		title = "Persona"
	}
	ctx = context.WithValue(ctx, segmentTitleKey{}, title)
	for _, key := range []string{"title", "text", "prompt", "images", "session_id", "new_session_id", "client_msg_id"} {
		delete(p, key)
	}
	delete(p, "operation_id")
	data, _ := json.Marshal(p)
	result, err := n.handleMethod(ctx, "assistant.send", data)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(result)
	var reply struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(out, &reply) != nil || strings.TrimSpace(reply.SessionID) == "" {
		return nil, errors.New("segment start returned no identity")
	}
	state, _ := n.state.get(reply.SessionID)
	state.SegmentOperation = operation
	n.state.put(reply.SessionID, state)
	return map[string]any{"session_id": reply.SessionID, "prepared": true}, nil
}
