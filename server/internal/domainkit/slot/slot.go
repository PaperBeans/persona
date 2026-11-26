// Package slot is the caller's place for one entry: a core that renders rows for its caller keeps the Slot an adapter
// gave it per entry and hands it back through its visitor without reading it, and the adapter indexes its own list
// with it. It replaces a type parameter carrying the caller's data into the core (docs/server/architecture.md
// "Domain model", "Visitors and acting on decisions").
//
//persona:layer kit
package slot

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// Slot is an index into the adapter's own list. The zero Slot is the caller's first entry.
type Slot struct{ slot int32 }

// NewSlot is the place of entry i. Adapters only (out-slot-mint); a negative i or one past 2^31-1 is a defect.
func NewSlot(i int) Slot {
	if i < 0 || i > 1<<31-1 {
		defect.Raise("slot.NewSlot", "an index outside 0 to 2^31-1")
	}
	return Slot{slot: int32(i)}
}

// Wire is the index, for the adapter that made the slot. Adapters only.
func (s Slot) Wire() int32 { return s.slot }
