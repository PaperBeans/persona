package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

type labsSeenHub struct {
	*fakeHub
	events chan json.RawMessage
}

func (h labsSeenHub) Broadcast(ctx context.Context, b Broadcast) {
	h.fakeHub.Broadcast(ctx, b)
	h.events <- b.Message
}

func readLabsSeen(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var view struct {
		Seen []string `json:"labs_seen"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); w.Code != http.StatusOK || err != nil || view.Seen == nil {
		t.Fatalf("want a successful view with a labs_seen array, got %d %s", w.Code, w.Body)
	}
	return view.Seen
}

func TestLabsSeenSettingsRouteAndBroadcast(t *testing.T) {
	f := newFakeHub().withMachine()
	h := labsSeenHub{f, make(chan json.RawMessage, 8)}
	s := withSettings(t, f, New(Options{Hub: h, Logf: testLogf(t)}))
	e := &todoEnv{t: t, s: s, f: f}
	const path = "/api/v1/assistant"
	if seen := readLabsSeen(t, e.do("GET", path, "")); len(seen) != 0 {
		t.Fatalf("initial seen IDs = %v", seen)
	}
	if w := e.doAs(roleAnswer, "PUT", path, `{"labs_seen":["terminal"]}`); w.Code != http.StatusForbidden {
		t.Fatalf("answer role: %d %s", w.Code, w.Body)
	}
	want := []string{"terminal", "work_guide"}
	for _, body := range []string{`{"labs_seen":[" work_guide ","terminal","work_guide"]}`, `{"labs_seen":["terminal"]}`, `{"labs_seen":[]}`} {
		if got := readLabsSeen(t, e.doAs(roleFull, "PUT", path, body)); !slices.Equal(got, want) {
			t.Fatalf("PUT %s: %v, want %v", body, got, want)
		}
	}
	select {
	case raw := <-h.events:
		var event assistantwire.LabsSeenEvent
		if err := json.Unmarshal(raw, &event); err != nil || event.Type != assistantwire.LabsSeenEventType || !slices.Equal(event.LabsSeen, want) {
			t.Fatalf("broadcast = %s", raw)
		}
	default:
		t.Fatal("receipt was not broadcast")
	}
	if len(h.events) != 0 || len(f.pushed()) != 0 || len(f.lastSnapshot().Interactions) != 0 {
		t.Fatal("duplicate receipts broadcast or marking seen asked for approval")
	}
	want = []string{"terminal", "todos_v2", "work_guide"}
	if got := readLabsSeen(t, e.do("PUT", path, `{"labs_seen":["todos_v2"],"recent_task_chats":true}`)); !slices.Equal(got, want) {
		t.Fatalf("mixed settings update: %v, want %v", got, want)
	}
	if got := readLabsSeen(t, e.do("PUT", path, `{"main_tasks_quiet":false}`)); !slices.Equal(got, want) {
		t.Fatalf("unrelated settings update lost receipts: %v, want %v", got, want)
	}
	if v := s.settings(context.Background()); v.RecentTaskChats == nil || !*v.RecentTaskChats {
		t.Fatal("mixed update did not apply the other setting")
	}
}

func TestLabsSeenValidation(t *testing.T) {
	e := newTodoEnv(t, nil)
	const path = "/api/v1/assistant"
	makeBody := func(count int) string {
		ids := make([]string, count)
		for i := range ids {
			ids[i] = fmt.Sprintf("trial-%02d", i)
		}
		body, err := json.Marshal(map[string]any{"labs_seen": ids})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	for _, body := range []string{
		`{"labs_seen":"terminal"}`, `{"labs_seen":[42]}`, `{"labs_seen":[null]}`, `{"labs_seen":[" "]}`,
		`{"labs_seen":["a/b"]}`, `{"labs_seen":["A"]}`, `{"labs_seen":["_trial"]}`,
		`{"labs_seen":["` + strings.Repeat("a", 65) + `"]}`, makeBody(65),
	} {
		if w := e.do("PUT", path, body); w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: %d %s", body, w.Code, w.Body)
		}
	}
	if got := readLabsSeen(t, e.do("GET", path, "")); len(got) != 0 {
		t.Fatalf("invalid writes persisted: %v", got)
	}
	want := readLabsSeen(t, e.do("PUT", path, makeBody(64)))
	if len(want) != 64 {
		t.Fatalf("expected the limit to be inclusive, got %d IDs", len(want))
	}
	if w := e.do("PUT", path, `{"labs_seen":["trial-00","overflow"]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("union above the cap: %d %s", w.Code, w.Body)
	}
	if got := readLabsSeen(t, e.do("PUT", path, `{"labs_seen":["trial-01"]}`)); !slices.Equal(got, want) {
		t.Fatalf("receipt for an existing ID at the cap changed it: %v", got)
	}
}

func TestLabsSeenConcurrentUnionAndPersistence(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		const path = "/api/v1/assistant"
		var wg sync.WaitGroup
		start := make(chan struct{})
		var want []string
		for i := range 12 {
			id := fmt.Sprintf("trial-%02d", i)
			want = append(want, id)
			wg.Go(func() {
				<-start
				body := fmt.Sprintf(`{"labs_seen":[%q]}`, id)
				if w := e.do("PUT", path, body); w.Code != http.StatusOK {
					t.Errorf("PUT %s: %d %s", body, w.Code, w.Body)
				}
			})
		}
		close(start)
		wg.Wait()
		if got := readLabsSeen(t, e.do("GET", path, "")); !slices.Equal(got, want) {
			t.Fatalf("concurrent writes: %v, want %v", got, want)
		}
		if database != nil {
			reloaded := newTodoEnv(t, database)
			if got := readLabsSeen(t, reloaded.do("GET", path, "")); !slices.Equal(got, want) {
				t.Fatalf("on a restarted manager: %v, want %v", got, want)
			}
		}
	})
}
