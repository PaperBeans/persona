package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func herdrSessionsHub(t *testing.T) (*Devices, *settingsStore, *Machine, http.Handler) {
	t.Helper()
	h, st, m, mux := dirsHub(t)
	out := http.NewServeMux()
	out.HandleFunc("GET /api/v1/machines/{mid}/herdr/sessions", h.withMachine(h.getHerdrSessions))
	out.HandleFunc("PUT /api/v1/machines/{mid}/herdr/sessions/blocked", h.withMachine(h.putHerdrSessionsBlocked))
	out.Handle("/", mux)
	return h, st, m, out
}

func TestHerdrSessionsRoutesOfflineAndValidation(t *testing.T) {
	h, st, _, mux := herdrSessionsHub(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/machines/m1/herdr/sessions", nil))
	var got proto.HerdrSessionsState
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Blocked) != 0 || got.Live {
		t.Fatalf("empty GET: %d %s", rec.Code, rec.Body.String())
	}

	code, body := put(t, mux, "/api/v1/machines/m1/herdr/sessions/blocked", `{"blocked":[" agent ","build"]}`)
	raw, _ := json.Marshal(body)
	if code != 200 || json.Unmarshal(raw, &got) != nil || !slices.Equal(got.Blocked, []string{"agent", "build"}) || got.Live {
		t.Fatalf("PUT: %d %s", code, raw)
	}

	// Reload from store on a fresh manager instance.
	h2 := newTestDevices(t)
	h2.opts.Store = dirsStore{st}
	m2 := newMachine(h2, MachineConfig{ID: "m1", Name: "box"})
	h2.addEntry(m2)
	got2, err := h2.HerdrSessions(context.Background(), "m1")
	if err != nil || !slices.Equal(got2.Blocked, []string{"agent", "build"}) {
		t.Fatalf("reloaded: %+v, %v", got2, err)
	}

	// Bad names, default, persona-*, duplicates are rejected with 400.
	for _, bad := range []string{
		`{"blocked":["default"]}`,
		`{"blocked":["persona-abc"]}`,
		`{"blocked":["bad/name"]}`,
		`{"blocked":["agent","agent"]}`,
		`not json`,
	} {
		if code, _ := put(t, mux, "/api/v1/machines/m1/herdr/sessions/blocked", bad); code != 400 {
			t.Fatalf("bad body %s -> %d, want 400", bad, code)
		}
	}

	// Empty list clears.
	code, body = put(t, mux, "/api/v1/machines/m1/herdr/sessions/blocked", `{"blocked":[]}`)
	raw, _ = json.Marshal(body)
	if code != 200 || json.Unmarshal(raw, &got) != nil || len(got.Blocked) != 0 {
		t.Fatalf("clear PUT: %d %s", code, raw)
	}
	_ = h
}

func TestHerdrSessionsLiveNodeAndConfigure(t *testing.T) {
	_, _, m, mux := herdrSessionsHub(t)
	f := connectDirsNode(t, m)

	told := func() [][]string {
		f.mu.Lock()
		defer f.mu.Unlock()
		var out [][]string
		for _, c := range f.configure {
			if raw, ok := c["herdr_sessions_blocked"]; ok {
				var list []string
				_ = json.Unmarshal(raw, &list)
				out = append(out, list)
			}
		}
		return out
	}

	code, body := put(t, mux, "/api/v1/machines/m1/herdr/sessions/blocked", `{"blocked":["agent"]}`)
	raw, _ := json.Marshal(body)
	var got proto.HerdrSessionsState
	if code != 200 || json.Unmarshal(raw, &got) != nil {
		t.Fatalf("PUT: %d %s", code, raw)
	}
	if !slices.Equal(got.Blocked, []string{"agent"}) {
		t.Fatalf("state = %+v", got)
	}

	eventually(t, 5*time.Second, "the node told the blocked herdr sessions", func() bool {
		list := told()
		return len(list) > 0 && slices.Equal(list[len(list)-1], []string{"agent"})
	})

	// Clear unblocks on the node.
	if code, _ := put(t, mux, "/api/v1/machines/m1/herdr/sessions/blocked", `{"blocked":[]}`); code != 200 {
		t.Fatalf("clear PUT: %d", code)
	}
	eventually(t, 5*time.Second, "the node told herdr sessions are unblocked", func() bool {
		list := told()
		return len(list) >= 2 && len(list[len(list)-1]) == 0
	})
}

type failingHerdrStore struct {
	dirsStore
	fail *bool
}

func (s failingHerdrStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if *s.fail && strings.HasPrefix(key, "herdr_sessions_blocked/") {
		return false, errors.New("database away")
	}
	return s.dirsStore.Setting(ctx, key, out)
}

func TestBlockedHerdrSessionsStoreFailureSendsNull(t *testing.T) {
	_, st, m, _ := dirsHub(t)
	fail := true
	m.d.opts.Store = failingHerdrStore{dirsStore{st}, &fail}
	f := connectDirsNode(t, m)
	ctx := context.Background()
	sent := func() string {
		f.mu.Lock()
		defer f.mu.Unlock()
		raw, ok := f.configure[len(f.configure)-1]["herdr_sessions_blocked"]
		if !ok {
			return "<absent>"
		}
		return string(raw)
	}
	m.configureNode(ctx, m.peer, nil, true)
	if got := sent(); got != "null" {
		t.Fatalf("a failed rules read sent herdr_sessions_blocked %s, want null", got)
	}

	fail = false
	m.herdrSessionsBlocked.mu.Lock()
	m.herdrSessionsBlocked.failedAt = time.Time{}
	m.herdrSessionsBlocked.mu.Unlock()
	m.configureNode(ctx, m.peer, nil, true)
	if got := sent(); got != "[]" {
		t.Fatalf("a successful empty read sent herdr_sessions_blocked %s, want []", got)
	}
}
