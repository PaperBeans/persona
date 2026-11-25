package extmgr

import (
	"context"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

func TestInboxVisibilityReview12DeniedDryRunAndNonMigratedOff(t *testing.T) {
	ctx := context.Background()
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	blocked := &review12SubscriptionConnector{id: "blocked"}
	if err := h.addConnector(blocked); err != nil {
		t.Fatal(err)
	}
	h.subs = subscriptions.New(h.subscriptionOptions(subscriptions.NewMemStore()))
	h.subsLive.Store(h.subs)
	recipe := subscriptions.Recipe{Mode: wire.ConnectorSubscriptionModeThings, Tool: subscriptions.InboxThingsTool,
		ItemsPath: "$.things", Kind: "sub_message", GroupBy: "thread"}
	migration := subscriptions.Migration{Connector: blocked.id, Source: blocked.id, Label: "Synthetic blocked", Recipe: recipe}
	sub, err := h.subs.Migrate(ctx, migration, true, subscriptions.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	sub, err = h.subs.Migrate(ctx, migration, false, subscriptions.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	on := true
	sub, err = h.subs.Update(ctx, sub.ID, subscriptions.Patch{Revision: sub.Revision, ToInbox: &on}, subscriptions.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Migrated != nil {
		t.Fatalf("fixture remained migrated: %+v", sub.Migrated)
	}
	if _, err := h.subs.RunNow(ctx, sub.ID, subscriptions.Actor{}); err != nil {
		t.Fatal(err)
	}
	h.subs.RunDue(ctx)
	if blocked.collects != 1 {
		t.Fatalf("baseline non-migrated subscription reads = %d, want one", blocked.collects)
	}

	// Selecting only the subscription leaves its backing connector enabled, so DryRun reaches its own readAllowed fence.
	h.cfg.Inbox.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{sub.ID}, DirectChats: true}
	if info, ok := (subscriptionConnectors{h}).Find(ctx, blocked.id); !ok || !info.Enabled {
		t.Fatalf("per-subscription selection disabled its backing connector: %+v", info)
	}
	before := blocked.collects
	dryRun, err := h.subs.DryRun(ctx, subscriptions.DryRunRequest{ID: sub.ID})
	if err != nil || dryRun.Error == nil || dryRun.Error.Code != subscriptions.CodeNotConnected {
		t.Errorf("denied subscription dry run: err=%v problem=%+v, want CodeNotConnected", err, dryRun.Error)
	}
	if blocked.collects != before {
		t.Errorf("denied subscription dry run made %d connector body read(s)", blocked.collects-before)
	}

	round := assistantwire.InboxRound{Names: map[string]string{}}
	h.readSubscriptions(ctx, &round, true)
	var found bool
	for _, src := range round.Sources {
		if src.ID != sub.ID {
			continue
		}
		found = true
		if !src.Off || len(src.Things) != 0 || !src.Extension || src.Migrated || src.Via != blocked.id {
			t.Errorf("denied non-migrated subscription projection = %+v, want Off with no stored rows", src)
		}
	}
	if !found {
		t.Errorf("non-migrated read omitted denied subscription %s: %+v", sub.ID, round.Sources)
	}
}

func TestInboxVisibilityReview12SubscriptionConnectorsAllScopesEnabledPerSource(t *testing.T) {
	ctx := context.Background()
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	for _, id := range []string{"blocked", "ordinary", "disabled"} {
		if err := h.addConnector(&review12SubscriptionConnector{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.connectors.SetEnabled("disabled", false); err != nil {
		t.Fatal(err)
	}
	h.cfg.Inbox.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"blocked"}, DirectChats: true}
	infos := (subscriptionConnectors{h}).All(ctx)
	got := map[string]bool{}
	for _, info := range infos {
		got[info.ID] = info.Enabled
	}
	for id, want := range map[string]bool{"blocked": false, "ordinary": true, "disabled": false} {
		if enabled, ok := got[id]; !ok || enabled != want {
			t.Errorf("subscriptionConnectors.All Enabled[%s] = %v, present=%v; want %v (all: %+v)", id, enabled, ok, want, infos)
		}
	}
}
