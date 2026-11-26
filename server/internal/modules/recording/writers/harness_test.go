package writers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"

	"github.com/nwverse/persona/server/internal/kb"
)

// testEngine is a knowledge base with this package's writers hooked into it, as the recording module's hook does:
// the knowledge base's own members (PutRecording, Get, Store, Model, ...) and the writers' (WriteMinutes, AskMinutes,
// MinutesWritten, ...) under one value, as the tests had them while the writers were methods of kb.Engine.
type testEngine struct {
	*kb.Engine
	*Writers
	clock func() time.Time
}

// SetClock sets the knowledge base's clock, and keeps it for a test that makes a second engine on the same store.
func (e *testEngine) SetClock(now func() time.Time) {
	e.clock = now
	e.Engine.SetClock(now)
}

var (
	testStoresMu sync.Mutex
	testStores   = map[*kb.Store]*Writers{}
)

// newTestEngine hooks e: its provider is this package's writers over its storage port, with the knowledge base's
// remaining writers behind them (recording.Recordings.Hook).
func newTestEngine(e *kb.Engine) *testEngine {
	w := New(kb.RecordingCore(e), kb.RecordingWriters(e))
	e.SetRecordingProvider(w)
	testStoresMu.Lock()
	testStores[e.Store] = w
	testStoresMu.Unlock()
	return &testEngine{Engine: e, Writers: w}
}

// eng is the engine as the writers see it now (Writers.engine), for a test that calls a writer below the rounds.
func (e *testEngine) eng() *engine { return e.Writers.engine() }

// ws is knowledge-base store s as the writers see it.
func ws(s *kb.Store) *store {
	testStoresMu.Lock()
	defer testStoresMu.Unlock()
	return testStores[s].store()
}

// What the knowledge base keeps to itself and these tests name: its files' names and a few limits, as they are on
// disk and in internal/kb, and small helpers of its own tests.
const (
	photosFile     = "photos.json"
	minutesHubWait = recordingwire.MinutesHubWait
)

// entryOf is the kind and id of the entry a path under the data directory belongs to (recordings/<id>/...).
func entryOf(rel string) (kind, id string, ok bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// summaryDue is the knowledge base's own rule (internal/kb/recordings_summary.go): r's summary is to be made (again).
func summaryDue(r *Recording) bool {
	if !r.Complete || r.Chars == 0 || r.Trimmed() || r.Kind == RecordingDay {
		return false
	}
	t := recordingwire.HubTranscription(r)
	return r.SummarizedRev < r.Revision || t != nil && t.Done() && !r.SummarizedFinal
}

// forgetOnlyPhoto rewrites the photo book at path as a delete of photo n cut short leaves it: the only listed photo
// becomes a deleted one, its file and the minutes citing it untouched. The caller holds the store's files lock.
func forgetOnlyPhoto(path string, n int) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var b map[string]json.RawMessage
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	var photos []struct {
		ID recordingwire.PhotoID `json:"id"`
	}
	if err := json.Unmarshal(b["photos"], &photos); err != nil {
		return err
	}
	var deleted []recordingwire.PhotoRef
	if b["deleted"] != nil {
		if err := json.Unmarshal(b["deleted"], &deleted); err != nil {
			return err
		}
	}
	deleted = append(deleted, recordingwire.PhotoRef{N: n, ID: photos[0].ID})
	b["deleted"], _ = json.Marshal(deleted)
	delete(b, "photos")
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

// The summary round is the knowledge base's own code still; a test calls it there (the provider's method of the same
// name carries it to the same function).
func (e *testEngine) SummarizeRecordings(ctx context.Context, max int) int {
	return e.Engine.SummarizeRecordings(ctx, max)
}

// trimRecording trims recording id at now as the knowledge base's retention does, and only it: its meta.json is
// written as trimmed (trimmed_at, attending off, the minutes and the recap planned again), then a retention round
// runs, which finishes every trimmed recording whatever its age and leaves the ones within recording_days alone. The
// tests called the store's own trim while they were in internal/kb; a round also tells meeting.json's transcription
// and runs the sweep after it, as every real trim does.
func trimRecording(t *testing.T, e *testEngine, id string, now time.Time) {
	t.Helper()
	markTrimmed(t, e, id, now)
	e.PruneRecordings(now)
	if r := recordingNow(t, e, id); !r.Trimmed() {
		t.Fatalf("recording %s is not trimmed", id)
	}
}

// More of what the knowledge base keeps to itself and these tests name (internal/kb/recording_photos_read.go,
// live.go), and small helpers of its own tests (internal/kb/meeting_test.go).
const (
	photosText = "photos.md"
)

// roomUploadJSON decodes an upload body as the HTTP layer does.
func roomUploadJSON(t *testing.T, body string) RecordingUpload {
	t.Helper()
	var u RecordingUpload
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

// metaKeys are the keys of recording id's meta.json on disk.
func metaKeys(t *testing.T, e *testEngine, id string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(e.Store.Path(KindRecording, id, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// markTrimmed writes recording id's meta.json as a trim cut short right after it leaves it: trimmed at now, nothing
// else of the trim done (the knowledge base's putTrimmedMeta, which these tests called while they were in
// internal/kb).
func markTrimmed(t *testing.T, e *testEngine, id string, now time.Time) {
	t.Helper()
	r := recordingNow(t, e, id)
	at := now.UTC().Truncate(time.Second)
	r.TrimmedAt = &at
	r.Attendance, r.AttendingSession = "", nil
	planMinutes(r)
	planRecap(r)
	if err := ws(e.Store).putEntity(KindRecording, id, r, &r.Tagging, false); err != nil {
		t.Fatal(err)
	}
}

// More names of the contract these tests use, as they were in internal/kb.
const (
	PartRoom        = recordingwire.PartRoom
	PartPrivate     = recordingwire.PartPrivate
	RoomStarting    = recordingwire.RoomStarting
	RoomReady       = recordingwire.RoomReady
	RoomUnavailable = recordingwire.RoomUnavailable
	RoomEnded       = recordingwire.RoomEnded
	RoomDeleted     = recordingwire.RoomDeleted
)

type MeetingRoomLean = recordingwire.MeetingRoomLean

// readOnlyEngine is a knowledge base opened read-only, as the module's routes reach one (the core never hooks it):
// writers over its storage port for the request, the summary round left out.
func readOnlyEngine(e *kb.Engine) *testEngine {
	return &testEngine{Engine: e, Writers: New(kb.RecordingCore(e), nil)}
}

// recordingJournal is the name of a recording's undo journal and recordingUndo its content, as the knowledge base's
// commit writes them (internal/kb/recording_commit_read.go): a test leaves one behind as a crash would.
const recordingJournal = ".recording-undo.json"

type recordingUndo struct {
	Version int               `json:"version"`
	ID      string            `json:"id"`
	Files   map[string][]byte `json:"files"`
}
