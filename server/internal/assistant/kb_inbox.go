package assistant

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/deeplink"
	"github.com/nwverse/persona/server/internal/kb"
)

// A knowledge base chat and its Inbox items (#15356, docs/assistant/knowledge.md "A chat's Inbox items"): a chat's page
// (GET /api/v1/kb/chats/{id}) lists the items that came from it as `inbox`, each with the rows of the page's recent
// messages it holds, and an item (GET /api/v1/inbox/{id}) names its chat as `kb_chat`. Both only for the user's own
// apps (fullRole): an extension's token reads the knowledge base or the Inbox by its own scopes, never one through the
// other.
//
// An item is a chat's when its connector and thread are the chat's connector and source id, whatever the connector,
// or when the chat is the one the AI twin reads for it (kbChatOf: a Feishu chat, a mail correspondent, a
// subscription's thread). A row merged into another counts for the one it was merged into, which is what the Inbox
// lists.

// kbChatInboxMost bounds the items a chat's page lists: the open ones first, newest first, then the done ones.
const kbChatInboxMost = 50

// kbChatInboxItem is an item as a chat's page lists it: what its row draws, and messages, the rows of the page's
// recent messages (detail.recent) it holds, each with its day and its lines as written there.
type kbChatInboxItem struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Tier      string            `json:"tier,omitempty"`
	Status    string            `json:"status,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Preview   string            `json:"preview,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
	DoneAt    *time.Time        `json:"done_at,omitempty"`
	Messages  []kbChatInboxLine `json:"messages,omitempty"`
	said      []inboxSaid       // what it received, matched against the recent rows
}

// kbChatInboxLine is one row of a chat's recent messages: its day ("2006-01-02") and its lines, "- 15:04 name: text"
// and any indented ones after it, joined by "\n" as the text has them.
type kbChatInboxLine struct {
	Day  string `json:"day"`
	Line string `json:"line"`
}

// inboxKBChat is the knowledge base chat an item came from, and the path of its page (deeplink.KnowledgeEntry).
type inboxKBChat struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// inboxSaid is one message an item received, as its chain keeps it: who and what, and the minutes it was said in:
// its own time when the chain has one, else its batch's span (from the batch before it to its own entry's time).
type inboxSaid struct {
	sender, text string
	from, to     time.Time
}

// kbChatConnector is the connector whose items a chat's are: the one that wrote it, or (an older file) its source.
func kbChatConnector(c *kb.Chat) string {
	if c.Connector != "" {
		return c.Connector
	}
	return c.Source
}

// fromKBChat says whether it came from chat c. It reads only it and the sources' word (kbChatOf takes srcMu, after b.mu
// when the caller holds that).
func (b *inboxBook) fromKBChat(it *InboxItem, c *kb.Chat) bool {
	if x := it.Connector; x != nil && x.Thread != "" && x.Thread == c.SourceID && it.Source == kbChatConnector(c) {
		return true
	}
	return b.kbChatOf(it) == c.ID
}

