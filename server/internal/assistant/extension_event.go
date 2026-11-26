package assistant

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Events extensions tell the main conversation (assistant.event, docs/extensions/extensions.md "Assistant events"). The
// manager checked the extension's assistant.events grant and its rate and says who sent it; this checks the event,
// writes it as a message that says it is an extension's observation and not the user's word (prompts/assistant/
// extension_event.md, source kind extension), and puts it into the main conversation that exists: now when that is
// idle, else queued behind its turn with the relay queue the conversations' messages use. It never starts, restarts or
// replaces a conversation: without a main conversation the answer is no_main_conversation.

const (
	// extensionEventTool marks a queued extension event (relayMsg.Tool); relayMsg.From is then the extension's id and
	// relayMsg.Key the event's key.
	extensionEventTool = "extension_event"

	// extEventQueueMost is how many events of one extension may wait in a conversation's queue; a conversation that
	// cannot take them (a computer that is off) would otherwise hold whatever a loop of keys sends.
	extEventQueueMost = 10

	// The most characters of each field (the schema says the same: schema/extension/assistant.schema.json).
	extEventKeyMost    = 128
	extEventTitleMost  = 200
	extEventBodyMost   = 4000
	extEventActionMost = 500
	extEventStateMost  = 64
	extEventNameMost   = 100
)

