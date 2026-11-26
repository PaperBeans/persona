package extmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/imessagewire"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

type visibilityCapableFeedConnector struct {
	feedConnector
}

func (f *visibilityCapableFeedConnector) Manifest() connector.Manifest {
	m := f.feedConnector.Manifest()
	m.Capabilities = append(m.Capabilities, connector.CapInboxVisibility)
	return m
}

func (f *feedConnector) visibilityCounts() (collects, changes int) {
	return f.collects, f.changes
}

func (f *visibilityCapableFeedConnector) visibilityCounts() (collects, changes int) {
	return f.collects, f.changes
}

func TestInboxVisibilityUnsupportedSourceStopsBeforeCollectAndArchive(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(map[bool]string{false: "older provider", true: "declared provider"}[capable], func(t *testing.T) {
			h := newTestManager(t)
			h.connectors = connector.NewRegistry(nil)
			var c connector.Connector = &feedConnector{}
			if capable {
				c = &visibilityCapableFeedConnector{}
			}
			if err := h.addConnector(c); err != nil {
				t.Fatal(err)
			}
			h.cfg.Inbox.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"example"}, Chats: []string{"synthetic-private"}}

			sources := h.readConnectors(context.Background())
			collects, _ := c.(interface{ visibilityCounts() (int, int) }).visibilityCounts()
			wantCollects := 0
			if capable {
				wantCollects = 1
			}
			if collects != wantCollects {
				t.Fatalf("capable=%v Collect calls=%d, want %d", capable, collects, wantCollects)
			}
			if len(sources) != 1 || (!capable && !sources[0].Off) || (capable && (sources[0].Off || len(sources[0].Things) != 1)) {
				t.Fatalf("capable=%v collection result: %+v", capable, sources)
			}
			if !capable && len(sources[0].Things) != 0 {
				t.Fatalf("older provider returned source content: %+v", sources[0].Things)
			}

			archives := h.kbArchives(context.Background())
			if !capable && len(archives.Archives) != 0 {
				t.Fatalf("older provider remained in KB enumeration: %+v", archives.Archives)
			}
			if capable && (len(archives.Archives) != 1 || archives.Archives[0].ID != "example") {
				t.Fatalf("declared provider lost its KB archive: %+v", archives.Archives)
			}
			_, _, err := h.kbSourcesCall(context.Background(), assistantwire.AppCall{AppID: assistantwire.AppConnectors,
				Method: assistantwire.ConnectorsChanges, Params: json.RawMessage(`{"id":"example"}`)})
			if !capable && err == nil {
				t.Fatal("older provider served archive changes without preflight")
			}
			if capable && err != nil {
				t.Fatalf("declared provider could not serve archive changes: %v", err)
			}
			_, _, historyErr := h.kbSourcesCall(context.Background(), assistantwire.AppCall{AppID: assistantwire.AppConnectors,
				Method: assistantwire.ConnectorsHistoryChats, Params: json.RawMessage(`{"id":"example"}`)})
			if !capable && historyErr == nil {
				t.Fatal("older provider reached history without preflight")
			}
			_, changes := c.(interface{ visibilityCounts() (int, int) }).visibilityCounts()
			wantChanges := 0
			if capable {
				wantChanges = 1
			}
			if changes != wantChanges {
				t.Fatalf("capable=%v archive Changes calls=%d, want %d", capable, changes, wantChanges)
			}
			if capable && historyErr == nil {
				t.Fatal("test fixture unexpectedly implements history; add a call counter to this positive path")
			}
		})
	}
}

func TestInboxVisibilityUnsupportedSourceStopsSubscriptionRunner(t *testing.T) {
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	f := &feedConnector{}
	if err := h.addConnector(f); err != nil {
		t.Fatal(err)
	}
	h.subs = subscriptions.New(h.subscriptionOptions(subscriptions.NewMemStore()))
	h.subsLive.Store(h.subs)
	recipe := subscriptions.Recipe{Mode: wire.ConnectorSubscriptionModeThings, Tool: subscriptions.InboxThingsTool,
		ItemsPath: "$.things", Kind: "sub_message", GroupBy: "thread"}
	sub, err := h.subs.Migrate(context.Background(), subscriptions.Migration{Connector: "example", Source: "example", Label: "Synthetic", Recipe: recipe}, true, subscriptions.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.subs.RunNow(context.Background(), sub.ID, subscriptions.Actor{}); err != nil {
		t.Fatal(err)
	}
	h.subs.RunDue(context.Background())
	if f.collects == 0 {
		t.Fatal("subscription fixture did not exercise the runner's direct Collect path")
	}

	h.cfg.Inbox.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"example"}, DirectChats: true}
	before := f.collects
	if info, ok := (subscriptionConnectors{h}).Find(context.Background(), "example"); !ok || info.Enabled {
		t.Fatalf("unsupported source remained ready to its subscription runner: %+v", info)
	}
	if _, err := h.subs.RunNow(context.Background(), sub.ID, subscriptions.Actor{}); err != nil {
		t.Fatal(err)
	}
	h.subs.RunDue(context.Background())
	if f.collects != before {
		t.Fatalf("blocked subscription runner made %d direct Collect calls", f.collects-before)
	}
}

func TestInboxVisibilitySelectedIMessageCannotEnableAI(t *testing.T) {
	h := newTestManager(t)
	h.cfg.Inbox.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"imessage"}, DirectChats: true}
	if !h.inboxVisibilityBlocksSource("imessage") {
		t.Fatal("iMessage was treated as a provider with inbox_visibility preflight")
	}
	on, err := h.imessageAI(context.Background())
	if err != nil || on {
		t.Fatalf("selected unsupported iMessage AI=%v err=%v", on, err)
	}

	h.store = newMemStore()
	h.connectors = connector.NewRegistry(nil)
	needsIMessageModule(t) // the enablement route and its connector below belong to this module
	imessageNode(t, h, func(method string, _ json.RawMessage) (any, error) {
		return json.RawMessage(`{"connectors":[{"id":"imessage","status":"ok"}]}`), nil
	})
	if err := h.addIMessage(imessagewire.Config{Machine: "mac"}); err != nil {
		t.Fatal(err)
	}
	h.imessage().SetAI(false)
	code, out := call(t, h.routes(), "PUT", "/api/v1/imessage/settings", h.cfg.Token, `{"ai":true}`)
	if code != http.StatusConflict || h.imessage().AI() {
		t.Fatalf("selected unsupported iMessage enablement: code=%d AI=%v response=%v", code, h.imessage().AI(), out)
	}
}
