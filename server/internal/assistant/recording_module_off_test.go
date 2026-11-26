package assistant

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/recordinghost"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// A module that is off keeps what the user has (docs/architecture/core-and-modules.md, the rule of #13430): with the
// recording module switched off or left out of the build, a recording stored earlier is still listed, opened
// (the knowledge routes, kb_list, kb_get) and found by search, its files byte for byte as they were; the module's
// rounds write nothing; the user can still delete it. It runs the same in every build (default,
// persona_without_recording, persona_core_only): the recording is written through the knowledge base's own commit
// before the service opens, as a hub with the module would have left it.

const (
	moduleOffRec     = "44444444-4444-4444-8444-444444444444" // a meeting with its transcript and minutes
	moduleOffPending = "55555555-5555-4555-8555-555555555555" // a meeting whose minutes were not written yet
)

// moduleOffMinutes is the minutes the fixture's first recording has, as the recording module's minutes round leaves
// them (the round is the module's own; a Core test writes the files it leaves).
var moduleOffMinutes = kb.Minutes{Version: 1, Revision: 1, Title: "Export review", When: "today",
	Points:    []kb.MinutesPoint{{Text: "Entries over 4 MB are dropped by the export", At: 10}},
	Decisions: []kb.MinutesItem{{Text: "This version keeps the quota", At: 20}},
	Markdown:  "# Export review\n\n## Points\n\n- [00:00:10] Entries over 4 MB are dropped by the export\n\n## Decisions\n\n- [00:00:20] This version keeps the quota"}

