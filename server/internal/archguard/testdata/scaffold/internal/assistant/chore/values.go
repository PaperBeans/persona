package chore

import (
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Title is a title as chore knows it. The zero Title is no title; ParseTitle never makes one.
type Title struct{ title string }

// ParseTitle is the title raw names; empty text is invalid.
func ParseTitle(raw string) (out outcome.Of[Title, Fail]) {
	if parts := titleRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(Title{title: raw})
}

// titleRule adds what is wrong with raw as a title to parts.
func titleRule(parts []fail.Invalid, raw string) []fail.Invalid {
	return wrong(parts, strings.TrimSpace(raw) != "", "chore.title.empty", "title")
}

// Wire is the title as the wire carries it. Adapters only.
func (x Title) Wire() string { return x.title }

// Effort is an effort as chore knows it. The zero Effort is no effort; ParseEffort never makes one.
type Effort struct{ effort int }

// ParseEffort is the effort raw counts; zero or less is invalid.
func ParseEffort(raw int) (out outcome.Of[Effort, Fail]) {
	if parts := effortRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(Effort{effort: raw})
}

// effortRule adds what is wrong with raw as an effort to parts.
func effortRule(parts []fail.Invalid, raw int) []fail.Invalid {
	return wrong(parts, raw > 0, "chore.effort.not_positive", "effort")
}

// Wire is the effort as the wire carries it. Adapters only.
func (x Effort) Wire() int { return x.effort }

// Snooze is a snooze as chore knows it. The zero Snooze is no snooze: ParseSnooze returns it for 0.
type Snooze struct{ snooze int }

// ParseSnooze is the snooze raw counts; less than zero is invalid, and 0 is the zero Snooze.
func ParseSnooze(raw int) (out outcome.Of[Snooze, Fail]) {
	if parts := snoozeRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(Snooze{snooze: raw})
}

// snoozeRule adds what is wrong with raw as a snooze to parts.
func snoozeRule(parts []fail.Invalid, raw int) []fail.Invalid {
	return wrong(parts, raw >= 0, "chore.snooze.negative", "snooze")
}

// Wire is the snooze as the wire carries it. Adapters only.
func (x Snooze) Wire() int { return x.snooze }

// Window is a window as chore knows it: its opens and its closes. The zero Window is no window; NewWindow never makes
// one, and Accept raises a defect on it.
type Window struct {
	opens  instant.Instant
	closes instant.Instant
}

// WindowVisitor is told the parts of a window.
type WindowVisitor interface {
	VisitWindow(opens instant.Instant, closes instant.Instant)
}

// NewWindow is the window of its parts; a part that is no value is invalid.
func NewWindow(opens instant.Instant, closes instant.Instant) (out outcome.Of[Window, Fail]) {
	var noInstant instant.Instant
	var parts []fail.Invalid
	parts = wrong(parts, opens != noInstant, "chore.window.no_opens", "opens")
	parts = wrong(parts, closes != noInstant, "chore.window.no_closes", "closes")
	if len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(Window{opens: opens, closes: closes})
}

// Accept tells v the parts of the window; the zero Window is a defect.
func (w Window) Accept(v WindowVisitor) {
	if w == (Window{}) {
		defect.Raise("chore.Window.Accept", "the zero Window")
	}
	v.VisitWindow(w.opens, w.closes)
}

// HouseID is a house id as chore knows it. The zero HouseID is no house id; ParseHouseID never makes one.
type HouseID struct{ house string }

// ParseHouseID is the house id raw names; empty text is invalid.
func ParseHouseID(raw string) (out outcome.Of[HouseID, Fail]) {
	if parts := houseIDRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(HouseID{house: raw})
}

// houseIDRule adds what is wrong with raw as a house id to parts.
func houseIDRule(parts []fail.Invalid, raw string) []fail.Invalid {
	return wrong(parts, strings.TrimSpace(raw) != "", "chore.house.empty", "house")
}

// Wire is the house id as the wire carries it. Adapters only.
func (h HouseID) Wire() string { return h.house }

// RoomID is a room id as chore knows it. The zero RoomID is no room id; ParseRoomID never makes one.
type RoomID struct{ room string }

// ParseRoomID is the room id raw names; empty text is invalid.
func ParseRoomID(raw string) (out outcome.Of[RoomID, Fail]) {
	if parts := roomIDRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(RoomID{room: raw})
}

// roomIDRule adds what is wrong with raw as a room id to parts.
func roomIDRule(parts []fail.Invalid, raw string) []fail.Invalid {
	return wrong(parts, strings.TrimSpace(raw) != "", "chore.room.empty", "room")
}

// Wire is the room id as the wire carries it. Adapters only.
func (x RoomID) Wire() string { return x.room }

// ChoreID is a chore id as chore knows it. The zero ChoreID is no chore id; ParseChoreID never makes one.
type ChoreID struct{ chore string }

// ParseChoreID is the chore id raw names; empty text is invalid.
func ParseChoreID(raw string) (out outcome.Of[ChoreID, Fail]) {
	if parts := choreIDRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(ChoreID{chore: raw})
}

// choreIDRule adds what is wrong with raw as a chore id to parts.
func choreIDRule(parts []fail.Invalid, raw string) []fail.Invalid {
	return wrong(parts, strings.TrimSpace(raw) != "", "chore.chore.empty", "chore")
}

// Wire is the chore id as the wire carries it. Adapters only.
func (c ChoreID) Wire() string { return c.chore }

// Spot is a spot as chore knows it: its house and its room. It is comparable, so it keys maps. The zero Spot is no
// spot; ParseSpot never makes one, and Accept raises a defect on it.
type Spot struct {
	house HouseID
	room  RoomID
}

// SpotVisitor is told the parts of a spot.
type SpotVisitor interface {
	VisitSpot(house HouseID, room RoomID)
}

// ParseSpot is the spot its raw parts name; it reports every invalid part, in order.
func ParseSpot(house string, room string) (out outcome.Of[Spot, Fail]) {
	var parts []fail.Invalid
	parts = houseIDRule(parts, house)
	parts = roomIDRule(parts, room)
	if len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(Spot{house: HouseID{house: house}, room: RoomID{room: room}})
}

// Accept tells v the parts of the spot; the zero Spot is a defect.
func (x Spot) Accept(v SpotVisitor) {
	if x == (Spot{}) {
		defect.Raise("chore.Spot.Accept", "the zero Spot")
	}
	v.VisitSpot(x.house, x.room)
}
