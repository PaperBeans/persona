package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/knowledgeuihost"
	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// The knowledge pages' module (internal/modules/knowledge-ui, docs/architecture/core-and-modules.md "Knowledge UI"):
// the import of the connectors' older chat history (the kb_history_* tools, its loop's rounds and the selection
// page's chat lists) and the marks of what the owner sent from Persona on a knowledge base day. The core reaches it
// only through internal/knowledgeuihost; s.ui is nil when the module is left out of the build
// (persona_without_knowledge_ui, persona_core_only) or switched off (the modules row), and then the core offers no
// kb_history_* tool, runs no import, answers the selection page's chat list as a source without history and gives
// a day as it is (SentOrigin "unavailable"). The knowledge base itself stays in the core.

// historyToolNames are the module's assistant tools, in the order toolNames lists them.
var historyToolNames = []string{"kb_history_plan", "kb_history_status", "kb_history_stop", "kb_history_resume"}

// loadKnowledgeUI makes the module's layer when the build has the module and the modules row does not switch it off
// (the port asks the switch, moduleswitch, as every provider seam does).
func (s *Service) loadKnowledgeUI() {
	if ui, ok := knowledgeuihost.New(s.moduleSwitch, knowledgeUIHost{s}); ok {
		s.ui = ui
	}
}

// withKnowledgeUI is names without the module's tools when svc has no knowledge-ui layer (none yet: when the build
// has no such module), in their order.
func withKnowledgeUI(names []string, svc *Service) []string {
	on := knowledgeuihost.InBuild(moduleswitch.Set{}) // no service yet: as the build has it
	if svc != nil {
		// A connected assistant can answer tool discovery before New has built the layer. Its switch, not that transient
		// nil field, decides whether the tools exist; core-only has no module to discover them through either.
		on = knowledgeuihost.InBuild(svc.moduleSwitch)
	}
	if on {
		return names
	}
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return slices.Contains(historyToolNames, n) })
}

// historyTool runs one of the module's tools; without it the tool is unknown.
func (s *Service) historyTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if s.ui == nil {
		return "", errors.New("unknown tool " + name)
	}
	return s.ui.Tool(ctx, name, args)
}

// kbDaySent is the knowledge base day p names, with the lines the user sent from Persona marked (the module's kbsent).
func (s *Service) kbDaySent(ctx context.Context, e *kb.Engine, p assistantwire.KBDayParams) (assistantwire.KBDayResult, error) {
	return s.ui.Day(ctx, e, p)
}

// knowledgeUIHost is the Service as the module reaches it (knowledgeuihost.Host).
type knowledgeUIHost struct{ s *Service }

func (h knowledgeUIHost) Logf(format string, args ...any)                { h.s.logf(format, args...) }
func (h knowledgeUIHost) Lang(ctx context.Context) i18n.Lang             { return h.s.lang(ctx) }
func (h knowledgeUIHost) Location(ctx context.Context) *time.Location    { return h.s.location(ctx) }
func (h knowledgeUIHost) Engine(ctx context.Context) (*kb.Engine, error) { return h.s.kbGet(ctx) }
func (h knowledgeUIHost) Connected() bool                                { return h.s.kbConnected() }
func (h knowledgeUIHost) Wake()                                          { h.s.kbHistoryWake() }
func (h knowledgeUIHost) DayStart(now time.Time) time.Time               { return dayStart(now) }

func (h knowledgeUIHost) Settings(ctx context.Context) *assistantwire.KBSettings {
	return h.s.kbSettings(ctx)
}

// WritableEngine is the knowledge base open now, when it can be written.
func (h knowledgeUIHost) WritableEngine(ctx context.Context) (*kb.Engine, error) {
	if !h.s.kbWritable(ctx) {
		return nil, errors.New("the knowledge base is not writable")
	}
	e, err := h.s.kbGet(ctx)
	if err == nil && e.Store.ReadOnly() {
		err = kb.ErrReadOnly
	}
	return e, err
}

// History is the history import's settings while the knowledge base is on and writable.
func (h knowledgeUIHost) History(ctx context.Context) *assistantwire.KBHistory {
	if set := h.s.kbSettings(ctx); set != nil && !set.Off && !set.ReadOnly {
		return set.History
	}
	return nil
}

func (h knowledgeUIHost) Current() *kb.Engine {
	if o := h.s.kb.cur.Load(); o != nil {
		return o.eng
	}
	return nil
}

func (h knowledgeUIHost) Notify(ctx context.Context, title, body string) {
	h.s.push(withCard(ctx, cardKBHistory), Notification{Subtitle: "Persona", Title: title, Body: body})
}

func (h knowledgeUIHost) UpdateSelection(ctx context.Context, f func(*kb.Selection) (bool, error)) (*kb.Selection, error) {
	return h.s.updateKBSelection(ctx, f)
}

func (h knowledgeUIHost) RefreshNames(ctx context.Context, connector string, chats []connectorwire.HistoryChat) {
	h.s.refreshSelectionNames(ctx, connector, chats)
}

// The connectors' archives, through the hub (connectors.*).

func (h knowledgeUIHost) Archives(ctx context.Context) ([]assistantwire.ArchiveSource, error) {
	var list assistantwire.Archives
	err := h.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsArchives, map[string]any{}, &list)
	return list.Archives, err
}

func (h knowledgeUIHost) HistoryChats(ctx context.Context, id string) ([]connectorwire.HistoryChat, error) {
	var out assistantwire.HistoryChatsResult
	err := h.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsHistoryChats, map[string]any{"id": id}, &out)
	return out.Chats, err
}

func (h knowledgeUIHost) HistoryPage(ctx context.Context, id string, q connectorwire.HistoryQuery) (connectorwire.HistoryPage, error) {
	var out connectorwire.HistoryPage
	err := h.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsHistory, assistantwire.HistoryParams{ID: id, Query: q}, &out)
	return out, err
}

func (h knowledgeUIHost) HistoryAnchors(ctx context.Context, id string, q connectorwire.HistoryAnchorsQuery) (connectorwire.HistoryAnchors, error) {
	var out connectorwire.HistoryAnchors
	err := h.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsHistoryAnchors, assistantwire.HistoryAnchorsParams{ID: id, Query: q}, &out)
	return out, err
}

func (h knowledgeUIHost) ArchiveMedia(ctx context.Context, id, ref string) (connectorwire.Media, error) {
	var out struct {
		Media connectorwire.Media `json:"media"`
	}
	err := h.s.appCall(ctx, AppConnectors, assistantwire.ConnectorsArchiveMedia, map[string]any{"id": id, "ref": ref}, &out)
	return out.Media, err
}

// The sent-as-user ledger (the manager's Postgres), when the store keeps it.

func (h knowledgeUIHost) HasSentLedger() bool {
	_, ok := h.s.store().(chatSentStore)
	return ok
}

func (h knowledgeUIHost) SentAsUsersForArchive(ctx context.Context, connector string, since, until time.Time) ([]connectorwire.SentAsUser, error) {
	st, ok := h.s.store().(chatSentStore)
	if !ok {
		return nil, nil
	}
	return st.SentAsUsersForArchive(ctx, connector, since, until)
}