// seedModuleOffUpload stores upload u of a meeting as recording id the way the module's upload leaves it, through the
// knowledge base's own commit (the storage port): meeting.json first when the upload names hub transcription, then
// meta.json, segments.json and transcript.md under the undo journal. The upload is the module's
// (internal/modules/recording/writers); a Core test, which runs without the module too, writes the files it leaves.
func seedModuleOffUpload(t *testing.T, e *kb.Engine, id string, u recordingwire.RecordingUpload, now time.Time) {
	t.Helper()
	core, rid := kb.RecordingCore(e), recordingwire.ID(id)
	locks := core.Locks()
	locks.Recordings.Lock()
	defer locks.Recordings.Unlock()
	r := &recordingwire.Recording{ID: id, Revision: u.Revision, StartedAt: u.StartedAt.UTC(), Duration: u.Duration,
		Language: u.Language, Device: u.Device, Title: u.Title, Complete: u.Complete, Kind: u.Kind,
		Segments: len(u.Segments), Chars: recordingwire.RecordChars(u.Segments), Updated: now.UTC()}
	if recordingwire.TranscriptionMode(u.Transcription) == recordingwire.TranscriptionHub {
		m := recordingwire.Meeting{Transcription: &recordingwire.MeetingTranscription{Mode: recordingwire.TranscriptionHub}}
		recordingwire.SettleTranscription(m.Transcription, now)
		recordingwire.BumpRevs(&m, recordingwire.MeetingParts(recordingwire.Meeting{}, m))
		raw, err := json.MarshalIndent(&m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(core.Path(rid, recordingwire.FileMeeting)), 0o700); err != nil {
			t.Fatal(err)
		}
		locks.Files.Lock()
		err = core.WriteDurable(core.Path(rid, recordingwire.FileMeeting), append(raw, '\n'))
		locks.Files.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		r.Conv = &m
	}
	recordingwire.PlanMinutes(r)
	recordingwire.PlanRecap(r)
	r.Name = core.RecordingName(r)
	if err := core.CommitRecording(r, u.Segments); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// seedModuleOffRecordings writes the two recordings into dir the way a hub with the module does.
func seedModuleOffRecordings(t *testing.T, dir string, now time.Time) {
	t.Helper()
	st, err := kb.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &kb.Engine{Store: st, Owner: "Owner", Logf: func(string, ...any) {}}
	put := func(id string, start time.Time, texts ...string) {
		u := recordingwire.RecordingUpload{Revision: 1, StartedAt: start, Duration: float64(10 * len(texts)), Language: "en-US", Device: "phone", Complete: true, Kind: kb.RecordingMeeting}
		for i, text := range texts {
			u.Segments = append(u.Segments, kb.RecordingSegment{Start: float64(10 * i), End: float64(10*i + 9), Text: text})
		}
		seedModuleOffUpload(t, e, id, u, now)
	}
	put(moduleOffRec, now.Add(-2*time.Hour), "Hello, today we review the export", "Ada says entries over 4 MB are dropped by the export", "Then this version keeps the quota zebrafish", "Bo adds the regression test by Friday")
	// The photo, as the module's intake leaves it: the image, photos.json and photos.md, written through the store's
	// own primitives (the intake is the module's; a Core test writes the files it leaves).
	core, rid := kb.RecordingCore(e), recordingwire.ID(moduleOffRec)
	book := recordingwire.PhotoBook{Next: 2, Rev: 1, Photos: []recordingwire.StoredPhoto{{N: 1, ID: moduleOffPhoto.ID, At: moduleOffPhoto.At,
		Mime: moduleOffPhoto.Mime, Width: moduleOffPhoto.Width, Height: moduleOffPhoto.Height, Text: moduleOffPhoto.Text,
		Caption: moduleOffPhoto.Caption, AddedAt: now.UTC(), Digest: moduleOffPhoto.Digest}}}
	photos := core.Path(rid, recordingwire.DirAttachments)
	if err := os.MkdirAll(photos, 0o700); err != nil {
		t.Fatal(err)
	}
	files := core.Locks().Files
	files.Lock()
	err = core.WriteDurable(filepath.Join(photos, book.Photos[0].FileName()), moduleOffPhoto.Data)
	if err == nil {
		err = core.PutPhotoBook(rid, &book, now.Add(-2*time.Hour), core.Zone())
	}
	files.Unlock()
	if err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	// The minutes, as the module's round leaves them: minutes.json and minutes.md, and meta.json saying they are ready.
	raw, err := json.MarshalIndent(moduleOffMinutes, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	metaPath := st.Path(kb.KindRecording, moduleOffRec, "meta.json")
	if b, err := os.ReadFile(metaPath); err != nil || json.Unmarshal(b, &meta) != nil {
		t.Fatalf("seed: meta.json: %v", err)
	}
	meta["minutes_version"], meta["minutes_rev"], meta["minutes_state"] = 1, 1, kb.MinutesReady
	metaRaw, _ := json.MarshalIndent(meta, "", "  ")
	for name, data := range map[string][]byte{"minutes.json": append(raw, '\n'), "minutes.md": []byte(moduleOffMinutes.Markdown + "\n"),
		"meta.json": append(metaRaw, '\n')} {
		if err := os.WriteFile(st.Path(kb.KindRecording, moduleOffRec, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(moduleOffPending, now.Add(-time.Hour), "The second meeting is about the schedule", "A proposal is due next Wednesday")
}

// moduleOffPhoto is the photo the fixture's first meeting holds: bytes that no decoder would accept, as the store
// keeps what the phone sent.
var moduleOffPhoto = kb.PhotoUpload{ID: "66666666-6666-4666-8666-666666666666", At: 12, Mime: kb.PhotoJPEG,
	Data: []byte("\xff\xd8 photo bytes of the whiteboard \xff\xd9"), Width: 1600, Height: 1200, Text: "whiteboard", Caption: "Plan",
	Digest: "66666666|photo"}

// moduleOffGet is a GET with the given credentials role header ("" for none): the status, headers and raw body.
func moduleOffGet(s *Service, path, role string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	s.ServeHTTP(w, r)
	return w
}

func moduleOffFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(dir, kb.KindRecording)
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[rel] = string(raw)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func moduleOffRequest(t *testing.T, s *Service, method, path string) (int, map[string]json.RawMessage) {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	var out map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestStoredRecordingsOutliveTheirModule(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	dir := t.TempDir()
	seedModuleOffRecordings(t, dir, now)
	before := moduleOffFiles(t, dir)
	for _, name := range []string{"meta.json", "transcript.md", "minutes.json", "minutes.md"} {
		if before[filepath.Join(moduleOffRec, name)] == "" {
			t.Fatalf("the fixture has no %s", name)
		}
	}
	f := newFakeHub()
	f.mu.Lock()
	f.hello.KB = &KBSettings{}
	f.mu.Unlock()
	st := &memStore{}
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), KB: &KBOptions{Dir: dir},
		ModulesOff: []string{recordinghost.ModuleID}}))
	t.Cleanup(s.kbEnd)
	if s.recordingOn() {
		t.Fatal("the recording module is on")
	}
	e := kbEng(t, s)

	// The module's own routes are gone, as before.
	if code, _ := moduleOffRequest(t, s, "GET", "/api/v1/recordings"); code != http.StatusNotFound {
		t.Errorf("GET /api/v1/recordings: %d without the module", code)
	}
	// Listed.
	code, out := moduleOffRequest(t, s, "GET", "/api/v1/kb/recordings")
	var items []kb.Entry
	_ = json.Unmarshal(out["items"], &items)
	paused := map[string]string{}
	for _, it := range items {
		paused[it.ID] = it.Paused
	}
	if code != http.StatusOK || len(items) != 2 || !reflect.DeepEqual(paused, map[string]string{moduleOffRec: "", moduleOffPending: kb.PausedModuleOff}) {
		t.Errorf("the knowledge list: %d, paused by id %v", code, paused)
	}
	// Opened: the transcript, the minutes, and nothing paused on a recording that was finished.
	code, out = moduleOffRequest(t, s, "GET", "/api/v1/kb/recordings/"+moduleOffRec)
	var d kb.Detail
	_ = json.Unmarshal(out["detail"], &d)
	if code != http.StatusOK || len(d.Segments) != 4 || !strings.Contains(string(d.Minutes), "Export review") || !strings.Contains(d.Recent, "zebrafish") || d.Paused != "" {
		t.Errorf("the detail: %d, %d segments, minutes %s, paused %q", code, len(d.Segments), d.Minutes, d.Paused)
	}
	// What was in flight says so in the response only.
	code, out = moduleOffRequest(t, s, "GET", "/api/v1/kb/recordings/"+moduleOffPending)
	d = kb.Detail{}
	_ = json.Unmarshal(out["detail"], &d)
	if code != http.StatusOK || d.Paused != kb.PausedModuleOff || !strings.Contains(string(d.Meta), `"minutes_state":"pending"`) {
		t.Errorf("the pending recording's detail: %d, paused %q, meta %s", code, d.Paused, d.Meta)
	}
	// A stored photo's image is read with the module off, as the module served it: the stored bytes, their type, the
	// headers; the same refusals for a missing photo or recording and for a device without full credentials.
	photo := "/api/v1/recordings/" + moduleOffRec + "/photos/1"
	if w := moduleOffGet(s, photo, ""); w.Code != http.StatusOK || w.Body.String() != string(moduleOffPhoto.Data) ||
		w.Header().Get("Content-Type") != kb.PhotoJPEG || w.Header().Get("X-Content-Type-Options") != "nosniff" ||
		w.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("the stored photo: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if w := moduleOffGet(s, photo, roleFull); w.Code != http.StatusOK {
		t.Errorf("the stored photo with full credentials: %d", w.Code)
	}
	if w := moduleOffGet(s, photo, "scoped"); w.Code != http.StatusForbidden {
		t.Errorf("the stored photo without full credentials: %d", w.Code)
	}
	for path, want := range map[string]int{
		"/api/v1/recordings/" + moduleOffRec + "/photos/2":                 http.StatusNotFound,
		"/api/v1/recordings/" + moduleOffRec + "/photos/zero":              http.StatusNotFound,
		"/api/v1/recordings/" + moduleOffPending + "/photos/1":             http.StatusNotFound,
		"/api/v1/recordings/77777777-7777-4777-8777-777777777777/photos/1": http.StatusNotFound,
	} {
		if w := moduleOffGet(s, path, ""); w.Code != want {
			t.Errorf("GET %s: %d, want %d", path, w.Code, want)
		}
	}
	// The list is a Core read too; the module's writes stay absent, with no method-mismatch 405 in their place.
	list := "/api/v1/recordings/" + moduleOffRec + "/photos"
	code, out = moduleOffRequest(t, s, "GET", list)
	var photos []kb.Photo
	_ = json.Unmarshal(out["photos"], &photos)
	if code != http.StatusOK || len(photos) != 1 || photos[0].N != 1 || photos[0].ID != moduleOffPhoto.ID || photos[0].Text != "whiteboard" {
		t.Errorf("the stored photo list: %d %v", code, photos)
	}
	if w := moduleOffGet(s, list, "scoped"); w.Code != http.StatusForbidden {
		t.Errorf("the photo list without full credentials: %d", w.Code)
	}
	if code, out := moduleOffRequest(t, s, "GET", "/api/v1/recordings/77777777-7777-4777-8777-777777777777/photos"); code != http.StatusNotFound || string(out["code"]) != `"recording_not_found"` {
		t.Errorf("a missing recording's photo list: %d %v", code, out)
	}
	f.mu.Lock()
	f.hello.KB = &KBSettings{ReadOnly: true}
	f.mu.Unlock()
	if code, _ := moduleOffRequest(t, s, "HEAD", list); code != http.StatusOK {
		t.Errorf("HEAD photo list on a read-only knowledge base: %d", code)
	}
	f.mu.Lock()
	f.hello.KB = &KBSettings{Off: true}
	f.mu.Unlock()
	if code, out := moduleOffRequest(t, s, "GET", list); code != http.StatusServiceUnavailable || string(out["code"]) != `"kb_disabled"` {
		t.Errorf("the photo list with the knowledge base off: %d %v", code, out)
	}
	f.mu.Lock()
	f.hello.KB = &KBSettings{}
	f.mu.Unlock()
	for _, req := range []struct{ method, path string }{{"POST", list}, {"DELETE", photo}} {
		if code, _ := moduleOffRequest(t, s, req.method, req.path); code != http.StatusNotFound {
			t.Errorf("%s %s: %d without the module", req.method, req.path, code)
		}
	}
	// Found by search: a line of the transcript, and the minutes.
	for _, q := range []string{"zebrafish", "Export review"} {
		code, out = moduleOffRequest(t, s, "GET", "/api/v1/kb-search?results=1&q="+url.QueryEscape(q))
		if code != http.StatusOK || !strings.Contains(string(out["results"]), moduleOffRec) {
			t.Errorf("search %q: %d %s", q, code, out["results"])
		}
	}
	// The assistant's tools read it the same way.
	if got, err := runTool(s, "c1", "kb_list", map[string]any{"kind": "recordings"}); err != nil || !strings.Contains(got, moduleOffRec) || !strings.Contains(got, moduleOffPending) {
		t.Errorf("kb_list: %v\n%s", err, got)
	}
	if got, err := runTool(s, "c1", "kb_get", map[string]any{"kind": "recordings", "id": moduleOffRec}); err != nil || !strings.Contains(got, "zebrafish") || !strings.Contains(got, "Export review") {
		t.Errorf("kb_get: %v\n%s", err, got)
	}
	if got, err := runTool(s, "c1", "kb_search", map[string]any{"query": "zebrafish"}); err != nil || !strings.Contains(got, moduleOffRec) {
		t.Errorf("kb_search: %v\n%s", err, got)
	}
	// The module's lanes and its part of the retention round run and write nothing: no model is asked, and the
	// minutes that were pending stay pending.
	f.mu.Lock()
	asked := f.modelRequests
	f.mu.Unlock()
	ctx := context.Background()
	for _, l := range s.kbLaneSet(&kbRounds{}).lanes {
		if l.name == "minutes" || l.name == "titles" || l.name == "recap" {
			l.run(ctx, e)
		}
	}
	if n := e.RecordingProvider().SummarizeRecordings(ctx, 3); n != 0 {
		t.Errorf("summarized %d recordings without the module", n)
	}
	if n := e.PruneRecordings(now); n != 0 {
		t.Errorf("retention removed %d recordings within recording_days", n)
	}
	f.mu.Lock()
	if f.modelRequests != asked {
		t.Errorf("%d model requests from the recording rounds without the module", f.modelRequests-asked)
	}
	f.mu.Unlock()
	if after := moduleOffFiles(t, dir); !reflect.DeepEqual(after, before) {
		for name := range after {
			if after[name] != before[name] {
				t.Errorf("%s changed or appeared without the module", name)
			}
		}
		for name := range before {
			if _, ok := after[name]; !ok {
				t.Errorf("%s is gone without the module", name)
			}
		}
	}
	// Deleting stays the user's: the knowledge base's own route, with or without the module.
	if code, _ := moduleOffRequest(t, s, "DELETE", "/api/v1/kb/recordings/not-a-uuid"); code != http.StatusBadRequest {
		t.Errorf("delete of a bad id: %d", code)
	}
	code, out = moduleOffRequest(t, s, "DELETE", "/api/v1/kb/recordings/"+moduleOffPending)
	if code != http.StatusOK || string(out["deleted"]) != "true" {
		t.Errorf("delete: %d %v", code, out)
	}
	if code, _ := moduleOffRequest(t, s, "GET", "/api/v1/kb/recordings/"+moduleOffPending); code != http.StatusNotFound {
		t.Errorf("the deleted recording's detail: %d", code)
	}
	after := moduleOffFiles(t, dir)
	for name, raw := range before {
		if strings.HasPrefix(name, moduleOffRec+string(filepath.Separator)) && after[name] != raw {
			t.Errorf("%s changed when another recording was deleted", name)
		}
		if strings.HasPrefix(name, moduleOffPending+string(filepath.Separator)) {
			if _, ok := after[name]; ok {
				t.Errorf("%s outlived its delete", name)
			}
		}
	}
	if code, out := moduleOffRequest(t, s, "DELETE", "/api/v1/kb/recordings/"+moduleOffPending); code != http.StatusOK || string(out["deleted"]) != "false" {
		t.Errorf("a second delete: %d %v", code, out)
	}
	if code, out := moduleOffRequest(t, s, "DELETE", "/api/v1/kb/recordings/"+moduleOffRec); code != http.StatusOK {
		t.Fatalf("delete the photo's recording: %d %v", code, out)
	}
	if code, out := moduleOffRequest(t, s, "GET", list); code != http.StatusGone || string(out["code"]) != `"recording_deleted"` {
		t.Errorf("a deleted recording's photo list: %d %v", code, out)
	}
}

// With the module, the knowledge base's delete route removes a recording as the module's own does, and nothing is
// paused.
func TestKBRecordingDeleteWithTheModule(t *testing.T) {
	needsRecordingModule(t)
	now := time.Now().Truncate(time.Second)
	dir := t.TempDir()
	seedModuleOffRecordings(t, dir, now)
	f := newFakeHub()
	f.mu.Lock()
	f.hello.KB = &KBSettings{}
	f.mu.Unlock()
	st := &memStore{}
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), KB: &KBOptions{Dir: dir}}))
	t.Cleanup(s.kbEnd)
	code, out := moduleOffRequest(t, s, "GET", "/api/v1/kb/recordings")
	if code != http.StatusOK || strings.Contains(string(out["items"]), "paused") {
		t.Errorf("the list with the module: %d %s", code, out["items"])
	}
	if code, out := moduleOffRequest(t, s, "GET", "/api/v1/kb/recordings/"+moduleOffPending); code != http.StatusOK || strings.Contains(string(out["detail"]), `"paused"`) {
		t.Errorf("the detail with the module: %d %s", code, out["detail"])
	}
	if code, out := moduleOffRequest(t, s, "DELETE", "/api/v1/kb/recordings/"+moduleOffRec); code != http.StatusOK || string(out["deleted"]) != "true" {
		t.Errorf("delete: %d %v", code, out)
	}
	if _, ok := kbEng(t, s).Store.Recording(moduleOffRec); ok {
		t.Error("the recording outlived its delete")
	}
	if code, _ := moduleOffRequest(t, s, "GET", "/api/v1/recordings"); code != http.StatusOK {
		t.Errorf("GET /api/v1/recordings with the module: %d", code)
	}
}

