package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/healthhost"
	"github.com/nwverse/persona/server/internal/moduleswitch"
	"github.com/nwverse/persona/server/internal/proto"
)

// The Health module (internal/modules/health) as the rest of the manager meets it: the core's space guard of its tool,
// the core's fitting of its intake page for older apps (extension.Fit), the push outbox its reminders go through, and
// what is left without it. The module's own tests run on a fake core in its package.

// needsHealthModule skips t when the build has no Health module (persona_without_health, persona_core_only).
func needsHealthModule(t testing.TB) {
	t.Helper()
	if !healthhost.InBuild(moduleswitch.Set{}) {
		t.Skip("the Health module is not in this build (persona_without_health, persona_core_only)")
	}
}

const intakeTestID = "00000000-0000-4000-8000-000000000001"

// intakeTestStore keeps intake entries in memory (the module's tests keep the same one, with the Postgres store's
// commit ordering tested there on the real SQL).
type intakeTestStore struct {
	memStore
	intakeMu sync.Mutex
	rows     map[string]db.HealthIntakeRow
	seq      int64
	fail     bool
}

func (m *intakeTestStore) HealthIntakeAdd(_ context.Context, v db.HealthIntakeRow) (db.HealthIntakeRow, bool, error) {
	m.intakeMu.Lock()
	defer m.intakeMu.Unlock()
	if m.fail {
		return v, false, errors.New("synthetic storage failure")
	}
	if old, ok := m.rows[v.ID]; ok {
		return old, false, nil
	}
	if m.rows == nil {
		m.rows = map[string]db.HealthIntakeRow{}
	}
	m.seq++
	v.Seq, v.CreatedAt = m.seq, time.Now().UTC()
	m.rows[v.ID] = v
	return v, true, nil
}

func (m *intakeTestStore) HealthIntakeUndo(_ context.Context, id string) (*db.HealthIntakeRow, bool, error) {
	m.intakeMu.Lock()
	defer m.intakeMu.Unlock()
	if m.fail {
		return nil, false, errors.New("synthetic storage failure")
	}
	v, ok := m.rows[id]
	if !ok {
		return nil, false, nil
	}
	changed := v.DeletedAt == nil
	if changed {
		m.seq++
		now := time.Now().UTC()
		v.DeletedAt, v.Seq = &now, m.seq
		m.rows[id] = v
	}
	return &v, changed, nil
}

func (m *intakeTestStore) HealthIntakeRange(_ context.Context, from, until time.Time) ([]db.HealthIntakeRow, error) {
	m.intakeMu.Lock()
	defer m.intakeMu.Unlock()
	if m.fail {
		return nil, errors.New("synthetic storage failure")
	}
	out := []db.HealthIntakeRow{}
	for _, v := range m.rows {
		if v.DeletedAt == nil && !v.At.Before(from) && v.At.Before(until) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b db.HealthIntakeRow) int { return b.At.Compare(a.At) })
	return out, nil
}

func (m *intakeTestStore) HealthIntakeChanges(_ context.Context, after int64, limit int) ([]db.HealthIntakeRow, error) {
	m.intakeMu.Lock()
	defer m.intakeMu.Unlock()
	if m.fail {
		return nil, errors.New("synthetic storage failure")
	}
	out := []db.HealthIntakeRow{}
	for _, v := range m.rows {
		if v.Seq > after {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b db.HealthIntakeRow) int { return int(a.Seq - b.Seq) })
	return out[:min(limit, len(out))], nil
}

var _ healthhost.IntakeStore = (*intakeTestStore)(nil)

