package chore_test

import (
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/chore"
	"github.com/nwverse/persona/server/internal/domainkit/clock/clocktest"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
)

// someTitle is a valid title.
func someTitle(t *testing.T) chore.Title {
	t.Helper()
	return domaintest.Ok(t, chore.ParseTitle("title 1"))
}

// someEffort is a valid effort.
func someEffort(t *testing.T) chore.Effort {
	t.Helper()
	return domaintest.Ok(t, chore.ParseEffort(1))
}

// someWindow is a valid window.
func someWindow(t *testing.T) chore.Window {
	t.Helper()
	return domaintest.Ok(t, chore.NewWindow(at, at))
}

// at is the moment the tests' instants and clock read.
var at = instant.At(time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC))

// raises fails the test unless do raises the defect want ("<op>: <cause>").
func raises(t *testing.T, want string, do func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if d, ok := r.(defect.Defect); !ok || d.Error() != want {
			t.Errorf("raised %v, want the defect %q", r, want)
		}
	}()
	do()
}

// someHouseID is a valid house id.
func someHouseID(t *testing.T) chore.HouseID {
	t.Helper()
	return domaintest.Ok(t, chore.ParseHouseID("house 1"))
}

// someRoomID is a valid room id.
func someRoomID(t *testing.T) chore.RoomID {
	t.Helper()
	return domaintest.Ok(t, chore.ParseRoomID("room 1"))
}

// someChoreID is a valid chore id.
func someChoreID(t *testing.T) chore.ChoreID {
	t.Helper()
	return domaintest.Ok(t, chore.ParseChoreID("chore 1"))
}

// someSpot is a valid spot.
func someSpot(t *testing.T) chore.Spot {
	t.Helper()
	return domaintest.Ok(t, chore.ParseSpot("house 1", "room 1"))
}

// someSnooze is a valid snooze.
func someSnooze(t *testing.T) chore.Snooze {
	t.Helper()
	return domaintest.Ok(t, chore.ParseSnooze(1))
}

func TestParseTitleNeverMakesTheZeroTitle(t *testing.T) {
	var zero chore.Title
	if someTitle(t) == zero {
		t.Error("ParseTitle made the zero Title")
	}
}

