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
)

// A module that is off keeps what the user has (docs/architecture/core-and-modules.md, the rule of #13430): with the
// recording module switched off or left out of the build, a recording stored earlier is still listed, opened
// (the knowledge routes, kb_list, kb_get) and found by search, its files byte for byte as they were; the module's
// rounds write nothing; the user can still delete it. It runs the same in every build (default,
// persona_without_recording, persona_core_only): the recording is written by the knowledge base's own writers before
// the service opens, as a hub with the module would have left it.

const (
	moduleOffRec     = "44444444-4444-4444-8444-444444444444" // a meeting with its transcript and minutes
	moduleOffPending = "55555555-5555-4555-8555-555555555555" // a meeting whose minutes were not written yet
)

// moduleOffMinutes is the model's answer the fixture's minutes are written from.
type moduleOffMinutes struct{}

func (moduleOffMinutes) JSONMax(_ context.Context, _, _ string, _ int, out any) error {
	return json.Unmarshal([]byte(`{"title": "Export review", "points": [{"text": "Entries over 4 MB are dropped by the export", "line": 2}],
		"decisions": [{"text": "This version keeps the quota", "line": 3}]}`), out)
}

// seedModuleOffRecordings writes the two recordings into dir the way a hub with the module does.
func seedModuleOffRecordings(t *testing.T, dir string, now time.Time) {
	t.Helper()
	st, err := kb.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &kb.Engine{Store: st, Model: moduleOffMinutes{}, Owner: "Owner", Logf: func(string, ...any) {}}
	e.SetRecordingProvider(kb.RecordingWriters(e))
	put := func(id string, start time.Time, texts ...string) {
		u := kb.RecordingUpload{Revision: 1, StartedAt: start, Duration: float64(10 * len(texts)), Language: "en-US", Device: "phone", Complete: true, Kind: kb.RecordingMeeting}
		for i, text := range texts {
			u.Segments = append(u.Segments, kb.RecordingSegment{Start: float64(10 * i), End: float64(10*i + 9), Text: text})
		}
		if p, err := e.PutRecording(id, u, now); err != nil || p.Status != kb.RecordingStored {
			t.Fatalf("seed %s: %+v %v", id, p, err)
		}
	}
	put(moduleOffRec, now.Add(-2*time.Hour), "Hello, today we review the export", "Ada says entries over 4 MB are dropped by the export", "Then this version keeps the quota zebrafish", "Bo adds the regression test by Friday")
	if n := e.RecordingProvider().WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("seed: wrote %d minutes, want 1", n)
	}
	put(moduleOffPending, now.Add(-time.Hour), "The second meeting is about the schedule", "A proposal is due next Wednesday")
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
