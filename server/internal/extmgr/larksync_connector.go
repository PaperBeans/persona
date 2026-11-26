package extmgr

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// larkSyncConnector is the knowledge base's way into what the sync's runs handed over (the outbox): its archive is
// the chat batches by sequence (the knowledge base keeps the cursor), its items the documents and minutes of the last
// week, newest revision of each, and its media a message's attachment, fetched as the binding that read it.
type larkSyncConnector struct{ h *Manager }

func (larkSyncConnector) ID() string   { return larksyncwire.ConnectorID }
func (larkSyncConnector) Name() string { return "Lark sync" }

func (larkSyncConnector) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapArchive, connector.CapItems, connector.CapStatus}}
}

// Changes are the chat batches after cursor (an outbox sequence, "" the first time), at most limit; next is the last
// one's sequence.
func (c larkSyncConnector) Changes(ctx context.Context, cursor string, limit int) ([]connector.ChatBatch, string, bool, error) {
	store, _ := c.h.store.(larkSyncStore)
	if store == nil {
		return nil, cursor, false, nil
	}
	after, _ := strconv.ParseInt(cursor, 10, 64)
	if limit < 1 {
		limit = larkSyncChangesMost
	}
	rows, err := store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, after, limit)
	if err != nil {
		return nil, cursor, false, err
	}
	var out []connector.ChatBatch
	next := cursor
	for _, r := range rows {
		next = strconv.FormatInt(r.Seq, 10)
		var cb connector.ChatBatch
		if err := json.Unmarshal(r.Payload, &cb); err != nil {
			c.h.logf("lark sync: outbox %d: %v", r.Seq, err)
			continue
		}
		out = append(out, cb)
	}
	return out, next, len(rows) == limit, nil
}

// ArchiveMedia fetches an attachment by its ref ("<binding key>|<message id>|<key>|<kind>") as its binding.
func (c larkSyncConnector) ArchiveMedia(ctx context.Context, ref string) (connector.Media, error) {
	key, _, ok := strings.Cut(ref, "|")
	if !ok || key == "" {
		return connector.Media{}, errLarkSyncNoBinding
	}
	st, err := c.h.larkSyncLoad(ctx)
	if err != nil {
		return connector.Media{}, err
	}
	b := st.binding(key)
	if b == nil || !b.authorized() {
		return connector.Media{}, errLarkSyncNoBinding
	}
	app, err := c.h.larkSyncApp(ctx, &st, b.BotID)
	if err != nil {
		return connector.Media{}, err
	}
	reader := c.h.readerOf(c.h.larkSyncRuntime(key))
	if reader == nil {
		return connector.Media{}, errLarkSyncUnavailable
	}
	binding := larksyncwire.Binding{Key: key, BotID: b.BotID, AccountID: b.AccountID, Label: b.Label, Tier: b.Tier, App: app}
	token, _ := c.h.larkSyncTokenFunc(key)
	data, mime, err := reader.Media(ctx, binding, token, ref, connector.ArchiveMediaMost)
	if err != nil {
		return connector.Media{}, err
	}
	return connector.Media{Mime: mime, Data: data}, nil
}

func (c larkSyncConnector) ItemSource() kbwire.ItemSource { return larkSyncItems(c) }

// larkSyncItems are the connector's items.
type larkSyncItems larkSyncConnector

// Items are the newest revision of every document and minutes handed over in the last week.
func (c larkSyncItems) Items(ctx context.Context) ([]kbwire.Item, error) {
	store, _ := c.h.store.(larkSyncStore)
	if store == nil {
		return nil, nil
	}
	rows, err := store.LarkSyncDocsSince(ctx, c.h.bots.sync.clock().Add(-larkSyncDocsWindow))
	if err != nil {
		return nil, err
	}
	out := make([]kbwire.Item, 0, len(rows))
	for _, r := range rows {
		var it kbwire.Item
		if err := json.Unmarshal(r.Payload, &it); err != nil {
			c.h.logf("lark sync: outbox %d: %v", r.Seq, err)
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// DocTitle is not looked up: the documents come with their titles.
func (larkSyncItems) DocTitle(context.Context, string) string { return "" }

// Status is how the sync is doing, from the settings as last read: ok while a binding reads, not configured before
// any, not signed in when every binding must be authorized again.
func (c larkSyncConnector) Status(context.Context) connector.Status {
	s := &c.h.bots.sync
	s.mu.Lock()
	st, have := s.last.clone(), s.haveLast
	s.mu.Unlock()
	if !have {
		var err error
		if st, err = c.h.larkSyncLoad(context.Background()); err != nil {
			return connector.Status{State: connector.StateError}
		}
	}
	n, reauth := 0, 0
	var last connector.Status
	for _, b := range st.bindings {
		if b.pending() || !b.Enabled {
			continue
		}
		n++
		if b.Health == larksyncwire.HealthNeedsReauth || !b.authorized() {
			reauth++
		}
		if b.LastRunAt.After(last.LastSync) {
			last.LastSync = b.LastRunAt
		}
		if b.LastIMRunAt.After(last.LastSync) {
			last.LastSync = b.LastIMRunAt
		}
	}
	switch {
	case n == 0:
		last.State = connector.StateNotConfigured
	case reauth == n:
		last.State = connector.StateNotSignedIn
	default:
		last.State = connector.StateOK
	}
	return last
}
