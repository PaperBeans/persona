package kbhistory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
)

// Checkpoint is the progress of one owner-approved chat. SourceID and FiledAs
// are checked against the host's installation binding before any result is sent.
type Checkpoint struct {
	State    string
	Read     int
	Written  int
	Approval string
	SourceID string `json:"-"`
	FiledAs  string `json:"-"`
}

// ProviderCheckpoint reads the host importer's durable progress. The caller
// supplies an exact chat from its owner binding and rechecks the approval after
// consulting the sent ledger. A missing plan, a stopped import or an ambiguous
// archive identity all withhold the projection.
func ProviderCheckpoint(e *kb.Engine, cfg *assistantwire.KBHistory, connector, chatID, day string, now time.Time) (Checkpoint, bool) {
	if e == nil || e.Store == nil || e.Store.ReadOnly() || !cfg.On() || connector == "" ||
		connector != kb.SafeID(connector) || chatID == "" || chatID != kb.SafeID(chatID) {
		return Checkpoint{}, false
	}
	if _, ok := cfg.Source(connector); !ok {
		return Checkpoint{}, false
	}
	if _, stopped := ReadStop(e.Store.Dir); stopped {
		return Checkpoint{}, false
	}
	p, ok, err := LoadPlan(e.Store.Dir, connector)
	sel, ret := Selection(e)
	if err != nil || !ok || p.Source != connector || p.ApprovedAt == nil || sel.Stopgap() {
		return Checkpoint{}, false
	}
	if n, unlisted := p.widened(sel, ret); n != 0 || unlisted {
		return Checkpoint{}, false
	}
	var chat *PlanChat
	for i := range p.Chats {
		c := &p.Chats[i]
		if kb.ChatID(c.Filing(connector), c.ID) == chatID {
			if chat != nil || c.ID == "" {
				return Checkpoint{}, false
			}
			chat = c
		}
	}
	if chat == nil {
		return Checkpoint{}, false
	}
	need := Need(sel, ret, connector, *chat)
	approved, explicitlyListed := p.Approved[chat.ID]
	if !explicitlyListed || !need.Read || need.Wider(approved) {
		return Checkpoint{}, false
	}
	filed := chat.Filing(connector)
	archived, exists := e.Store.Chat(chatID)
	if exists && (archived.ID != chatID || archived.Source != filed || archived.SourceID != chat.ID ||
		archived.Connector != connector && !(archived.Connector == "" && filed == connector)) {
		return Checkpoint{}, false
	}
	if day != "" {
		// Sent markers cover only the owner's messages. Other message rules
		// (such as mentions) may retain this chat for a longer time.
		mine := sel.Evaluate(ret, chat.facts(connector), kb.MessageFacts{Mine: true})
		if !mine.Store || mine.Days <= 0 {
			return Checkpoint{}, false
		}
		zone := e.Store.Zone()
		today := now.In(zone)
		date, err := time.ParseInLocation(time.DateOnly, day, zone)
		if !exists || err != nil || date.Format(time.DateOnly) != day || day > today.Format(time.DateOnly) ||
			day < today.AddDate(0, 0, -min(need.Days, mine.Days)).Format(time.DateOnly) {
			return Checkpoint{}, false
		}
	}
	// Hash only the approval's identity, never the moving cursor or message data.
	data, _ := json.Marshal(struct {
		Connector, Filing, SourceID string
		GeneratedAt, ApprovedAt     time.Time
		Need                        kb.ReadPlan
	}{connector, filed, chat.ID, p.GeneratedAt, *p.ApprovedAt, need})
	sum := sha256.Sum256(data)
	out := Checkpoint{State: "pending", Approval: hex.EncodeToString(sum[:]), SourceID: chat.ID, FiledAs: filed}
	st, exists, err := e.Store.HistoryState(chatID)
	if err != nil {
		return Checkpoint{}, false
	}
	if !exists {
		return out, true
	}
	// Before the writer recorded the raw identity, let matching canonical archive
	// metadata establish ownership for an existing checkpoint.
	if st.Source != filed || st.Connector != "" && st.Connector != connector || st.SourceID != "" && st.SourceID != chat.ID ||
		(st.Connector == "" || st.SourceID == "") && archived == nil ||
		st.Plan == nil || !st.Plan.Read || st.Plan.Wider(need) || need.Wider(*st.Plan) ||
		st.Read < 0 || st.Written < 0 {
		return Checkpoint{}, false
	}
	switch st.State {
	case kb.HistoryRunning, kb.HistoryPaused, kb.HistoryDone, kb.HistoryError, kb.HistoryUnreadable:
		out.State, out.Read, out.Written = st.State, st.Read, st.Written
		return out, true
	default:
		return Checkpoint{}, false
	}
}