// kbChatInbox is chat c's items as the Inbox lists them (sources=all: open, and the done ones the list still has),
// open first, each group newest first, at most kbChatInboxMost; recent is the page's recent messages, read in zone,
// whose rows the items take as theirs. nil when the Inbox has none of c's, or cannot be read.
func (s *Service) kbChatInbox(ctx context.Context, c *kb.Chat, recent string, zone *time.Location) []kbChatInboxItem {
	if s.inboxReady(ctx) != nil {
		return nil
	}
	b := &s.inbox
	b.mu.Lock()
	cutoff := time.Now().Add(-inboxKeepDone)
	cut := b.listedDoneCut(cutoff, true)
	byID := map[string]*kbChatInboxItem{}
	var out []*kbChatInboxItem
	for _, it := range b.items {
		shown := it
		if it.MergedInto != "" { // its survivor shows, as a link naming it opens that one
			if shown = b.items[it.MergedInto]; shown == nil {
				continue
			}
		}
		// Joined on the row itself, not its survivor: the survivor is listed on the chat of each row it stands for and
		// takes there what that row received, which is what that chat's recent holds (a member keeps its own chain, and
		// its source keeps collecting into it, followMerged). No visibility filter: that boundary keeps content from the
		// AI, not from the owner, whose Inbox list has a quarantined item too (inboxListAt), and this asks no model.
		if shown.MergedInto != "" || !inboxShows(shown, cutoff, cut, true) || !b.fromKBChat(it, c) {
			continue
		}
		x := byID[shown.ID]
		if x == nil {
			x = &kbChatInboxItem{ID: shown.ID, Title: shown.Title, Tier: shown.Tier, Status: shown.status(), Summary: shown.Summary,
				Preview: clip(oneLine(shown.Preview), chainMessageText), UpdatedAt: shown.UpdatedAt}
			if shown.DoneAt != nil {
				at := *shown.DoneAt
				x.DoneAt = &at
			}
			byID[shown.ID] = x
			out = append(out, x)
		}
		x.said = append(x.said, inboxSaidOf(it)...)
	}
	b.mu.Unlock()
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(x, y *kbChatInboxItem) int {
		if (x.DoneAt == nil) != (y.DoneAt == nil) {
			if x.DoneAt == nil {
				return -1
			}
			return 1
		}
		return inboxListOrder(x.UpdatedAt, x.ID, y.UpdatedAt, y.ID)
	})
	out = out[:min(len(out), kbChatInboxMost)]
	// Each row goes to the newest item that holds it.
	for _, l := range kbRecentRows(recent, zone) {
		for _, x := range out {
			if slices.ContainsFunc(x.said, l.says) {
				x.Messages = append(x.Messages, kbChatInboxLine{Day: l.day, Line: l.raw})
				break
			}
		}
	}
	list := make([]kbChatInboxItem, len(out))
	for i, x := range out {
		list[i] = *x
		list[i].said = nil
	}
	return list
}

// inboxSaidOf is what it received, by its chain's received entries. Under b.mu.
func inboxSaidOf(it *InboxItem) []inboxSaid {
	var out []inboxSaid
	from := it.StartedAt
	for _, e := range it.Chain {
		if e.Kind != ChainReceived {
			continue
		}
		for _, m := range e.Messages {
			x := inboxSaid{sender: m.Sender, text: m.Text, from: from, to: e.At}
			if x.sender == "" {
				x.sender = e.Sender
			}
			if m.At != nil {
				x.from, x.to = *m.At, *m.At
			}
			out = append(out, x)
		}
		from = e.At
	}
	return out
}

// kbRecentRow is one message of a chat's recent text (kb.Store.Since): its day, its lines as written, its minute in
// the chat's zone, who wrote it and what.
type kbRecentRow struct {
	day, raw     string
	at           time.Time
	sender, text string
}

// says whether the row is message x: the same sender and text, within x's minutes. The Inbox keeps a message's first
// chainMessageText characters with "…" after a cut one (clip), and either side's spacing may differ.
func (l kbRecentRow) says(x inboxSaid) bool {
	if strings.TrimSpace(l.sender) != strings.TrimSpace(x.sender) || l.at.Before(x.from.Truncate(time.Minute)) || l.at.After(x.to.Truncate(time.Minute)) {
		return false
	}
	row, said := oneLine(l.text), oneLine(x.text)
	if row == "" || said == "" {
		return false
	}
	if row == said {
		return true
	}
	head, cut := strings.CutSuffix(said, "…")
	return cut && head != "" && strings.HasPrefix(row, strings.TrimSpace(head))
}