func intakeHTTP(s *Service, method, path, body, role string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(RoleHeader, role)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func intakePageHTTP(s *Service, kind, action string, values map[string]string, state string, version int, role string) *httptest.ResponseRecorder {
	if state == "" {
		state = fmt.Sprintf(`{"kind":%q,"days":7}`, kind)
	}
	body, _ := json.Marshal(wire.UIRequest{Context: &wire.UIContext{Locale: "en", UIVersion: version}, State: state, Action: action, Values: values})
	return intakeHTTP(s, "POST", "/api/v1/assistant/health/intake/ui?space=health", string(body), role)
}

func intakePageResult(t *testing.T, w *httptest.ResponseRecorder) wire.ActionResult {
	t.Helper()
	var res wire.ActionResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || res.View == nil {
		t.Fatalf("page: %d %s", w.Code, w.Body)
	}
	if err := wire.CheckView(res.View); err != nil {
		t.Fatal(err)
	}
	return res
}

// healthRoutes are the module's routes, each with a request it answers when the module is loaded.
var healthRouteRequests = []struct{ method, path, body string }{
	{"GET", "/api/v1/assistant/health/items", ""},
	{"PUT", "/api/v1/assistant/health/items", `{"items":[],"revision":0}`},
	{"GET", "/api/v1/assistant/health/intake", ""},
	{"POST", "/api/v1/assistant/health/intake", `{"id":"` + intakeTestID + `","kind":"water"}`},
	{"DELETE", "/api/v1/assistant/health/intake/" + intakeTestID, ""},
	{"GET", "/api/v1/assistant/health/intake/changes", ""},
	{"POST", "/api/v1/assistant/health/intake/ui", `{"state":"{\"kind\":\"water\",\"days\":7}"}`},
	{"POST", "/api/v1/assistant/health/intake/import", ""},
	{"POST", "/api/v1/assistant/health/water/reminder", `{"reminder":"r","action":"snooze"}`},
}

type healthTellModuleTestStore struct {
	*intakeTestStore
	healthTellStore
}

func TestHealthFollowsItsModule(t *testing.T) {
	for _, off := range []bool{false, true} {
		t.Run(fmt.Sprintf("off=%v", off), func(t *testing.T) {
			absent := off || !healthhost.InBuild(moduleswitch.Set{})
			var modulesOff []string
			if off {
				modulesOff = []string{healthhost.ModuleID}
			}
			f, st := healthHub(), &healthTellModuleTestStore{intakeTestStore: &intakeTestStore{}, healthTellStore: &healthTellMemStore{}}
			s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), ModulesOff: modulesOff}))
			if !slices.Contains(s.Routes(), "POST /api/v1/assistant/health/tell") {
				t.Fatal("core Tell Health route absent")
			}
			if w := intakeHTTP(s, "POST", "/api/v1/assistant/health/tell", `{"client_id":"module-tell-client","text":"note"}`, "answer"); w.Code != http.StatusAccepted {
				t.Fatalf("Tell Health with module off=%v: %d %s", off, w.Code, w.Body)
			}
			if s.healthOn() == absent {
				t.Fatalf("Health layer loaded = %v with the module off = %v", s.healthOn(), off)
			}
			for _, rq := range healthRouteRequests {
				w := intakeHTTP(s, rq.method, rq.path, rq.body, "full")
				if (w.Code == 404 && !strings.Contains(w.Body.String(), "not_found")) != absent {
					t.Errorf("%s %s = %d %s with the module off = %v", rq.method, rq.path, w.Code, w.Body, off)
				}
				if slices.Contains(s.Routes(), rq.method+" "+strings.ReplaceAll(rq.path, intakeTestID, "{id}")) == absent {
					t.Errorf("route %s %s listed = %v with the module off = %v", rq.method, rq.path, !off, off)
				}
			}
			for _, name := range healthToolNames {
				listed := slices.ContainsFunc(s.ToolsFor(context.Background(), "server", "sleep-000-0000"), func(t proto.Tool) bool { return t.Name == name })
				if listed == absent {
					t.Errorf("%s listed = %v with the module off = %v", name, listed, off)
				}
			}
			caps := withHealth([]string{assistantwire.CapabilityConfigOwn, assistantwire.CapabilityHealthTell, assistantwire.CapabilityHealthIntake, assistantwire.CapabilityHealthItems}, s)
			if !slices.Contains(caps, assistantwire.CapabilityHealthTell) {
				t.Fatal("core Tell Health capability absent")
			}
			if slices.Contains(caps, assistantwire.CapabilityHealthIntake) == absent {
				t.Errorf("hello capabilities %v with the module off = %v", caps, off)
			}
			if slices.Contains(caps, assistantwire.CapabilityHealthItems) == absent {
				t.Errorf("health_items capabilities %v with module off = %v", caps, off)
			}
			if absent {
				if got := s.healthClaudeMD(context.Background(), "server"); strings.Contains(got, "`health_intake`") {
					t.Error("intake instructions remained without the module")
				}
				if _, err := s.healthTool(context.Background(), healthhost.ToolCall{Name: healthhost.Tool, Args: json.RawMessage(`{"action":"list"}`)}); err == nil {
					t.Error("health_intake ran without the module")
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				s.healthReminders(ctx) // returns at once: no loop without the module
			}
		})
	}
}