// The stored audio of a recording in flight is the module's input, never a thing the Core reads out (no route serves
// it, with the module or without): with the module off its chunk files stay on disk byte for byte as they were,
// and the module resumes from them when it is back.
func TestStoredAudioStaysUntouchedWithoutTheModule(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	dir := t.TempDir()
	st, err := kb.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &kb.Engine{Store: st, Owner: "Owner", Logf: func(string, ...any) {}}
	e.TranscriptionReady = func() bool { return true }
	const id = "88888888-8888-4888-8888-888888888888"
	u := recordingwire.RecordingUpload{Revision: 1, StartedAt: now.Add(-time.Hour), Duration: 10, Language: "en-US", Device: "phone", Kind: kb.RecordingMeeting,
		Segments: []kb.RecordingSegment{{Start: 0, End: 9, Text: "Hello"}}}
	u.SetTranscription(kb.TranscriptionHub, nil)
	seedModuleOffUpload(t, e, id, u, now)
	pcm := make([]byte, 2*kb.AudioRate*2) // two seconds
	for i := range pcm {
		pcm[i] = byte(i*7 + 1)
	}
	// The audio, as the module's writers leave it: a chunk file named by its first sample (they are the module's; a
	// Core test writes the file).
	chunk := filepath.Join(id, "attachments", "audio", "0.pcm")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, "recordings", chunk)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recordings", chunk), pcm, 0o600); err != nil {
		t.Fatalf("seed audio: %v", err)
	}
	before := moduleOffFiles(t, dir)
	if before[chunk] != string(pcm) {
		t.Fatalf("the fixture's chunk is not the stored audio (%d bytes)", len(before[chunk]))
	}
	f := newFakeHub()
	f.mu.Lock()
	f.hello.KB = &KBSettings{}
	f.mu.Unlock()
	ms := &memStore{}
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return ms }, Logf: testLogf(t), KB: &KBOptions{Dir: dir},
		ModulesOff: []string{recordinghost.ModuleID}}))
	t.Cleanup(s.kbEnd)
	if code, out := moduleOffRequest(t, s, "GET", "/api/v1/kb/recordings/"+id); code != http.StatusOK || !strings.Contains(string(out["detail"]), kb.PausedModuleOff) {
		t.Errorf("the in-flight recording's detail: %d %s", code, out["detail"])
	}
	for _, path := range []string{"/api/v1/recordings/" + id + "/audio", "/api/v1/kb/recordings/" + id + "/audio"} {
		if w := moduleOffGet(s, path, ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, no route serves stored audio", path, w.Code)
		}
	}
	ctx := context.Background()
	kbEng(t, s).PruneRecordings(now)
	for _, l := range s.kbLaneSet(&kbRounds{}).lanes {
		l.run(ctx, kbEng(t, s))
	}
	if after := moduleOffFiles(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("recording files changed or appeared without the module")
	}
}