// kbRecentRows reads a chat's recent text ("## 2006-01-02" days, "- 15:04 name: text" rows, their further lines
// indented) into its rows as the apps split it (web/src/lib/kb.ts parseRecent), so a row's lines are the same on
// both sides; a row without a day, a time or a sender is left out.
func kbRecentRows(recent string, zone *time.Location) []kbRecentRow {
	var out []kbRecentRow
	day := ""
	var lines []string
	end := func() {
		if len(lines) == 0 {
			return
		}
		raw := strings.Join(lines, "\n")
		lines = nil
		rest, ok := strings.CutPrefix(raw, "- ")
		if !ok || day == "" || len(rest) < 6 || rest[5] != ' ' {
			return
		}
		at, err := time.ParseInLocation("2006-01-02 15:04", day+" "+rest[:5], zone)
		if err != nil {
			return
		}
		sender, text, ok := strings.Cut(rest[6:], ": ")
		if !ok {
			return
		}
		out = append(out, kbRecentRow{day: day, raw: raw, at: at, sender: sender, text: text})
	}
	for _, l := range strings.Split(recent, "\n") {
		if d, ok := kbRecentDay(l); ok {
			end()
			day = d
			continue
		}
		switch t := strings.TrimSpace(l); {
		case strings.HasPrefix(l, "- "):
			end()
			lines = []string{l}
		case len(lines) > 0 && startsSpace(l): // a further line of the row
			lines = append(lines, l)
		case t != "" && t != "…": // a line of its own (the apps' row without a time); "…" says the text was cut
			end()
			lines = []string{l}
		}
	}
	end()
	return out
}

// startsSpace says whether l starts with white space.
func startsSpace(l string) bool {
	r, n := utf8.DecodeRuneInString(l)
	return n > 0 && unicode.IsSpace(r)
}

// kbRecentDay is the day a "## 2006-01-02" heading of a chat's recent text names.
func kbRecentDay(l string) (string, bool) {
	d, ok := strings.CutPrefix(l, "## ")
	if !ok {
		return "", false
	}
	d = strings.TrimRightFunc(d, unicode.IsSpace)
	if _, err := time.Parse("2006-01-02", d); err != nil || len(d) != len("2006-01-02") {
		return "", false
	}
	return d, true
}

// itemKBChat is the knowledge base chat it came from, when the knowledge base has one: the chat its connector
// archives its thread under, or the one kbChatOf names. nil without, or with the knowledge base off or not open.
//
// The connector's chat is "<source>-<thread>" (kb.ChatID), where source is the connector's id or one of its
// manifest's ArchiveSources (connectorwire.ChatBatch.Source), and the chat keeps the connector as its Connector
// whichever name it is filed under. So it is looked up under the connector's id first, then among the chats whose id
// ends in "-<thread>", each held to fromKBChat (its Connector and SourceID). A chat whose id kb.SafeID cut short is not
// found that way.
func (s *Service) itemKBChat(ctx context.Context, it *InboxItem) *inboxKBChat {
	var ids []string
	thread := ""
	if x := it.Connector; x != nil && x.Thread != "" {
		thread = x.Thread
		ids = append(ids, kb.ChatID(it.Source, thread))
	}
	b := &s.inbox
	if id := b.kbChatOf(it); id != "" {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	e, _ := s.kbGet(ctx)
	if e == nil {
		return nil
	}
	found := func(id string) *inboxKBChat {
		if c, ok := e.Store.Chat(id); ok && b.fromKBChat(it, c) {
			return &inboxKBChat{ID: c.ID, Path: deeplink.KnowledgeEntry(kb.KindChat, c.ID)}
		}
		return nil
	}
	for _, id := range ids {
		if c := found(id); c != nil {
			return c
		}
	}
	if thread == "" {
		return nil
	}
	suffix := kb.SafeID("-" + thread) // what kb.ChatID ends in, whatever the source before it
	for _, id := range e.Store.IDs(kb.KindChat) {
		if strings.HasSuffix(id, suffix) && !slices.Contains(ids, id) {
			if c := found(id); c != nil {
				return c
			}
		}
	}
	return nil
}
