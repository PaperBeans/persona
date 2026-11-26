package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/rpc"
)

// assistant.event from extension id (docs/extensions/extensions.md "Assistant events"): the extension tells the
// assistant's existing main conversation what it observed. It needs the assistant.events scope as granted (the legacy
// full-hub grant counts, as for assistant.read: that token can already message the main conversation as the user, and
// an event is less than that), is limited in how often an extension may send (a faulty one cannot flood the
// conversation), and goes to the assistant, which checks the event and delivers it, with the extension's id and name
// as the manager knows them: the program cannot say who it is. Over the process connection like every call of the
// program, so it needs no HTTP and works in the sandbox, which has no route to the hub.

const (
	// assistantEventMost is how many assistant.event calls one extension may make in assistantEventWindow, refused
	// ones too.
	assistantEventMost   = 20
	assistantEventWindow = 10 * time.Minute
)

// eventLimiter is a sliding window per extension for assistant.event.
type eventLimiter struct {
	mu     sync.Mutex
	now    func() time.Time // time.Now when nil (tests)
	window time.Duration    // assistantEventWindow when 0 (tests)
	most   int              // assistantEventMost when 0 (tests)
	calls  map[string][]time.Time
}

// take counts one call of id. When id has made the most calls within the window it counts nothing and returns how long
// until the oldest one leaves it.
func (l *eventLimiter) take(id string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	window, most := l.window, l.most
	if window == 0 {
		window = assistantEventWindow
	}
	if most == 0 {
		most = assistantEventMost
	}
	kept := l.calls[id][:0]
	for _, at := range l.calls[id] {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	if len(kept) >= most {
		l.calls[id] = kept
		return kept[0].Add(window).Sub(now)
	}
	if l.calls == nil {
		l.calls = map[string][]time.Time{}
	}
	l.calls[id] = append(kept, now)
	return 0
}

// extensionAssistantEvent answers assistant.event from extension id: permission_denied (data {scope}) without the grant,
// rate_limited (data {retry_after}) beyond assistantEventMost calls in assistantEventWindow, then the assistant's answer
// (or its error: invalid_params, no_main_conversation, queue_full).
func (ps *extensions) extensionAssistantEvent(ctx context.Context, id string, params json.RawMessage) (any, error) {
	if ps.reg == nil || !ps.reg.HubScopeAllowed(id, extension.ScopeAssistantEvents) {
		data, _ := json.Marshal(map[string]string{"scope": extension.ScopeAssistantEvents})
		return nil, &rpc.Error{Code: extension.CodePermissionDenied, Message: assistantwire.MethodAssistantEvent + ": requires " + extension.ScopeAssistantEvents, Data: data}
	}
	if wait := ps.eventLimit.take(id); wait > 0 {
		data, _ := json.Marshal(map[string]float64{"retry_after": wait.Seconds()})
		return nil, &rpc.Error{Code: assistantwire.CodeRateLimited, Data: data,
			Message: fmt.Sprintf("%s: more than %d events in %d minutes, try again in %.0f seconds", assistantwire.MethodAssistantEvent,
				assistantEventMost, int(assistantEventWindow.Minutes()), max(1, wait.Seconds()))}
	}
	if ps.h == nil {
		return nil, &rpc.Error{Code: assistantwire.CodeAssistantOffline, Message: errAssistantOffline.Error()}
	}
	name := id
	if p := ps.reg.SnapshotExact(id); p != nil && p.Full != nil && p.Full.Name != "" {
		name = p.Full.Name
	}
	var out json.RawMessage
	if err := ps.h.kbAsk(ctx, assistantwire.MethodExtensionEvent, assistantwire.ExtensionEvent{Extension: id, Name: name, Params: assistantwire.UncheckedEventParams(params)}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