// The core keeps the tool to known Health conversations before the module reads any storage.
func TestHealthIntakeToolCallerBoundary(t *testing.T) {
	needsHealthModule(t)
	for _, tc := range []struct {
		machine, session string
		allowed          bool
	}{
		{"server", "main-0000-0000", false},
		{"server", "sleep-000-0000", true},
		{"server", "steps-000-0000", true},
		{"other", "remote-health", true},
		{"other", "health-subdir", true},
		{"server", "unknown", false},
		{"other", "sleep-000-0000", false},
		{"", "sleep-000-0000", false},
		{"server", "", false},
	} {
		t.Run(tc.machine+"/"+tc.session, func(t *testing.T) {
			f := healthHub()
			f.devices = append(f.devices, Device{ID: "other", Kind: "ssh"})
			f.sessions["other"] = []proto.Session{
				{SessionID: "remote-health", Space: SpaceHealth, Cwd: "/home/me/.persona/health", Assistant: true},
				{SessionID: "health-subdir", Cwd: "/home/me/.persona/health/reports", Assistant: true},
				{SessionID: "sleep-000-0000", Cwd: "/home/me/.persona/assistant", Assistant: true},
			}
			st := &intakeTestStore{}
			s := newServiceOn(t, f, st)
			setSettings(t, s, func(v *Settings) { v.MachineID = "server" })
			// A failing store distinguishes the space guard from a later storage error.
			st.fail = !tc.allowed
			out, err := s.runTool(context.Background(), tc.machine, tc.session, "health_intake", json.RawMessage(`{"action":"list"}`))
			if tc.allowed && err != nil {
				t.Fatalf("valid Health caller: %s %v", out, err)
			}
			if !tc.allowed && (err == nil || !strings.Contains(err.Error(), "known Health conversation")) {
				t.Fatalf("caller passed space guard: %s %v", out, err)
			}
		})
	}
}

// Older apps get the intake page fitted by the core (extension.Fit): a gauge as its value and subtitle.
func TestHealthIntakePageCategoryColors(t *testing.T) {
	needsHealthModule(t)
	s := newServiceOn(t, newFakeHub(), &intakeTestStore{})
	for _, tc := range []struct{ kind, color string }{{"water", "blue"}, {"protein", "amber"}} {
		for _, version := range []int{6, 9, 11} {
			for _, days := range []int{7, 30} {
				t.Run(fmt.Sprintf("%s/v%d/%d", tc.kind, version, days), func(t *testing.T) {
					state := fmt.Sprintf(`{"kind":%q,"days":%d}`, tc.kind, days)
					res := intakePageResult(t, intakePageHTTP(s, tc.kind, "", nil, state, version, "full"))
					tile := res.View.Sections[0].Items[0].Tiles[0]
					if tile.Color != tc.color {
						t.Fatalf("%s tile color: %+v", tc.kind, tile)
					}
					if version >= 9 {
						if tile.Gauge == nil || tile.Gauge.Color != tc.color || *tile.Gauge.Ratio != 0 {
							t.Fatalf("%s gauge: %+v", tc.kind, tile.Gauge)
						}
					} else if tile.Gauge != nil || !strings.Contains(tile.Subtitle, "0%") {
						t.Fatalf("legacy gauge did not retain percentage: %+v", tile)
					}
					chart := res.View.Sections[3].Items[1]
					if chart.Kind != "bar" || len(chart.Series) != 1 || chart.Series[0].Color != tc.color || chart.Series[0].Tone != "" {
						t.Fatalf("%s chart color: %+v", tc.kind, chart)
					}
				})
			}
		}
	}
}

func TestHealthIntakePageLegacyProgressTotals(t *testing.T) {
	needsHealthModule(t)
	s := newServiceOn(t, newFakeHub(), &intakeTestStore{})
	for _, kind := range []string{"water", "protein"} {
		for _, version := range []int{1, 4, 5} {
			t.Run(fmt.Sprintf("%s/v%d", kind, version), func(t *testing.T) {
				res := intakePageResult(t, intakePageHTTP(s, kind, "", nil, "", version, "full"))
				row := res.View.Sections[0].Items[0]
				unit := map[string]string{"water": "ml", "protein": "g"}[kind]
				if row.Type != "row" || row.Value != "0 "+unit || !strings.Contains(row.Subtitle, "0%") {
					t.Fatalf("legacy totals lost: %+v", row)
				}
			})
		}
	}
}

// The push outbox keeps a water reminder's Health context while the hub is away.
func TestHealthWaterOutboxPreservesReminderContext(t *testing.T) {
	h := newOutboxHub(t)
	out := &memOutbox{}
	b := startOutboxManager(t, h, out)
	n := Notification{ID: "synthetic-reminder", Kind: assistantwire.NotifyHealthWater, Level: assistantwire.LevelP1,
		Title: "Time for some water", Health: &assistantwire.NotifyHealth{Reminder: "synthetic-reminder"}}
	res, err := b.NotifyRequest(context.Background(), NotifyRequest{Notification: n})
	if err != nil || !res.Queued || res.Devices != nil {
		t.Fatalf("offline: %+v %v", res, err)
	}
	items, err := out.OutboxItems(context.Background(), 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("outbox: %v %v", items, err)
	}
	var saved Notification
	if err := json.Unmarshal(items[0].Body, &saved); err != nil || saved.Health == nil || saved.Health.Reminder != n.Health.Reminder || saved.Level != assistantwire.LevelP1 || saved.RelatedKey != "" {
		t.Fatalf("queued context lost: %+v %v", saved, err)
	}
	h.up.Store(true)
	h.waitFor(t, []string{"push synthetic-reminder"})
}
