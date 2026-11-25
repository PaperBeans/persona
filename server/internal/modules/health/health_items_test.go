package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

const itemsPath = "/api/v1/assistant/health/items"

func itemsReply(t *testing.T, w *httptest.ResponseRecorder, code int) healthItemsState {
	t.Helper()
	var v healthItemsState
	if w.Code != code || json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatalf("items: %d %s, want %d", w.Code, w.Body, code)
	}
	return v
}

func putItems(t *testing.T, s *Service, items []string, revision int64) healthItemsState {
	t.Helper()
	b, err := json.Marshal(healthItemsSetting{Items: items, Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	return itemsReply(t, intakeHTTP(s, "PUT", itemsPath, string(b), "full"), 200)
}

func TestHealthItemsDefaults(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"fresh", "entry", "undone", "settings", "pending_enabled", "pending_disabled", "imported"} {
		t.Run(name, func(t *testing.T) {
			st := &intakeTestStore{}
			f := &importWaterHub{fakeHub: newFakeHub()}
			s := New(&fakeCore{o: Options{Hub: f, Store: func() Store { return st }}})
			switch name {
			case "entry", "undone":
				_, _, err := st.HealthIntakeAdd(ctx, assistantwire.HealthIntakeRow{ID: intakeTestID, Kind: "water", Amount: 250, At: time.Now()})
				if err != nil {
					t.Fatal(err)
				}
				if name == "undone" {
					if _, _, err := st.HealthIntakeUndo(ctx, intakeTestID); err != nil {
						t.Fatal(err)
					}
				}
			case "settings":
				if err := st.PutSetting(ctx, healthIntakeKey, defaultHealthIntakeSettings()); err != nil {
					t.Fatal(err)
				}
			case "pending_enabled", "pending_disabled", "imported":
				f.water = &assistantwire.WaterExtension{Enabled: name == "pending_enabled"}
				if name == "imported" {
					if err := st.PutSetting(ctx, waterImportKey, waterImportReceipt{}); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := st.puts
			for range 2 {
				v := itemsReply(t, intakeHTTP(s, "GET", itemsPath, "", "full"), 200)
				want := []string{"water", "protein", "activity", "trends"}
				if name == "fresh" || name == "imported" {
					want = []string{"activity", "trends"}
				}
				if !slices.Equal(v.Items, want) || v.Saved || v.Revision != 0 || !reflect.DeepEqual(v.Catalog, healthCatalog) {
					t.Fatalf("defaults: %+v", v)
				}
			}
			if st.puts != before {
				t.Fatal("reading defaults wrote settings")
			}
		})
	}
}

func TestHealthItemsDefaultsRecomputedUntilSaved(t *testing.T) {
	ctx := context.Background()
	st := &intakeTestStore{}
	s := newServiceOn(t, newFakeHub(), st)
	first, err := s.Items(ctx)
	if err != nil || slices.Contains(first, "water") {
		t.Fatalf("fresh: %v %v", first, err)
	}
	if err := st.PutSetting(ctx, healthIntakeKey, defaultHealthIntakeSettings()); err != nil {
		t.Fatal(err)
	}
	next, err := s.Items(ctx)
	if err != nil || !slices.Contains(next, "water") {
		t.Fatalf("defaults were cached: %v %v", next, err)
	}
	putItems(t, s, []string{}, 0)
	s = newServiceOn(t, newFakeHub(), st) // Saved empty means empty, including after a restart.
	v := itemsReply(t, intakeHTTP(s, "GET", itemsPath, "", "full"), 200)
	if !v.Saved || v.Revision != 1 || v.Items == nil || len(v.Items) != 0 {
		t.Fatalf("explicit empty lost: %+v", v)
	}
}

type itemsEventHost struct {
	Host
	mu     sync.Mutex
	events []map[string]any
}

func (h *itemsEventHost) Event(_ context.Context, ev map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, ev)
}

func TestHealthItemsRevisionAndBroadcast(t *testing.T) {
	st := &intakeTestStore{}
	s := newServiceOn(t, newFakeHub(), st)
	h := &itemsEventHost{Host: s.h}
	s.h = h
	v := putItems(t, s, []string{"trends", "water", "activity"}, 0)
	if v.Revision != 1 || !v.Saved || !slices.Equal(v.Items, []string{"trends", "water", "activity"}) {
		t.Fatal(v)
	}
	stale := itemsReply(t, intakeHTTP(s, "PUT", itemsPath, `{"items":["protein"],"revision":0}`, "full"), 409)
	if !reflect.DeepEqual(stale, v) || len(h.events) != 1 {
		t.Fatalf("stale write changed items: %+v, events %v", stale, h.events)
	}
	// Even an identical successful save advances once; rejected saves do neither.
	v = putItems(t, s, v.Items, v.Revision)
	if v.Revision != 2 || len(h.events) != 2 || h.events[0]["revision"] != int64(1) || h.events[1]["revision"] != int64(2) || h.events[0]["type"] != "health_items" {
		t.Fatalf("broadcasts: %+v %v", v, h.events)
	}
	st.setFailPut(errors.New("synthetic write failure"))
	if w := intakeHTTP(s, "PUT", itemsPath, `{"items":[],"revision":2}`, "full"); w.Code != 503 || len(h.events) != 2 {
		t.Fatalf("failed save: %d %s %v", w.Code, w.Body, h.events)
	}
	got := itemsReply(t, intakeHTTP(s, "GET", itemsPath, "", "full"), 200)
	if !reflect.DeepEqual(got, v) {
		t.Fatal("failed save changed state")
	}
}

func TestHealthItemsConcurrentSaves(t *testing.T) {
	s := newServiceOn(t, newFakeHub(), &intakeTestStore{})
	results := make(chan int, 2)
	for _, item := range []string{"water", "protein"} {
		go func() {
			results <- intakeHTTP(s, "PUT", itemsPath, fmt.Sprintf(`{"items":[%q],"revision":0}`, item), "full").Code
		}()
	}
	codes := []int{<-results, <-results}
	slices.Sort(codes)
	if !slices.Equal(codes, []int{200, 409}) {
		t.Fatalf("lost update: %v", codes)
	}
}

func TestHealthItemsValidationAndAccess(t *testing.T) {
	st, f := &intakeTestStore{}, newFakeHub()
	s := newServiceOn(t, f, st)
	for _, body := range []string{
		`{`, `null`, `[]`, `{}`, `{"items":[],"revision":null}`, `{"items":[],"revision":-1}`,
		`{"items":[],"revision":0.5}`, `{"items":[],"revision":9223372036854775808}`,
		`{"items":[],"revision":0,"extra":true}`, `{"items":[],"revision":0} {}`,
		`{"items":null,"revision":0}`, `{"items":["unknown"],"revision":0}`,
		`{"items":["water","water"],"revision":0}`, `{"items":[],"revision":0}` + strings.Repeat(" ", 64<<10),
	} {
		if w := intakeHTTP(s, "PUT", itemsPath, body, "full"); w.Code != 400 {
			t.Errorf("accepted %q: %d %s", body[:min(len(body), 150)], w.Code, w.Body)
		}
	}
	for _, role := range []string{"answer", "read", "extension", "scoped"} {
		for _, method := range []string{"GET", "PUT"} {
			if w := intakeHTTP(s, method, itemsPath, `{"items":[],"revision":0}`, role); w.Code != 403 {
				t.Errorf("%s %s: %d %s", role, method, w.Code, w.Body)
			}
		}
	}
	if st.puts != 0 || len(f.events) != 0 {
		t.Fatal("rejected request mutated settings or broadcast")
	}
	st.failKey(healthItemsKey, errors.New("synthetic read failure"))
	if w := intakeHTTP(s, "GET", itemsPath, "", "full"); w.Code != 503 {
		t.Fatal("read failure became defaults")
	}
}

func TestHealthItemsRemovalKeepsRecordsAndLegacyWrites(t *testing.T) {
	s := newServiceOn(t, newFakeHub(), &intakeTestStore{})
	putItems(t, s, []string{"activity", "trends"}, 0)
	// Older clients never consult health_items and can still write via their original route.
	if w := intakeHTTP(s, "POST", "/api/v1/assistant/health/intake", `{"id":"`+intakeTestID+`","kind":"water","amount":250}`, "answer"); w.Code != 200 {
		t.Fatalf("legacy write: %d %s", w.Code, w.Body)
	}
	read := func(water bool) {
		t.Helper()
		w := intakeHTTP(s, "GET", "/api/v1/assistant/health/intake", "", "answer")
		var v healthIntakeView
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Water.Today != 250 || len(v.Water.Entries) != 1 || slices.Contains(v.Items, "water") != water {
			t.Fatalf("records/list: %d %s", w.Code, w.Body)
		}
	}
	read(false)
	putItems(t, s, []string{"water", "activity", "trends"}, 1)
	read(true)
	putItems(t, s, []string{}, 2)
	read(false)
	if w := intakeHTTP(s, "GET", "/api/v1/assistant/health/intake/changes", "", "full"); w.Code != 200 || !strings.Contains(w.Body.String(), intakeTestID) {
		t.Fatalf("sync followed items: %d %s", w.Code, w.Body)
	}
	// Listing and deletion still work when the item is absent.
	if out, err := s.intakeTool(context.Background(), json.RawMessage(`{"action":"list"}`)); err != nil || !strings.Contains(out, intakeTestID) {
		t.Fatalf("list: %s %v", out, err)
	}
	if _, err := s.intakeTool(context.Background(), json.RawMessage(`{"action":"delete","id":"`+intakeTestID+`"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestHealthItemsToolGateAndAdd(t *testing.T) {
	ctx := context.Background()
	st, f := &intakeTestStore{}, newFakeHub()
	s := newServiceOn(t, f, st)
	for _, id := range []string{"water", "protein"} {
		out, err := s.intakeTool(ctx, json.RawMessage(fmt.Sprintf(`{"action":"log","kind":%q,"amount":20}`, id)))
		if err != nil || !strings.Contains(out, "Ask the user") || len(st.rows) != 0 || st.puts != 0 {
			t.Fatalf("missing item: %s %v", out, err)
		}
	}
	for _, args := range []string{`{"action":"add_item"}`, `{"action":"add_item","item":"unknown"}`, `{"action":"add_item","item":"water","amount":20}`, `{"action":"list","item":"water"}`} {
		if _, err := s.intakeTool(ctx, json.RawMessage(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	for i, id := range []string{"protein", "water"} {
		if _, err := s.intakeTool(ctx, json.RawMessage(fmt.Sprintf(`{"action":"add_item","item":%q}`, id))); err != nil {
			t.Fatal(err)
		}
		v := itemsReply(t, intakeHTTP(s, "GET", itemsPath, "", "full"), 200)
		if v.Revision != int64(i+1) || v.Items[len(v.Items)-1] != id || len(f.events) != i+1 {
			t.Fatalf("add: %+v, events %v", v, f.events)
		}
	}
	if _, err := s.intakeTool(ctx, json.RawMessage(`{"action":"add_item","item":"water"}`)); err != nil || len(f.events) != 2 {
		t.Fatalf("idempotent add: %v %v", err, f.events)
	}
	if out, err := s.intakeTool(ctx, json.RawMessage(`{"action":"log","kind":"water","amount":300}`)); err != nil || !strings.Contains(out, "Recorded water") || len(st.rows) != 1 {
		t.Fatalf("added item log: %s %v", out, err)
	}
}

func TestHealthItemsWaterReminderGate(t *testing.T) {
	ctx := context.Background()
	st, f := &intakeTestStore{}, newFakeHub()
	s := newServiceOn(t, f, st)
	setSettings(t, s, func(v *Settings) { v.Timezone = "UTC" })
	c := defaultHealthIntakeSettings()
	c.Water.Remind = true
	if err := st.PutSetting(ctx, healthIntakeKey, c); err != nil {
		t.Fatal(err)
	}
	putItems(t, s, []string{"protein"}, 0)
	now := waterTestTime("2026-11-16T10:00:00Z")
	if err := s.waterReminderTick(ctx, now); err != nil || len(f.pushed()) != 0 {
		t.Fatalf("removed water reminded: %v %v", err, f.pushed())
	}
	putItems(t, s, []string{"protein", "water"}, 1)
	if err := s.waterReminderTick(ctx, now); err != nil || len(f.pushed()) != 1 {
		t.Fatalf("added water did not remind: %v %v", err, f.pushed())
	}
}

func TestHealthItemsAbsentStillAcceptsImport(t *testing.T) {
	s, _, st := importHarness(t, false)
	putItems(t, s, []string{}, 0)
	if result := doWaterImport(t, s, 200); result.Imported != 3 {
		t.Fatal(result)
	}
	items, err := s.Items(context.Background())
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("import changed explicit items: %v %v", items, err)
	}
	changes, err := st.HealthIntakeChanges(context.Background(), 0, 100)
	if err != nil || len(changes) != 3 {
		t.Fatalf("import/sync followed items: %v %v", changes, err)
	}
}

func TestHealthItemsDatabasePersistence(t *testing.T) {
	s, _, _ := importHarness(t, true)
	putItems(t, s, []string{"water", "activity"}, 0)
	if w := intakeHTTP(s, "POST", "/api/v1/assistant/health/intake", `{"id":"`+intakeTestID+`","kind":"water","amount":300}`, "full"); w.Code != 200 {
		t.Fatalf("record: %d %s", w.Code, w.Body)
	}
	putItems(t, s, []string{}, 1)
	s = New(s.h) // No module-local state is needed to recover the list after a restart.
	v := itemsReply(t, intakeHTTP(s, "GET", itemsPath, "", "full"), 200)
	if !v.Saved || v.Revision != 2 || v.Items == nil || len(v.Items) != 0 {
		t.Fatalf("persisted state: %+v", v)
	}
	putItems(t, s, []string{"water"}, 2)
	w := intakeHTTP(s, "GET", "/api/v1/assistant/health/intake", "", "full")
	var intake healthIntakeView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &intake) != nil || intake.Water.Today != 300 ||
		len(intake.Water.Entries) != 1 || !slices.Equal(intake.Items, []string{"water"}) {
		t.Fatalf("records after re-adding: %d %s", w.Code, w.Body)
	}
}

func TestHealthItemsStoredFutureIDs(t *testing.T) {
	for _, realDB := range []bool{false, true} {
		t.Run(fmt.Sprintf("postgres=%v", realDB), func(t *testing.T) {
			ctx := context.Background()
			s := newServiceOn(t, newFakeHub(), &intakeTestStore{})
			if realDB {
				s, _, _ = importHarness(t, true)
			}
			st := s.store()
			stored := healthItemsSetting{Items: []string{"future-one", "water", "water", "future-two", "protein", "future-one"}, Revision: 7}
			if err := st.PutSetting(ctx, healthItemsKey, stored); err != nil {
				t.Fatal(err)
			}
			checkStored := func(items []string, revision int64) {
				t.Helper()
				var got healthItemsSetting
				if found, err := st.Setting(ctx, healthItemsKey, &got); err != nil || !found || got.Revision != revision || !slices.Equal(got.Items, items) {
					t.Fatalf("stored items: %+v, found=%v err=%v", got, found, err)
				}
			}
			w := intakeHTTP(s, "GET", itemsPath, "", "full")
			v := itemsReply(t, w, 200)
			if !v.Saved || v.Revision != 7 || !slices.Equal(v.Items, []string{"water", "protein"}) || strings.Contains(w.Body.String(), "future-") {
				t.Fatalf("future IDs leaked or blocked the read: %s", w.Body)
			}
			checkStored(stored.Items, 7) // Reading filters without rewriting anything.
			w = intakeHTTP(s, "GET", "/api/v1/assistant/health/intake", "", "full")
			var intake healthIntakeView
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &intake) != nil || !slices.Equal(intake.Items, v.Items) {
				t.Fatalf("intake blocked by future IDs: %d %s", w.Code, w.Body)
			}
			putItems(t, s, []string{"trends", "water"}, 7)
			checkStored([]string{"trends", "water", "future-one", "future-two", "future-one"}, 8)
			if _, err := s.intakeTool(ctx, json.RawMessage(`{"action":"add_item","item":"protein"}`)); err != nil {
				t.Fatal(err)
			}
			checkStored([]string{"trends", "water", "protein", "future-one", "future-two", "future-one"}, 9)
			v = itemsReply(t, intakeHTTP(s, "GET", itemsPath, "", "full"), 200)
			if !slices.Equal(v.Items, []string{"trends", "water", "protein"}) || v.Revision != 9 {
				t.Fatalf("add_item read: %+v", v)
			}
			if w := intakeHTTP(s, "PUT", itemsPath, `{"items":["future-one"],"revision":9}`, "full"); w.Code != 400 {
				t.Fatalf("unknown PUT input accepted: %d %s", w.Code, w.Body)
			}
			checkStored([]string{"trends", "water", "protein", "future-one", "future-two", "future-one"}, 9)
			// Unknown-only saved lists stay explicitly empty, never become fresh-owner defaults.
			if err := st.PutSetting(ctx, healthItemsKey, healthItemsSetting{Items: []string{"future-only", "future-only"}, Revision: 10}); err != nil {
				t.Fatal(err)
			}
			v = itemsReply(t, intakeHTTP(s, "GET", itemsPath, "", "full"), 200)
			if !v.Saved || v.Items == nil || len(v.Items) != 0 || v.Revision != 10 {
				t.Fatalf("unknown-only saved list: %+v", v)
			}
			putItems(t, s, []string{}, 10)
			checkStored([]string{"future-only", "future-only"}, 11)
		})
	}
}

func TestHealthItemsFutureIDsDoNotBlockReminders(t *testing.T) {
	ctx := context.Background()
	st, f := &intakeTestStore{}, newFakeHub()
	s := newServiceOn(t, f, st)
	setSettings(t, s, func(v *Settings) { v.Timezone = "UTC" })
	c := defaultHealthIntakeSettings()
	c.Water.Remind = true
	if err := st.PutSetting(ctx, healthIntakeKey, c); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSetting(ctx, healthItemsKey, healthItemsSetting{Items: []string{"future-one", "water", "water"}, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.waterReminderTick(ctx, waterTestTime("2026-11-16T10:00:00Z")); err != nil || len(f.pushed()) != 1 {
		t.Fatalf("future IDs blocked water reminder: %v %v", err, f.pushed())
	}
}

func TestHealthItemsWaterPageReminderStatus(t *testing.T) {
	ctx := context.Background()
	st, f := &intakeTestStore{}, newFakeHub()
	s := newServiceOn(t, f, st)
	setSettings(t, s, func(v *Settings) { v.Timezone = "UTC" })
	c := defaultHealthIntakeSettings()
	c.Water.Remind, c.Water.Weekends = true, true
	c.Water.Start, c.Water.End = "00:00", "23:59"
	if err := st.PutSetting(ctx, healthIntakeKey, c); err != nil {
		t.Fatal(err)
	}
	for i, items := range [][]string{{"protein"}, {"protein", "water"}} {
		putItems(t, s, items, int64(i))
		w := intakePageHTTP(s, "water", "", nil, "", 11, "full")
		intakePageResult(t, w)
		body := w.Body.String()
		absent := i == 0
		if strings.Contains(body, "Add Water to Health to receive reminders.") != absent ||
			strings.Contains(body, "Next reminder ") == absent {
			t.Fatalf("water added=%v: %s", !absent, body)
		}
	}
}
