package lark

import (
	"testing"
	"time"
)

// turnAt is a turn that came at since, behind prev, read on a clock that says now.
func turnAt(prev chan struct{}, wait time.Duration, since, now time.Time) *feishuTurn {
	return &feishuTurn{prev: prev, done: make(chan struct{}), wait: wait, since: since, now: func() time.Time { return now }}
}

func closedWithin(c chan struct{}, d time.Duration) bool {
	select {
	case <-c:
		return true
	case <-time.After(d):
		return false
	}
}

// A message that came longer ago than the wait goes on at once, though the one before it is still stuck (#4846).
func TestFeishuTurnPastItsWaitGoesOnAtOnce(t *testing.T) {
	t0 := time.Unix(1000, 0)
	stuck := make(chan struct{})
	turn := turnAt(stuck, time.Hour, t0, t0.Add(2*time.Hour))
	turn.pass()
	if !closedWithin(turn.done, 2*time.Second) {
		t.Fatal("a message past its wait still waits for the stuck one")
	}
}

// Messages in line behind a stuck one each wait until the wait after they themselves came, not one after another
// (#4846): three that came together go on together; one that came just now behind a stuck one waits its whole wait.
func TestFeishuTurnsInLineEachCountFromWhenTheyCame(t *testing.T) {
	const wait = 10 * time.Second
	const left = 150 * time.Millisecond
	t0 := time.Unix(1000, 0)
	now := t0.Add(wait - left)
	stuck := make(chan struct{}) // the first one, never through
	start := time.Now()
	t2 := turnAt(stuck, wait, t0, now)
	t3 := turnAt(t2.done, wait, t0, now)
	t4 := turnAt(t3.done, wait, t0, now)
	held := make(chan struct{})          // another one stuck
	late := turnAt(held, wait, now, now) // came just now: its whole wait is ahead
	defer close(held)
	for _, turn := range []*feishuTurn{t2, t3, t4, late} {
		turn.pass()
	}
	if !closedWithin(t4.done, 3*time.Second) {
		t.Fatal("the waits added up: the third in line still waits")
	}
	if took := time.Since(start); took < left/2 {
		t.Fatalf("went on after %v, before its wait was out", took)
	}
	if closedWithin(late.done, 500*time.Millisecond) {
		t.Fatal("a message that came just now went on before its own wait was out")
	}
	close(stuck)
}