// extEventIDRe is an extension id (extension.idPattern).
var extEventIDRe = lazyre.New(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// extEventBook serializes the queueing of events, so the replace-or-add decision and the cap of extEventQueueMost
// hold for calls that arrive together.
type extEventBook struct {
	mu sync.Mutex
}

// extensionEventRequest answers assistant.extension_event; false for any other method.
func (s *Service) extensionEventRequest(ctx context.Context, method string, params json.RawMessage) (any, bool, error) {
	if method != assistantwire.MethodExtensionEvent {
		return nil, false, nil
	}
	var req assistantwire.ExtensionEvent
	if err := strictParams(method, params, &req); err != nil {
		return nil, true, err
	}
	out, err := s.extensionEvent(ctx, req)
	return out, true, err
}

// checkedEvent is an event whose fields are within their limits, trimmed, with its severity and replace filled in.
type checkedEvent struct {
	assistantwire.AssistantEventParams
	replace bool
}

// checkEvent holds raw, an extension's assistant.event params, to the schema's limits (invalid_params otherwise).
func checkEvent(raw json.RawMessage) (checkedEvent, error) {
	var p assistantwire.AssistantEventParams
	if err := strictParams(assistantwire.MethodAssistantEvent, raw, &p); err != nil {
		return checkedEvent{}, err
	}
	bad := func(msg string) error { return conversationsInvalid(assistantwire.MethodAssistantEvent, msg) }
	p.Key, p.Title = strings.TrimSpace(p.Key), strings.TrimSpace(p.Title)
	p.Body, p.ActionHint = strings.TrimSpace(p.Body), strings.TrimSpace(p.ActionHint)
	p.State = assistantwire.AssistantEventState(strings.TrimSpace(string(p.State)))
	for _, f := range []struct {
		name  string
		value string
		most  int
		need  bool
	}{
		{"key", p.Key, extEventKeyMost, true}, {"title", p.Title, extEventTitleMost, true}, {"body", p.Body, extEventBodyMost, false},
		{"action_hint", p.ActionHint, extEventActionMost, false}, {"state", string(p.State), extEventStateMost, false},
	} {
		if f.need && f.value == "" {
			return checkedEvent{}, bad(f.name + " is required")
		}
		if n := utf8.RuneCountInString(f.value); n > f.most {
			return checkedEvent{}, bad(f.name + " is longer than " + strconv.Itoa(f.most) + " characters")
		}
	}
	switch p.Severity {
	case "":
		p.Severity = assistantwire.EventSeverityInfo
	case assistantwire.EventSeverityInfo, assistantwire.EventSeverityWarning, assistantwire.EventSeverityCritical:
	default:
		return checkedEvent{}, bad("severity must be info, warning or critical")
	}
	return checkedEvent{AssistantEventParams: p, replace: p.Replace.Replaces()}, nil
}

// extensionEvent delivers one event of extension req.Extension into the main conversation.
func (s *Service) extensionEvent(ctx context.Context, req assistantwire.ExtensionEvent) (assistantwire.AssistantEventResult, error) {
	if !extEventIDRe.MatchString(req.Extension) { // the manager's word, but it ends up in the assistant's prompt as it is
		return assistantwire.AssistantEventResult{}, conversationsInvalid(assistantwire.MethodExtensionEvent, "extension is not an extension id")
	}
	ev, err := checkEvent(json.RawMessage(req.Params))
	if err != nil {
		return assistantwire.AssistantEventResult{}, err
	}
	here := s.settings(ctx)
	machine, to := here.mainRouteMachine(), here.Main // the main conversation lives where settings bind it, not always on the default machine
	if machine == "" || to == "" {
		return assistantwire.AssistantEventResult{}, &rpc.Error{Code: assistantwire.CodeNoMainConversation, Message: assistantwire.MethodAssistantEvent + ": there is no main conversation to tell"}
	}
	name := strings.Join(strings.Fields(req.Name), " ")
	if name == "" {
		name = req.Extension
	}
	name = cutRunes(name, extEventNameMost)
	msg := relayMsg{
		From: req.Extension, FromTitle: name, Tool: extensionEventTool, Key: ev.Key, Title: ev.Title, State: ev.Severity, At: time.Now().UTC(),
		Text: prompts.RenderExtensionEvent(prompts.ExtensionEvent{ID: req.Extension, Severity: ev.Severity, Name: name, Key: ev.Key,
			Title: ev.Title, Body: ev.Body, ActionHint: ev.ActionHint, State: string(ev.State)}),
	}
	c, ok := s.session(ctx, machine, to)
	idle := ok && c.Status != proto.StatusWorking && !c.Starting
	if idle && s.queuedFor(to) == 0 {
		dctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		err := s.deliverRelay(dctx, machine, to, msg)
		cancel()
		if err == nil {
			return assistantwire.AssistantEventResult{Delivery: assistantwire.EventDelivered}, nil
		}
		s.logf("extension %s: event %q: %v; queued", req.Extension, ev.Key, err)
	}
	delivery, err := s.queueExtensionEvent(to, msg, ev.replace)
	if err != nil {
		return assistantwire.AssistantEventResult{}, err
	}
	s.publish(ctx) // shown waiting above the composer
	if idle {
		s.drainRelays(machine, []proto.Session{c})
	}
	return assistantwire.AssistantEventResult{Delivery: delivery}, nil
}

// queueExtensionEvent queues msg behind conversation to's turn: in the place of the same extension's queued event with
// the same key when replace (assistant.event's replace; the one going in right now is left), else after the others,
// unless the extension already has extEventQueueMost waiting.
func (s *Service) queueExtensionEvent(to string, msg relayMsg, replace bool) (string, error) {
	book := &s.extEvents
	book.mu.Lock()
	defer book.mu.Unlock()
	if replace && s.replaceQueuedEvent(to, msg) {
		return assistantwire.EventReplaced, nil
	}
	if s.queuedEvents(to, msg.From) >= extEventQueueMost {
		return "", &rpc.Error{Code: assistantwire.CodeQueueFull, Message: assistantwire.MethodAssistantEvent + ": " + msg.From + " already has " + strconv.Itoa(extEventQueueMost) + " events waiting for the main conversation"}
	}
	if s.queueRelay(to, msg) == 0 {
		return "", &rpc.Error{Code: assistantwire.CodeAssistantOffline, Message: assistantwire.MethodAssistantEvent + ": the relay queue could not be read, try again"}
	}
	return assistantwire.EventQueued, nil
}

// replaceQueuedEvent: an event of msg's extension with msg's key still waits in conversation to's queue: msg takes its
// place (true). The first one may be going in right now (drainRelays takes it off once delivered): it is left.
func (s *Service) replaceQueuedEvent(to string, msg relayMsg) bool {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return false
	}
	q := b.st.Queue[to]
	for i := range q {
		if q[i].Tool == extensionEventTool && q[i].From == msg.From && q[i].Key == msg.Key && (i > 0 || !b.draining[to]) {
			q[i] = msg
			s.saveRelayLocked()
			return true
		}
	}
	return false
}

// queuedEvents is how many events of extension id wait in conversation to's queue.
func (s *Service) queuedEvents(to, id string) int {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return 0
	}
	n := 0
	for _, m := range b.st.Queue[to] {
		if m.Tool == extensionEventTool && m.From == id {
			n++
		}
	}
	return n
}
