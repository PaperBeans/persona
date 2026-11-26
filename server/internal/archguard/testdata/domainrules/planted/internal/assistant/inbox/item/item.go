package item

import (
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Fail is every failure item returns. The zero Fail is nil; no operation returns it as a failure.
type Fail interface {
	fail.Fail
	itemFail()
}

// invalid is input that names no item.
type invalid struct{ fail.Invalid }

func (invalid) itemFail() {}

// refused is a command the item's state turns down.
type refused struct{ fail.Refusal }

func (refused) itemFail() {}

// ItemID is one Inbox item. The zero ItemID is no item; ParseItemID never makes one.
type ItemID struct{ item string }

// ParseItemID is the item an id names; an empty id is invalid.
func ParseItemID(raw string) (out outcome.Of[ItemID, Fail]) {
	if raw == "" {
		return out.Err(invalid{fail.NewInvalid(fail.NewCode("item.id.empty"), fail.NewPart("item"))})
	}
	return out.Ok(ItemID{item: raw})
}

// Wire is the id as the wire carries it. Adapters only.
func (i ItemID) Wire() string { return i.item }

// Doer is who marked an item done. The zero Doer is nobody; CommandMarkDone raises a defect on it.
type Doer struct{ doer uint8 }

// DoerOwner is the owner.
func DoerOwner() Doer { return Doer{doer: 1} }

// DoerReview is a review that found the work done.
func DoerReview() Doer { return Doer{doer: 2} }

// DoerVisitor is told who it was.
type DoerVisitor interface {
	VisitOwner()
	VisitReview()
}

// Accept tells v who it was; the zero Doer is a defect.
func (d Doer) Accept(v DoerVisitor) {
	switch d.doer {
	case 1:
		v.VisitOwner()
	case 2:
		v.VisitReview()
	default:
		defect.Raise("item.Doer.Accept", "the zero Doer")
	}
}

// Item is one Inbox item, open or done: each state holds only its own data. The zero Item is no item; only NewItem
// and Decide make one.
type Item struct {
	id    ItemID
	state state
}

// state is an item's place in its lifecycle.
type state interface{ isOpen() bool }

// open is an item waiting for the owner.
type open struct{}

// done is an item marked done, with when and by whom.
type done struct {
	at instant.Instant
	by Doer
}

func (open) isOpen() bool { return true }
func (done) isOpen() bool { return false }

// NewItem is a new item, open.
func NewItem(id ItemID) Item { return Item{id: id, state: open{}} }

// ID is the item's identity, which stores and other items refer to it by.
func (i Item) ID() ItemID { return i.id }

// NeedsOwner says whether the item still waits for the owner.
func (i Item) NeedsOwner() bool { return i.known().isOpen() }

// CanReopen says whether CommandReopen applies: the item is done.
func (i Item) CanReopen() bool { return !i.known().isOpen() }

// known is the item's state; the zero Item is a defect.
func (i Item) known() state {
	if i.state == nil {
		defect.Raise("item.Item", "the zero Item")
	}
	return i.state
}
