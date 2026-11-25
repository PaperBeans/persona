package extmgr

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

type review12SubscriptionConnector struct {
	id       string
	collects int
}

func (c *review12SubscriptionConnector) ID() string   { return c.id }
func (c *review12SubscriptionConnector) Name() string { return c.id }
func (c *review12SubscriptionConnector) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapInbox}, Kinds: []string{"group"}}
}
func (c *review12SubscriptionConnector) Collect(context.Context) ([]connector.InboxThing, error) {
	c.collects++
	return []connector.InboxThing{{Thread: "thread-" + c.id, Kind: "group", Title: c.id, Preview: "SYNTHETIC-" + c.id, At: time.Now(), Last: "1"}}, nil
}
func (c *review12SubscriptionConnector) Reply(context.Context, connector.InboxReply) error {
	return nil
}

func TestInboxVisibilityReview12UnsupportedSourceIsolatesSubscriptions(t *testing.T) {
	for _, selectSubscriptionID := range []bool{false, true} {
		t.Run(map[bool]string{false: "backing connector selected", true: "subscription selected"}[selectSubscriptionID], func(t *testing.T) {
			ctx := context.Background()
			h := newTestManager(t)
			h.connectors = connector.NewRegistry(nil)
			blocked := &review12SubscriptionConnector{id: "blocked"}
			ordinary := &review12SubscriptionConnector{id: "ordinary"}
			if err := h.addConnector(blocked); err != nil {
				t.Fatal(err)
			}
			if err := h.addConnector(ordinary); err != nil {
				t.Fatal(err)
			}
			h.subs = subscriptions.New(h.subscriptionOptions(subscriptions.NewMemStore()))
			h.subsLive.Store(h.subs)
			recipe := subscriptions.Recipe{Mode: wire.ConnectorSubscriptionModeThings, Tool: subscriptions.InboxThingsTool,
				ItemsPath: "$.things", Kind: "sub_message", GroupBy: "thread"}
			blockedSub, err := h.subs.Migrate(ctx, subscriptions.Migration{Connector: blocked.id, Source: blocked.id, Label: "Synthetic blocked", Recipe: recipe}, true, subscriptions.Actor{})
			if err != nil {
				t.Fatal(err)
			}
			ordinarySub, err := h.subs.Migrate(ctx, subscriptions.Migration{Connector: ordinary.id, Source: ordinary.id, Label: "Synthetic ordinary", Recipe: recipe}, true, subscriptions.Actor{})
			if err != nil {
				t.Fatal(err)
			}
			for _, sub := range []subscriptions.Subscription{blockedSub, ordinarySub} {
				if _, err := h.subs.RunNow(ctx, sub.ID, subscriptions.Actor{}); err != nil {
					t.Fatal(err)
				}
			}
			h.subs.RunDue(ctx)
			if blocked.collects != 1 || ordinary.collects != 1 {
				t.Fatalf("baseline subscription reads: blocked=%d ordinary=%d, want one each", blocked.collects, ordinary.collects)
			}

			selected := blockedSub.Connector
			if selectSubscriptionID {
				selected = blockedSub.ID
			}
			h.cfg.Inbox.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{selected}, DirectChats: true}
			if info, ok := (subscriptionConnectors{h}).Find(ctx, ordinary.id); !ok || !info.Enabled {
				t.Errorf("unrelated subscription connector was globally disabled: %+v", info)
			}
			if !selectSubscriptionID {
				if info, ok := (subscriptionConnectors{h}).Find(ctx, blocked.id); !ok || info.Enabled {
					t.Errorf("selected unsupported backing connector remained ready: %+v", info)
				}
			}

			beforeBlocked, beforeOrdinary := blocked.collects, ordinary.collects
			for _, sub := range []subscriptions.Subscription{blockedSub, ordinarySub} {
				if _, err := h.subs.RunNow(ctx, sub.ID, subscriptions.Actor{}); err != nil {
					t.Fatal(err)
				}
			}
			h.subs.RunDue(ctx)
			if blocked.collects != beforeBlocked {
				t.Errorf("selected unsupported subscription made %d body reads", blocked.collects-beforeBlocked)
			}
			if ordinary.collects != beforeOrdinary+1 {
				t.Errorf("unrelated scheduled subscription reads = %d, want one", ordinary.collects-beforeOrdinary)
			}
			dryRun, err := h.subs.DryRun(ctx, subscriptions.DryRunRequest{ID: ordinarySub.ID})
			if err != nil || dryRun.Error != nil {
				t.Errorf("unrelated subscription dry run was blocked: err=%v problem=%+v", err, dryRun.Error)
			}

			round := assistantwire.InboxRound{Names: map[string]string{}}
			h.readSubscriptions(ctx, &round, true)
			var sawOrdinary bool
			for _, src := range round.Sources {
				switch src.ID {
				case ordinary.id:
					sawOrdinary = true
					if src.Off || len(src.Things) == 0 {
						t.Errorf("unrelated subscription lost its stored Inbox rows: %+v", src)
					}
				case blocked.id:
					if !src.Off || len(src.Things) != 0 {
						t.Errorf("selected unsupported subscription leaked stored Inbox rows: %+v", src)
					}
				}
			}
			if !sawOrdinary {
				t.Error("mixed Inbox read omitted the unrelated subscription")
			}
		})
	}
}
