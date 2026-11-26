package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

type inlineViewStore interface {
	RegisterAssistantViewBlock(context.Context, db.AssistantViewBlockRow) (bool, error)
	AssistantViewBlockKeys(context.Context, string) ([]string, error)
	RecordAssistantViewErrors(context.Context, string, string, string, []string, time.Time) error
}

const inlineFullCacheMax = 2048

type inlineFullKey struct {
	machine      string
	conversation string
	itemID       string
	fullSize     int
}

// inlineFullBook remembers truncated assistant replies (FullSize > 0) whose full text was already fetched via
// session.item and processed without store errors, avoiding repeated node round trips across recent-turn scans.
type inlineFullBook struct {
	mu    sync.Mutex
	seen  map[inlineFullKey]struct{}
	order []inlineFullKey
	next  int
}

func (b *inlineFullBook) has(k inlineFullKey) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.seen[k]
	return ok
}

func (b *inlineFullBook) mark(k inlineFullKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen == nil {
		b.seen = make(map[inlineFullKey]struct{})
	}
	if _, exists := b.seen[k]; exists {
		return
	}
	if len(b.order) < inlineFullCacheMax {
		b.order = append(b.order, k)
	} else {
		delete(b.seen, b.order[b.next])
		b.order[b.next] = k
		b.next = (b.next + 1) % inlineFullCacheMax
	}
	b.seen[k] = struct{}{}
}

func (s *Service) registerInlineViews(parent context.Context, p SessionItems) {
	st, ok := s.store().(inlineViewStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	if session, known := s.session(ctx, p.MachineID, p.SessionID); !known || !session.Assistant {
		return
	}
	// Walk newest-first so the latest reply always gets first claim on the 15s context budget (especially when
	// older turns have FullSize > 0 and require session.item round trips), and newer valid replies advance the
	// pass watermark before older turns are scanned.
	for i := len(p.Items) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			return
		}
		it := p.Items[i]
		if it.ID == "" || it.Role != "assistant" || it.Kind != proto.KindText {
			continue
		}
		var fullKey inlineFullKey
		if it.FullSize > 0 {
			fullKey = inlineFullKey{machine: p.MachineID, conversation: p.SessionID, itemID: it.ID, fullSize: it.FullSize}
			if s.inlineFull.has(fullKey) {
				continue
			}
			var full proto.Item
			if err := s.call(ctx, p.MachineID, "session.item", map[string]any{"session_id": p.SessionID, "item_id": it.ID}, &full); err != nil || full.ID != it.ID || full.Role != "assistant" || full.Kind != proto.KindText || full.FullSize > 0 {
				continue
			}
			it = full
		}
		if !wire.HasAssistantView(it.Text) {
			if fullKey.fullSize > 0 {
				s.inlineFull.mark(fullKey)
			}
			continue
		}
		at := it.TS
		if at.IsZero() {
			at = time.Now().UTC()
		}
		var problems []string
		valid := 0 // complete views that passed and are registered
		scanOK := true
		for _, b := range wire.AssistantViewBlocks(it.Text) {
			if b.Language != "persona-view" {
				continue
			}
			v, err := b.Parse()
			if err != nil {
				problems = append(problems, scrubViewError(scrubViewNames(fmt.Sprintf("persona-view block %d: %v", b.Index, err), b.Raw)))
				continue
			}
			raw, err := json.Marshal(v)
			if err != nil {
				scanOK = false
				continue
			}
			row := db.AssistantViewBlockRow{Conversation: p.SessionID, Key: wire.AssistantBlockKey(it.ID, b.Index), MachineID: p.MachineID, ItemID: it.ID, Index: b.Index, View: raw, CreatedAt: at}
			created, err := st.RegisterAssistantViewBlock(ctx, row)
			if err != nil {
				scanOK = false
				s.logf("assistant inline view registration failed: %v", err)
				continue
			}
			valid++
			if created {
				s.event(ctx, map[string]any{"type": "assistant_view_block", "conversation": p.SessionID, "key": row.Key})
			}
		}
		if len(problems) == 0 {
			if valid == 0 {
				if fullKey.fullSize > 0 && scanOK {
					s.inlineFull.mark(fullKey)
				}
				continue // no card at all (only talks about one): it neither fails nor redoes anything
			}
			at = it.TS // a passing reply clears earlier problems only by when it was really written, not when it was replayed
		}
		if err := st.RecordAssistantViewErrors(ctx, p.SessionID, p.MachineID, it.ID, problems, at); err != nil {
			scanOK = false
			s.logf("assistant view errors could not be recorded: %v", err)
		}
		if fullKey.fullSize > 0 && scanOK {
			s.inlineFull.mark(fullKey)
		}
	}
}

