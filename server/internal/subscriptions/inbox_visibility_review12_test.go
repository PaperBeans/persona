package subscriptions

import (
	"context"
	"errors"
	"testing"
)

// review12ReadStore counts reads of stored item content, separately from subscription metadata.
type review12ReadStore struct {
	Store
	heldItemReads  int
	heldItemsReads int
}

func (s *review12ReadStore) HeldItem(ctx context.Context, id, uid string) (*Held, error) {
	s.heldItemReads++
	return s.Store.HeldItem(ctx, id, uid)
}

func (s *review12ReadStore) HeldItems(ctx context.Context, id string, q ItemQuery) ([]Held, error) {
	s.heldItemsReads++
	return s.Store.HeldItems(ctx, id, q)
}

// review12ReadConnectors counts connector discovery after authorization is denied.
type review12ReadConnectors struct {
	Connectors
	findReads int
}

func (c *review12ReadConnectors) Find(ctx context.Context, id string) (ConnectorInfo, bool) {
	c.findReads++
	return c.Connectors.Find(ctx, id)
}

// review12DeniedReadFixture first proves the reply and knowledge-base fixture is usable, then denies its one source.
func review12DeniedReadFixture(t *testing.T) (*env, Subscription, *review12ReadStore, *review12ReadConnectors) {
	t.Helper()
	e, _, sub := replyEnv(t, true)
	sub = e.patch(sub.ID, Patch{ToKB: ptr(true)})
	if in, thread, err := e.svc.ReplyTarget(e.ctx, sub.ID, "", "c2"); err != nil || in == nil || thread == "" {
		t.Fatalf("ReplyTarget positive control: conn=%v thread=%q err=%v", in, thread, err)
	}
	if ids, err := e.svc.Archives(e.ctx); err != nil || len(ids) != 1 || ids[0] != sub.ID {
		t.Fatalf("Archives positive control: ids=%v err=%v", ids, err)
	}
	if batches, _, _, err := e.svc.Changes(e.ctx, sub.ID, "", 20); err != nil || len(batches) == 0 {
		t.Fatalf("Changes positive control: batches=%+v err=%v", batches, err)
	}
	store := &review12ReadStore{Store: e.store}
	conns := &review12ReadConnectors{Connectors: e.conns}
	e.svc.o.Store, e.svc.o.Connectors = store, conns
	e.svc.o.ReadAllowed = func(context.Context, Subscription) bool { return false }
	return e, sub, store, conns
}

func TestInboxVisibilityReview12ReadAllowedDeniesReplyTarget(t *testing.T) {
	e, sub, store, conns := review12DeniedReadFixture(t)
	in, thread, err := e.svc.ReplyTarget(e.ctx, sub.ID, "", "c2")
	if in != nil || thread != "" || !errors.Is(err, errReplyUnavailable) {
		t.Errorf("denied ReplyTarget = (%v, %q, %v), want no connector and errReplyUnavailable", in, thread, err)
	}
	if store.heldItemReads != 0 || conns.findReads != 0 {
		t.Errorf("denied ReplyTarget read stored content %d time(s) and discovered a connector %d time(s)", store.heldItemReads, conns.findReads)
	}
}

func TestInboxVisibilityReview12ReadAllowedDeniesArchives(t *testing.T) {
	e, _, store, conns := review12DeniedReadFixture(t)
	ids, err := e.svc.Archives(e.ctx)
	if err != nil || len(ids) != 0 {
		t.Errorf("denied subscription Archives = (%v, %v), want no readable archives", ids, err)
	}
	if store.heldItemReads != 0 || store.heldItemsReads != 0 || conns.findReads != 0 {
		t.Errorf("denied Archives read content or connectors: held item=%d held items=%d connectors=%d", store.heldItemReads, store.heldItemsReads, conns.findReads)
	}
}

func TestInboxVisibilityReview12ReadAllowedDeniesChanges(t *testing.T) {
	e, sub, store, conns := review12DeniedReadFixture(t)
	batches, next, more, err := e.svc.Changes(e.ctx, sub.ID, "17", 20)
	if batches != nil || next != "17" || more || !errors.Is(err, ErrArchiveOff) {
		t.Errorf("denied Changes = (%+v, %q, %v, %v), want no content, unchanged cursor, ErrArchiveOff", batches, next, more, err)
	}
	if store.heldItemsReads != 0 || conns.findReads != 0 {
		t.Errorf("denied Changes read stored content %d time(s) and discovered a connector %d time(s)", store.heldItemsReads, conns.findReads)
	}
}
