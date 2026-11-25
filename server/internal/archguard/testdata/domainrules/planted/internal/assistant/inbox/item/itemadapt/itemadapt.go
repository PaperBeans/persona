// Package itemadapt converts between item and the stored Inbox item, and acts on item's decisions.
//
//persona:layer adapter
package itemadapt

import (
	"time"

	"github.com/nwverse/persona/server/internal/assistant/inbox/item"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Record is the stored item: plain and tagged, owned by this adapter.
type Record struct {
	ID     string     `json:"id"`
	DoneAt *time.Time `json:"done_at,omitempty"`
	DoneBy string     `json:"done_by,omitempty"`
}

// Open is a new, open item for an id.
func Open(id string) outcome.Of[item.Item, item.Fail] {
	return outcome.Map(item.ParseItemID(id), item.NewItem)
}

// Act stores what a decision decided: the item's record after it, written through save.
func Act(d item.Decided, save func(Record) error) error {
	var r recorder
	d.Accept(&r)
	return save(r.record)
}

// recorder fills a Record from a decision. The visitor it reads doers with is a field, so nested Accept calls do not
// allocate.
type recorder struct {
	record Record
	doer   doerWord
}

func (r *recorder) VisitItem(i item.Item)   { r.record.ID = i.ID().Wire() }
func (r *recorder) VisitEvent(e item.Event) { e.Accept(r) }

func (r *recorder) VisitMarkedDone(at instant.Instant, by item.Doer) {
	t := time.Unix(0, at.Wire()).UTC()
	by.Accept(&r.doer)
	r.record.DoneAt, r.record.DoneBy = &t, r.doer.word
}

func (r *recorder) VisitReopened() { r.record.DoneAt, r.record.DoneBy = nil, "" }

// doerWord is a doer as the store spells it.
type doerWord struct{ word string }

func (w *doerWord) VisitOwner()  { w.word = "owner" }
func (w *doerWord) VisitReview() { w.word = "review" }