// With the module the same Core route answers, with the same bytes and headers, and the module's own photo routes sit
// beside it (one owner per route: registering both would have panicked).
func TestStoredPhotoWithTheModule(t *testing.T) {
	needsRecordingModule(t)
	now := time.Now().Truncate(time.Second)
	dir := t.TempDir()
	seedModuleOffRecordings(t, dir, now)
	f := newFakeHub()
	f.mu.Lock()
	f.hello.KB = &KBSettings{}
	f.mu.Unlock()
	ms := &memStore{}
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return ms }, Logf: testLogf(t), KB: &KBOptions{Dir: dir}}))
	t.Cleanup(s.kbEnd)
	photo := "/api/v1/recordings/" + moduleOffRec + "/photos/1"
	if w := moduleOffGet(s, photo, ""); w.Code != http.StatusOK || w.Body.String() != string(moduleOffPhoto.Data) || w.Header().Get("Content-Type") != kb.PhotoJPEG {
		t.Errorf("the stored photo with the module: %d %q", w.Code, w.Body.String())
	}
	list := "/api/v1/recordings/" + moduleOffRec + "/photos"
	code, out := moduleOffRequest(t, s, "GET", list)
	var photos []kb.Photo
	_ = json.Unmarshal(out["photos"], &photos)
	if code != http.StatusOK || len(photos) != 1 || photos[0].ID != moduleOffPhoto.ID {
		t.Errorf("the Core photo list with the module: %d %v", code, photos)
	}
	for path, allow := range map[string]string{list: "GET, HEAD, POST", photo: "DELETE, GET, HEAD"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("PUT", path, nil))
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != allow {
			t.Errorf("PUT %s with the module: %d, Allow %q", path, w.Code, w.Header().Get("Allow"))
		}
	}
	if code, out := moduleOffRequest(t, s, "DELETE", photo); code != http.StatusOK || string(out["deleted"]) != "true" {
		t.Errorf("the module's photo delete: %d %v", code, out)
	}
	if w := moduleOffGet(s, photo, ""); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "photo_not_found") {
		t.Errorf("a deleted photo: %d %s", w.Code, w.Body.String())
	}
	for _, n := range []string{"9", "0", "abc", "-1"} {
		if w := moduleOffGet(s, "/api/v1/recordings/"+moduleOffRec+"/photos/"+n, ""); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "photo_not_found") {
			t.Errorf("photo %s: %d %s", n, w.Code, w.Body.String())
		}
	}
	if code, _ := moduleOffRequest(t, s, "DELETE", "/api/v1/kb/recordings/"+moduleOffRec); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if w := moduleOffGet(s, photo, ""); w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "recording_deleted") {
		t.Errorf("a photo of a deleted recording: %d %s", w.Code, w.Body.String())
	}
	if code, out := moduleOffRequest(t, s, "GET", list); code != http.StatusGone || string(out["code"]) != `"recording_deleted"` {
		t.Errorf("the photo list of a deleted recording: %d %v", code, out)
	}
}
