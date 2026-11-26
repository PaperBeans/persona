package writers

import (
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// purgeCounting is the storage port counting how often the writers ask the search index to drop what was deleted.
type purgeCounting struct {
	recordingwire.Core
	purges int
}

func (c *purgeCounting) PurgeSearch() {
	c.purges++
	c.Core.PurgeSearch()
}

// Deleting a photo asks the search index for the purge, through the storage port: its line of photos.md and the
// minutes' items citing it leave the index at its next update (the index's side is the knowledge base's
// TestPhotoDeleteDropsItsWordsFromTheSearchIndex).
func TestPhotoDeleteAsksForTheSearchPurge(t *testing.T) {
	e := goldenEngine(t, nil)
	photoMeeting(t, e, recID, time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone))
	addPhoto(t, e, recID, photoUpload(1, 1, "第一张", ""), false)
	core := &purgeCounting{Core: kb.RecordingCore(e.Engine)}
	w := New(core, nil)
	if _, _, err := w.Photos(recID); err != nil || core.purges != 0 {
		t.Fatalf("reading the photos: %v, %d purges", err, core.purges)
	}
	if ok, err := w.PhotoDelete(recID, 1); !ok || err != nil || core.purges != 1 {
		t.Fatalf("PhotoDelete: %v, %v, %d purges", ok, err, core.purges)
	}
	if _, err := w.PhotoDelete(recID, 7); !errors.Is(err, ErrPhotoNotFound) || core.purges != 1 {
		t.Fatalf("a photo that is not there: %v, %d purges", err, core.purges)
	}
}

// The audio writers' state is one per store: a second engine over the same store (the manager makes one when its
// settings change) is hooked with writers of its own, and a delete through them reaches the socket the first ones
// opened. Without that an open socket would go on writing into a recording deleted through the new engine.
func TestAudioStateIsOnePerStore(t *testing.T) {
	e, _ := hubEngine(t, nil)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	hubMeeting(t, e, recID, now)
	w, err := e.AudioOpen(recID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(0, pcmAt(0, 100)); err != nil {
		t.Fatal(err)
	}
	second := newTestEngine(&kb.Engine{Store: e.Store, TranscriptionReady: e.TranscriptionReady, Logf: func(string, ...any) {}})
	if second.Writers == e.Writers || second.Writers.state != e.Writers.state {
		t.Fatalf("the second engine's writers: same %v, same audio state %v", second.Writers == e.Writers, second.Writers.state == e.Writers.state)
	}
	if held, err := second.AudioHeld(recID); err != nil || len(held) != 1 {
		t.Fatalf("held through the second engine: %v %v", held, err)
	}
	if ok, err := second.DeleteRecording(recID, now); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if err := w.Write(1, pcmAt(secs(1), 100)); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("a write after the delete through the other engine: %v", err)
	}
	_ = w.Close()
	// A store nobody hooked has no state to share: its writers start their own.
	fresh := goldenEngine(t, nil)
	if fresh.Writers.state == e.Writers.state || fresh.Writers.state == nil {
		t.Fatal("writers of another store share the audio state")
	}
}

// An audio append goes through the port without a goroutine or an allocation of the port's: the files lock is the
// store's own, held for reading through an interface call, and the chunk file stays open between appends.
func TestAudioAppendAllocatesNothingForThePort(t *testing.T) {
	e, _ := hubEngine(t, nil)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	hubMeeting(t, e, recID, now)
	w, err := e.AudioOpen(recID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	pcm := pcmAt(0, 160)
	at := int64(0)
	if err := w.Write(0, pcm); err != nil { // the first append makes the chunk file
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		at += 160
		if err := w.Write(sampleAt(at), pcm); err != nil {
			t.Fatal(err)
		}
	})
	// What an append allocates is its own: the span it is missing (missingLocked). Nothing more with the port between.
	if allocs > 1 {
		t.Fatalf("an append allocates %v times", allocs)
	}
}

// A live append through the port costs what it did as a method of the knowledge base's engine, plus the writers'
// view of it made for the call (Writers.engine: the engine and the store as the writers see them, and the closures
// over the port): a handful of allocations beside the few hundred of reading the log, and no lock or goroutine more.
func TestLiveAppendAllocatesLittleMoreThroughThePort(t *testing.T) {
	e := liveBenchEngine(t, 20)
	seg := []LiveSegment{{Start: 0, End: 0.9, Text: "sentence 0 of the meeting"}} // held already: read, compared, not written
	made := e.eng()
	direct := testing.AllocsPerRun(100, func() {
		if _, _, err := made.LiveAppend(recID, 0, seg); err != nil {
			t.Fatal(err)
		}
	})
	through := testing.AllocsPerRun(100, func() {
		if _, _, err := e.LiveAppend(recID, 0, seg); err != nil {
			t.Fatal(err)
		}
	})
	if through-direct > 4 {
		t.Fatalf("an append through the port allocates %v times, %v with the engine made already", through, direct)
	}
}

// The order of the locks between the store's meeting lock and the module's live lock is kept by neither side taking
// the other's: a live log's writers finish while the meeting lock is held (they never take it), and meeting.json's
// update finishes while the live lock is held (it never takes that), so no path can hold one and wait for the other.
func TestLiveAndMeetingLocksNeverNest(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e := goldenEngine(t, nil)
	putRecording(t, e, recID, attendedUpload(1, now, AttendanceParticipant), now)
	done := func(what string, fn func() error) {
		t.Helper()
		errs := make(chan error, 1)
		go func() { errs <- fn() }()
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s waits for a lock it must not take", what)
		}
	}
	meeting := e.Writers.core.Locks().Meeting
	meeting.Lock()
	done("a live append under the meeting lock", func() error {
		_, _, err := e.LiveAppend(recID, 0, []LiveSegment{{Start: 1, End: 2, Text: "a line"}})
		return err
	})
	done("a live update under the meeting lock", func() error {
		_, err := e.LiveUpdate(recID, func(l *Live, _ Recording) error { l.Problem = "x"; return nil })
		return err
	})
	done("a live append of the hub's under the meeting lock", func() error {
		_, err := e.LiveAppendHub(recID, nil)
		if errors.Is(err, ErrNotHub) {
			return nil
		}
		return err
	})
	meeting.Unlock()
	live := &e.Writers.state.liveMu
	live.Lock()
	done("a meeting update under the live lock", func() error {
		private := true
		_, err := e.MeetingUpdate(recID, func(m *Meeting, _ Recording) error { m.Private = &private; return nil })
		return err
	})
	live.Unlock()
}