// inlineViewTurns is the bounded recent-turn window scanned by registerInlineTurn / registerInlineTurnSettled.
// Scanning more than one turn ensures a reply whose native transcript flushes after TurnEnded (replacing temporary
// live item ids with final persisted ids) — or whose TurnEnded edge was coalesced by an immediate background wake —
// still registers its final persona-view blocks when a background task, reminder, relay, or quick follow-up starts
// another turn before settle. All node and runtime session.items implementations cap turns at 10 (and items at 2000),
// and registerInlineViews is idempotent on already-registered blocks and timestamp-watermarked view errors.
const inlineViewTurns = 5

// registerInlineTurn also registers cards when no app was watching the conversation during its reply.
func (s *Service) registerInlineTurn(parent context.Context, r SessionRef) {
	if _, ok := s.store().(inlineViewStore); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	if session, known := s.session(ctx, r.MachineID, r.SessionID); !known || !session.Assistant {
		return
	}
	var result struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, r.MachineID, DeviceSessionItems, map[string]any{"session_id": r.SessionID, "turns": inlineViewTurns, "lazy_images": true}, &result); err != nil {
		return
	}
	s.registerInlineViews(ctx, SessionItems{MachineID: r.MachineID, SessionID: r.SessionID, Items: result.Items})
}

// registerInlineTurnSettled re-runs registerInlineTurn after the turn settles so runtimes that flush their native
// transcript after TurnEnded (replacing temporary live/unexported item ids with final persisted ids) still register
// their final persona-view blocks idempotently.
func (s *Service) registerInlineTurnSettled(ctx context.Context, machine, sid string) {
	s.registerInlineTurn(ctx, SessionRef{MachineID: machine, SessionID: sid})
}

func (s *Service) conversationViewBlockKeys(ctx context.Context, conversation string) ([]string, error) {
	st, ok := s.store().(inlineViewStore)
	if !ok {
		return []string{}, nil
	}
	return st.AssistantViewBlockKeys(ctx, conversation)
}

// viewName is a page name or action id that can be said back to the model as it is.
var viewName = lazyre.New(`^[A-Za-z0-9_\-]{1,40}$`)

// scrubViewNames replaces the page names and action ids of a failed view (raw) in a check's message with "…", unless
// they are short plain names: the checks write them unquoted ("pages.<name>: …", "action.<id>: …"), and a name can
// be anything the model wrote, a long sentence or markup included. The reason after them is kept.
func scrubViewNames(msg string, raw json.RawMessage) string {
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		return msg // not JSON: the check's message has no names in it
	}
	var names []string
	var walk func(v any, pages bool)
	walk = func(v any, pages bool) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				if pages {
					names = append(names, k)
				}
				if id, ok := child.(string); ok && k == "id" {
					names = append(names, id)
				}
				walk(child, k == "pages" && !pages)
			}
		case []any:
			for _, child := range v {
				walk(child, false)
			}
		}
	}
	walk(doc, false)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) }) // a name inside a longer one
	for _, n := range names {
		if n != "" && !viewName.MatchString(n) {
			for _, at := range []string{"pages.", "action.", "action "} { // where the checks name them
				msg = strings.ReplaceAll(msg, at+n, at+"…")
			}
		}
	}
	return msg
}
