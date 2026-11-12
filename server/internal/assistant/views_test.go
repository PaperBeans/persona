package assistant

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type fakeViewsStore struct {
	memStore
	lock  sync.Mutex
	views map[string]db.AssistantViewRow
	feed  []db.FeedRow
}

func (f *fakeViewsStore) CreateAssistantView(_ context.Context, v db.AssistantViewRow, feed db.FeedRow) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.views == nil {
		f.views = map[string]db.AssistantViewRow{}
	}
	f.views[v.ID] = v
	f.feed = append(f.feed, feed)
	return nil
}
func (f *fakeViewsStore) AssistantView(_ context.Context, c, id string) (*db.AssistantViewRow, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	v, ok := f.views[id]
	if !ok || v.Conversation != c {
		return nil, db.ErrAssistantViewNotFound
	}
	return &v, nil
}
func (f *fakeViewsStore) AssistantViews(_ context.Context, c string) ([]db.AssistantViewRow, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	out := []db.AssistantViewRow{}
	for _, v := range f.views {
		if v.Conversation == c {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *fakeViewsStore) UpdateAssistantView(_ context.Context, c, id string, expected int64, raw json.RawMessage, at time.Time) (*db.AssistantViewRow, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	v, ok := f.views[id]
	if !ok || v.Conversation != c {
		return nil, db.ErrAssistantViewNotFound
	}
	if v.Version != expected {
		return nil, db.ErrAssistantViewVersion
	}
	v.View = raw
	v.Version++
	v.UpdatedAt = at
	f.views[id] = v
	return &v, nil
}
func (f *fakeViewsStore) Feed(_ context.Context, c string, since time.Time, limit int) ([]db.FeedRow, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	var out []db.FeedRow
	for _, v := range f.feed {
		if v.Conversation == c && v.At.After(since) && len(out) < limit {
			out = append(out, v)
		}
	}
	return out, nil
}

func newViewService(t *testing.T) (*Service, *fakeHub, *fakeViewsStore) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.setSessions("server", proto.Session{SessionID: "conversation-a", Assistant: true}, proto.Session{SessionID: "conversation-b", Assistant: true}, proto.Session{SessionID: "ordinary", Assistant: false})
	st := &fakeViewsStore{}
	s := newServiceOn(t, f, st)
	return s, f, st
}

func TestViewToolsPersistAndScope(t *testing.T) {
	s, f, st := newViewService(t)
	ctx := context.Background()
	out, err := s.toolView(ctx, "server", "conversation-a", "view_show", json.RawMessage(`{"view":{"fallback":"Progress","sections":[{"items":[{"type":"progress","label":"Build","ratio":0.2}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	var made struct {
		ID      string
		Version int64
	}
	_ = json.Unmarshal([]byte(out), &made)
	if !strings.HasPrefix(made.ID, "view_") || made.Version != 1 {
		t.Fatal(out)
	}
	row, err := st.AssistantView(ctx, "conversation-a", made.ID)
	if err != nil || row.MachineID != "server" {
		t.Fatalf("%+v %v", row, err)
	}
	if !strings.Contains(f.eventTypes(), "assistant_feed") {
		t.Fatal("no feed event")
	}
	args := json.RawMessage(`{"id":"` + made.ID + `"}`)
	if _, err := s.toolView(ctx, "server", "conversation-b", "view_get", args); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("foreign read: %v", err)
	}
	update := json.RawMessage(`{"id":"` + made.ID + `","version":1,"view":{"fallback":"Done","sections":[]}}`)
	if _, err := s.toolView(ctx, "server", "conversation-b", "view_update", update); err == nil {
		t.Fatal("foreign update accepted")
	}
	if _, err := s.toolView(ctx, "server", "conversation-a", "view_update", update); err != nil {
		t.Fatal(err)
	}
	if _, err := s.toolView(ctx, "server", "conversation-a", "view_update", update); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("stale update: %v", err)
	}
	read, err := s.toolView(ctx, "server", "conversation-a", "view_get", args)
	if err != nil || !strings.Contains(read, `"version":2`) {
		t.Fatalf("%s %v", read, err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/assistant/conversations/conversation-a/feed", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"views"`) || !strings.Contains(w.Body.String(), `"fallback":"Done"`) {
		t.Fatalf("feed %d %s", w.Code, w.Body.String())
	}
	if len(f.pushed()) != 0 {
		t.Fatal("view created a push notification")
	}
}

func TestViewToolsRejectInvalidAndNonAssistant(t *testing.T) {
	s, f, _ := newViewService(t)
	ctx := context.Background()
	valid := json.RawMessage(`{"view":{"fallback":"Example","sections":[]}}`)
	if _, err := s.toolView(ctx, "server", "ordinary", "view_show", valid); err == nil {
		t.Fatal("ordinary agent created a card")
	}
	if _, err := s.toolView(ctx, "server", "missing", "view_show", valid); err == nil {
		t.Fatal("unknown conversation created a card")
	}
	if _, err := s.toolView(ctx, "server", "conversation-a", "view_show", json.RawMessage(`{"view":{"sections":[]}}`)); err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("bad view: %v", err)
	}
	// An older hub's persona_view false is not read: the tools are always there and work (#5509).
	f.mu.Lock()
	f.hello.PersonaView = false
	f.mu.Unlock()
	if _, err := s.toolView(ctx, "server", "conversation-a", "view_show", valid); err != nil {
		t.Fatalf("view_show with an older hub's switch off: %v", err)
	}
	n := 0
	for _, tool := range s.Tools(ctx) {
		if strings.HasPrefix(tool.Name, "view_") {
			n++
			if len(tool.InputSchema) == 0 {
				t.Fatal("no tool schema")
			}
		}
	}
	if n != 3 {
		t.Fatalf("view tools %d", n)
	}
	storeless := newStorelessService(t, f)
	if _, err := storeless.toolView(ctx, "server", "conversation-a", "view_show", valid); err == nil || !strings.Contains(err.Error(), "durable") {
		t.Fatalf("storeless: %v", err)
	}
}

func TestViewInvalidUpdateKeepsLastVersion(t *testing.T) {
	s, _, st := newViewService(t)
	ctx := context.Background()
	v := db.AssistantViewRow{ID: "view_example", Conversation: "conversation-a", MachineID: "server", Version: 1, View: json.RawMessage(`{"fallback":"Keep","sections":[]}`)}
	if err := st.CreateAssistantView(ctx, v, db.FeedRow{}); err != nil {
		t.Fatal(err)
	}
	_, err := s.toolView(ctx, "server", "conversation-a", "view_update", json.RawMessage(`{"id":"view_example","view":{"sections":[]}}`))
	if err == nil {
		t.Fatal("invalid update accepted")
	}
	got, err := st.AssistantView(ctx, v.Conversation, v.ID)
	if err != nil || got.Version != 1 || string(got.View) != string(v.View) {
		t.Fatalf("%+v %v", got, err)
	}
}

var _ viewStore = (*fakeViewsStore)(nil)
