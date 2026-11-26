package node

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func init() { nodeRPC(proto.MethodStartStatus, (*Node).startStatus) }

// startOpKeep is how long completed op_ids stay cached in memory (matching the 24h held-start expiry); persisted
// sessionState.StartOpID via state.put/state.load backs reconciliation across restarts as well.
const startOpKeep = 24 * time.Hour

type startOpEntry struct {
	done      chan struct{}
	sessionID string
	session   *proto.Session
	startedAt time.Time
}

type startOpLog struct {
	mu      sync.Mutex
	entries map[string]*startOpEntry
	now     func() time.Time // tests
}

func (l *startOpLog) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *startOpLog) pruneLocked(now time.Time) {
	for id, e := range l.entries {
		select {
		case <-e.done:
			if !e.startedAt.IsZero() && now.Sub(e.startedAt) > startOpKeep {
				delete(l.entries, id)
			}
		default:
		}
	}
}

// beginStartOp guards a new-Agent start carrying opID against duplicates on this node (#10674). If a session for opID
// already exists (in memory or loaded from state.load), existing is returned and no new start should run. If another
// call for the same opID is currently running, this waits for it to finish and re-checks. Otherwise finish must be
// called with the started session (or nil if the start failed, which releases opID for retry).
func (n *Node) beginStartOp(ctx context.Context, opID string) (existing *proto.Session, finish func(*proto.Session), err error) {
	if err := n.state.loaded(ctx); err != nil {
		return nil, nil, err
	}
	for {
		n.startOps.mu.Lock()
		now := n.startOps.clock()
		n.startOps.pruneLocked(now)
		if e := n.startOps.entries[opID]; e != nil {
			select {
			case <-e.done:
				if e.sessionID != "" {
					var s proto.Session
					if e.session != nil {
						s = *e.session
					} else {
						s = proto.Session{SessionID: e.sessionID, Status: proto.StatusIdle}
					}
					n.startOps.mu.Unlock()
					return &s, nil, nil
				}
			default:
				wait := e.done
				n.startOps.mu.Unlock()
				select {
				case <-wait:
					continue
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				}
			}
		}
		if sid, st, ok := n.state.byStartOp(opID); ok {
			s := n.reconciledSession(sid, st)
			if n.startOps.entries == nil {
				n.startOps.entries = map[string]*startOpEntry{}
			}
			done := make(chan struct{})
			close(done)
			cp := s
			n.startOps.entries[opID] = &startOpEntry{done: done, sessionID: sid, session: &cp, startedAt: now}
			n.startOps.mu.Unlock()
			return &s, nil, nil
		}
		if n.startOps.entries == nil {
			n.startOps.entries = map[string]*startOpEntry{}
		}
		entry := &startOpEntry{done: make(chan struct{})}
		n.startOps.entries[opID] = entry
		n.startOps.mu.Unlock()
		return nil, func(sess *proto.Session) {
			if sess != nil && sess.SessionID != "" {
				n.recordStartOpState(sess, opID)
				cp := *sess
				n.startOps.mu.Lock()
				entry.sessionID = sess.SessionID
				entry.session = &cp
				entry.startedAt = n.startOps.clock()
				close(entry.done)
				n.startOps.mu.Unlock()
				return
			}
			n.startOps.mu.Lock()
			if n.startOps.entries[opID] == entry {
				delete(n.startOps.entries, opID)
			}
			close(entry.done)
			n.startOps.mu.Unlock()
		}, nil
	}
}

func (n *Node) reconciledSession(sid string, st sessionState) proto.Session {
	if rec := n.starts.get(sid); rec != nil && rec.session.SessionID != "" {
		return rec.session
	}
	return proto.Session{
		SessionID: sid,
		Launcher:  st.Launcher,
		Cwd:       st.Cwd,
		Status:    proto.StatusIdle,
	}
}

// recordStartOpState persists opID on the started session's state record so start.status still finds it after a node
// restart (#10674).
func (n *Node) recordStartOpState(s *proto.Session, opID string) {
	if n.state == nil || s == nil || s.SessionID == "" || opID == "" {
		return
	}
	updated := n.state.update(s.SessionID, func(v *sessionState) bool {
		if v.StartOpID == opID {
			return false
		}
		if v.StartOpID == "" {
			v.StartOpID = opID
			return true
		}
		return false
	})
	if !updated {
		n.state.put(s.SessionID, sessionState{
			Launcher:  s.Launcher,
			Cwd:       s.Cwd,
			StartedAt: n.startOps.clock().UTC(),
			StartOpID: opID,
		})
	}
}

// startStatus answers proto.MethodStartStatus ("start.status"): whether a new-Agent start carrying op_id already
// started, is currently in flight, or is unknown on this node.
func (n *Node) startStatus(ctx context.Context, raw json.RawMessage) (any, error) {
	var p proto.StartStatusParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &rpc.Error{Code: "invalid_params", Message: "start.status: " + err.Error()}
		}
	}
	opID := strings.TrimSpace(p.OpID)
	if opID == "" {
		return nil, &rpc.Error{Code: "invalid_params", Message: "start.status: op_id is required"}
	}
	if err := n.state.loaded(ctx); err != nil {
		return nil, err
	}
	n.startOps.mu.Lock()
	n.startOps.pruneLocked(n.startOps.clock())
	if e := n.startOps.entries[opID]; e != nil {
		select {
		case <-e.done:
			if e.sessionID != "" {
				sid := e.sessionID
				n.startOps.mu.Unlock()
				return proto.StartStatus{State: proto.StartStatusStarted, SessionID: sid}, nil
			}
		default:
			n.startOps.mu.Unlock()
			return proto.StartStatus{State: proto.StartStatusPending}, nil
		}
	}
	n.startOps.mu.Unlock()
	if sid, _, ok := n.state.byStartOp(opID); ok {
		return proto.StartStatus{State: proto.StartStatusStarted, SessionID: sid}, nil
	}
	return proto.StartStatus{State: proto.StartStatusUnknown}, nil
}
