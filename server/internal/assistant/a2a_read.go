package assistant

// a2a_read (#13831): a session in A2A scope reads another session's recent conversation, for example one the user
// quoted with @ (node/session_mention.go tells the Agent its address). It reaches the sessions a2a_send reaches, never
// the assistant's own conversations, and reads them as agent_read does.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	a2aReadTurns    = 2  // turns a read without turns returns
	a2aReadMaxTurns = 10 // the most one read returns
)

// errA2ANoSession: the read names no session it may read (a2aReadable).
var errA2ANoSession = errors.New("A2A session not found: use the exact machine_id and session_id from a2a_sessions or a quoted session")

// a2aRead is the recent conversation of session to for caller from: its messages, replies and one line per tool call,
// cut as transcriptText cuts. The target is one a2a_send may address in this scope (a2aAllowed: never a guest, work,
// pending, hidden, archived or space session), listed on its computer and not the assistant's (a2aReadable). Reading
// does not need the target to take messages, so a read-only session in scope can be read too.
func (s *Service) a2aRead(ctx context.Context, cfg assistantwire.A2AConfig, from, to a2aRef, turns int) (string, error) {
	if !a2aValidID(to.Machine) || !a2aValidID(to.Session) || to == from {
		return "", errA2AInvalid
	}
	if !s.a2aAllowed(ctx, cfg, to) {
		return "", errA2AUnavailable
	}
	sess, ok := s.a2aReadable(ctx, to)
	if !ok {
		return "", errA2ANoSession
	}
	if turns <= 0 {
		turns = a2aReadTurns
	}
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, to.Machine, DeviceSessionItems, map[string]any{"session_id": to.Session, "turns": min(turns, a2aReadMaxTurns)}, &res); err != nil {
		return "", err
	}
	machine := to.Machine
	if d, ok := s.device(ctx, to.Machine); ok && d.Name != "" {
		machine = d.Name
	}
	title := agentLabel(sess)
	if strings.Trim(title, "./") == "" { // no title and no folder to name it by
		title = to.Session
	}
	state := firstNonEmpty(sess.Status, proto.StatusUnknown)
	if sess.Cwd != "" {
		state += ", " + sess.Cwd
	}
	// Tool-result text, as agent_read's head: which session, who answers there, where, in what state.
	head := fmt.Sprintf("%s · %s on %s (%s)", title, firstNonEmpty(sess.AgentLabel, sess.Agent, "claude"), machine, state)
	return transcriptText(head, "", res.Items), nil
}

// a2aReadable is session ref as its computer lists it, when an A2A read may return it: visible (a2aVisible) and an
// Agent's, not one of the assistant's conversations, whose chats are the owner's and read with conversation_read.
func (s *Service) a2aReadable(ctx context.Context, ref a2aRef) (proto.Session, bool) {
	for _, sess := range s.a2aSessionsOn(ctx, ref.Machine) {
		if sess.SessionID == ref.Session {
			return sess, a2aVisible(sess) && !sess.Assistant
		}
	}
	return proto.Session{}, false
}