func TestParseTitleRefusesInvalidInput(t *testing.T) {
	got := domaintest.Fails(t, chore.ParseTitle(" "))
	if want := "invalid chore.title.empty title"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseEffortNeverMakesTheZeroEffort(t *testing.T) {
	var zero chore.Effort
	if someEffort(t) == zero {
		t.Error("ParseEffort made the zero Effort")
	}
}

func TestParseEffortRefusesInvalidInput(t *testing.T) {
	got := domaintest.Fails(t, chore.ParseEffort(0))
	if want := "invalid chore.effort.not_positive effort"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseSnoozeOfZeroIsTheZeroSnooze(t *testing.T) {
	var zero chore.Snooze
	if domaintest.Ok(t, chore.ParseSnooze(0)) != zero {
		t.Error("ParseSnooze(0) is not no snooze")
	}
}

func TestParseSnoozeRefusesLessThanZero(t *testing.T) {
	got := domaintest.Fails(t, chore.ParseSnooze(-1))
	if want := "invalid chore.snooze.negative snooze"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNewWindowNeverMakesTheZeroWindow(t *testing.T) {
	var zero chore.Window
	if someWindow(t) == zero {
		t.Error("NewWindow made the zero Window")
	}
}

func TestTheZeroWindowIsADefect(t *testing.T) {
	var zero chore.Window
	raises(t, "chore.Window.Accept: the zero Window", func() { zero.Accept(nil) })
}

func TestNewWindowRefusesPartsThatAreNoValue(t *testing.T) {
	var noInstant instant.Instant
	got := domaintest.Fails(t, chore.NewWindow(noInstant, noInstant))
	if want := "invalid chore.window.no_opens opens; " +
		"invalid chore.window.no_closes closes"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseHouseIDNeverMakesTheZeroHouseID(t *testing.T) {
	var zero chore.HouseID
	if someHouseID(t) == zero {
		t.Error("ParseHouseID made the zero HouseID")
	}
}

func TestParseHouseIDRefusesInvalidInput(t *testing.T) {
	got := domaintest.Fails(t, chore.ParseHouseID(" "))
	if want := "invalid chore.house.empty house"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseRoomIDNeverMakesTheZeroRoomID(t *testing.T) {
	var zero chore.RoomID
	if someRoomID(t) == zero {
		t.Error("ParseRoomID made the zero RoomID")
	}
}

func TestParseRoomIDRefusesInvalidInput(t *testing.T) {
	got := domaintest.Fails(t, chore.ParseRoomID(" "))
	if want := "invalid chore.room.empty room"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseChoreIDNeverMakesTheZeroChoreID(t *testing.T) {
	var zero chore.ChoreID
	if someChoreID(t) == zero {
		t.Error("ParseChoreID made the zero ChoreID")
	}
}

func TestParseChoreIDRefusesInvalidInput(t *testing.T) {
	got := domaintest.Fails(t, chore.ParseChoreID(" "))
	if want := "invalid chore.chore.empty chore"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseSpotNeverMakesTheZeroSpot(t *testing.T) {
	var zero chore.Spot
	if someSpot(t) == zero {
		t.Error("ParseSpot made the zero Spot")
	}
}

func TestTheZeroSpotIsADefect(t *testing.T) {
	var zero chore.Spot
	raises(t, "chore.Spot.Accept: the zero Spot", func() { zero.Accept(nil) })
}

func TestParseSpotReportsEveryInvalidPart(t *testing.T) {
	got := domaintest.Fails(t, chore.ParseSpot(" ", " "))
	if want := "invalid chore.house.empty house; " +
		"invalid chore.room.empty room"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEqualPartsMakeEqualSpots(t *testing.T) {
	seen := map[chore.Spot]bool{someSpot(t): true}
	if !seen[someSpot(t)] {
		t.Error("two spots of the same parts are not the same key")
	}
}

// spotParts is the test's own visitor: it keeps the parts it is told.
type spotParts struct {
	house chore.HouseID
	room  chore.RoomID
}

func (r *spotParts) VisitSpot(house chore.HouseID, room chore.RoomID) { r.house, r.room = house, room }

func TestSpotAcceptTellsItsParts(t *testing.T) {
	var got spotParts
	someSpot(t).Accept(&got)
	if got.house != someHouseID(t) {
		t.Error("Accept told another house")
	}
	if got.room != someRoomID(t) {
		t.Error("Accept told another room")
	}
}

func TestTheZeroPriorityIsADefect(t *testing.T) {
	var zero chore.Priority
	raises(t, "chore.Priority.Accept: the zero Priority", func() { zero.Accept(nil) })
}

// priorityNames is the test's own visitor: it keeps the name of each priority it is told.
type priorityNames struct{ names []string }

func (r *priorityNames) VisitLow()    { r.names = append(r.names, "low") }
func (r *priorityNames) VisitNormal() { r.names = append(r.names, "normal") }
func (r *priorityNames) VisitUrgent() { r.names = append(r.names, "urgent") }

func TestPriorityAcceptTellsEachPriority(t *testing.T) {
	var got priorityNames
	for _, x := range []chore.Priority{chore.PriorityLow(), chore.PriorityNormal(), chore.PriorityUrgent()} {
		x.Accept(&got)
	}
	if want := []string{"low", "normal", "urgent"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestReminderRefusesPayloadsThatAreNoValue(t *testing.T) {
	var noInstant instant.Instant
	raises(t, "chore.ReminderRing: the zero Instant", func() { chore.ReminderRing(noInstant) })
}

// reminderNames is the test's own visitor: it keeps the name of each reminder it is told.
type reminderNames struct{ names []string }

func (r *reminderNames) VisitSilent()              { r.names = append(r.names, "silent") }
func (r *reminderNames) VisitRing(instant.Instant) { r.names = append(r.names, "ring") }

func TestReminderAcceptTellsEachReminder(t *testing.T) {
	var got reminderNames
	for _, x := range []chore.Reminder{chore.ReminderSilent(), chore.ReminderRing(at)} {
		x.Accept(&got)
	}
	if want := []string{"silent", "ring"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestBuildRefusesChoreWithoutID(t *testing.T) {
	var noChoreID chore.ChoreID
	got := domaintest.Fails(t, chore.NewChoreBuilder(noChoreID).
		Title(someTitle(t)).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()).
		Build())
	if want := "invalid chore.chore.no_id id"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesChoreWithoutTitle(t *testing.T) {
	var noTitle chore.Title
	got := domaintest.Fails(t, chore.NewChoreBuilder(someChoreID(t)).
		Title(noTitle).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()).
		Build())
	if want := "invalid chore.chore.no_title title"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesChoreWithoutEffort(t *testing.T) {
	var noEffort chore.Effort
	got := domaintest.Fails(t, chore.NewChoreBuilder(someChoreID(t)).
		Title(someTitle(t)).
		Effort(noEffort).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()).
		Build())
	if want := "invalid chore.chore.no_effort effort"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildRefusesChoreWithoutPriority(t *testing.T) {
	var noPriority chore.Priority
	got := domaintest.Fails(t, chore.NewChoreBuilder(someChoreID(t)).
		Title(someTitle(t)).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(noPriority).
		Build())
	if want := "invalid chore.chore.no_priority priority"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildListsEveryPartASkippedStageLeftOut(t *testing.T) {
	var skipped chore.ChorePriorityStep // a skipped stage still compiles; Build refuses it
	got := domaintest.Fails(t, skipped.Priority(chore.PriorityLow()).Build())
	if want := "invalid chore.chore.no_id id; " +
		"invalid chore.chore.no_title title; " +
		"invalid chore.chore.no_effort effort"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTheZeroChoreIsADefect(t *testing.T) {
	var zero chore.Chore
	some := domaintest.Ok(t, chore.NewChoreBuilder(someChoreID(t)).
		Title(someTitle(t)).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()).
		Build())
	raises(t, "chore.Chore.ID: the zero Chore", func() { zero.ID() })
	raises(t, "chore.Chore.IsSame: the zero Chore", func() { zero.IsSame(some) })
	raises(t, "chore.Chore.IsSame: the zero Chore", func() { some.IsSame(zero) })
	raises(t, "chore.Chore.Accept: the zero Chore", func() { zero.Accept(nil) })
	raises(t, "chore.Chore.CanClose: the zero Chore", func() { zero.CanClose() })
	raises(t, "chore.Chore.CanReopen: the zero Chore", func() { zero.CanReopen() })
	now := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()
	raises(t, "chore.Decide: the zero Chore", func() { chore.Decide(zero, chore.CommandClose(), now) })
}

// choreParts is the test's own visitor: it keeps the identity it is told and whether the chore is closed.
type choreParts struct {
	id     chore.ChoreID
	closed bool
}

func (r *choreParts) VisitChoreID(id chore.ChoreID) { r.id = id }
func (r *choreParts) VisitTitle(chore.Title)        {}
func (r *choreParts) VisitEffort(chore.Effort)      {}
func (r *choreParts) VisitSnooze(chore.Snooze)      {}
func (r *choreParts) VisitPriority(chore.Priority)  {}
func (r *choreParts) VisitOpen()                    { r.closed = false }
func (r *choreParts) VisitClosedAt(instant.Instant) { r.closed = true }

func TestChoreAcceptTellsItsIdentity(t *testing.T) {
	var got choreParts
	domaintest.Ok(t, chore.NewChoreBuilder(someChoreID(t)).
		Title(someTitle(t)).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()).
		Build()).Accept(&got)
	if got.id != someChoreID(t) {
		t.Error("Accept told another identity")
	}
}

func TestRestoreChoreRefusesAnUnknownClosingTime(t *testing.T) {
	var unknown instant.Instant
	got := domaintest.Fails(t, chore.RestoreChore(chore.NewChoreBuilder(someChoreID(t)).
		Title(someTitle(t)).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()), opt.Some(unknown)))
	if want := "invalid chore.chore.closed_unknown closed"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestARestoredClosedChoreCanReopen(t *testing.T) {
	if !domaintest.Ok(t, chore.RestoreChore(chore.NewChoreBuilder(someChoreID(t)).
		Title(someTitle(t)).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()), opt.Some(at))).CanReopen() {
		t.Error("a chore restored closed cannot reopen")
	}
}

// events is the test's own visitor: it keeps the chore as decided and the name of each event.
type events struct {
	decided chore.Chore
	names   []string
}

func (r *events) VisitChore(x chore.Chore)    { r.decided = x }
func (r *events) VisitEvent(e chore.Event)    { e.Accept(r) }
func (r *events) VisitClosed(instant.Instant) { r.names = append(r.names, "closed") }
func (r *events) VisitReopened()              { r.names = append(r.names, "reopened") }

func TestTheZeroDecidedIsADefect(t *testing.T) {
	var zero chore.Decided
	now := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()
	raises(t, "chore.Decided.Accept: the zero Decided", func() { zero.Accept(nil) })
	raises(t, "chore.Decided.Then: the zero Decided", func() { zero.Then(chore.CommandClose(), now) })
}

func TestCloseThenReopen(t *testing.T) {
	now := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()
	open := domaintest.Ok(t, chore.NewChoreBuilder(someChoreID(t)).
		Title(someTitle(t)).
		Effort(someEffort(t)).
		Snooze(someSnooze(t)).
		Priority(chore.PriorityLow()).
		Build())
	decided := domaintest.Ok(t, chore.Decide(open, chore.CommandClose(), now))
	twice := domaintest.Fails(t, decided.Then(chore.CommandClose(), now))
	if want := "refused chore.chore.already_closed"; twice != want {
		t.Errorf("closing twice: got %q, want %q", twice, want)
	}
	var seen events
	domaintest.Ok(t, decided.Then(chore.CommandReopen(), now)).Accept(&seen)
	if want := []string{"closed", "reopened"}; !slices.Equal(seen.names, want) {
		t.Errorf("events %q, want %q", seen.names, want)
	}
	if !seen.decided.CanClose() {
		t.Error("a reopened chore cannot close")
	}
}
