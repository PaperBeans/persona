package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/healthhost"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/moduleswitch"
	"github.com/nwverse/persona/server/internal/proto"
)

func strengthCardHarness(t *testing.T) (*Service, *fakeHub, *db.DB, string, string) {
	t.Helper()
	needsHealthModule(t)
	url := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, url, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	conv := "strength-" + randomHex(8)
	f := healthHub()
	f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: conv, Assistant: true, Cwd: "/home/me/.persona/health", Space: SpaceHealth})
	s := newServiceOn(t, f, d)
	if err := d.PutSetting(ctx, "health.items", map[string]any{"items": []string{"strength"}, "revision": 1}); err != nil {
		t.Fatal(err)
	}
	eid := "00000000-0000-4000-8000-" + randomHex(6)
	if _, _, err := d.HealthStrengthExerciseSave(ctx, assistantwire.StrengthExercise{ID: eid, Name: "Press " + randomHex(8), WeightRule: "stack"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.runTool(ctx, "server", conv, "health_strength", json.RawMessage(`{"action":"draft","sets":[{"exercise":"`+eid+`","weight":40,"reps":10},{"exercise":"`+eid+`","weight":35,"reps":12}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct{ Card string }
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Card, healthhost.CardPrefix) {
		t.Fatal(out)
	}
	return s, f, d, conv, res.Card
}
func strengthActionHTTP(s *Service, conversation, key, action, client string, version int64) *httptest.ResponseRecorder {
	body, _ := json.Marshal(wire.ViewActionInput{Action: action, ClientMsgID: client, ExpectedVersion: version})
	return intakeHTTP(s, "POST", "/api/v1/assistant/conversations/"+conversation+"/views/"+key+"/actions", string(body), "full")
}
func TestHealthStrengthCardSaveHTTPNoTurnOrPush(t *testing.T) {
	s, f, d, conv, key := strengthCardHarness(t)
	ctx := context.Background()
	before := len(f.calls)
	seenBeforeResponse := false
	w := httptest.NewRecorder()
	f.onBroadcast = func() {
		f.mu.Lock()
		last := ""
		if len(f.events) > 0 {
			last = f.events[len(f.events)-1]
		}
		f.mu.Unlock()
		if last == "assistant_view" {
			if w.Body.Len() != 0 {
				t.Error("card update happened after the HTTP body")
			}
			row, err := d.AssistantView(ctx, conv, key)
			if err != nil || row.Version != 2 {
				t.Errorf("event before commit: %+v %v", row, err)
			}
			seenBeforeResponse = true
		}
	}
	r := httptest.NewRequest("POST", "/api/v1/assistant/conversations/"+conv+"/views/"+key+"/actions", strings.NewReader(`{"action":"save","client_msg_id":"tap","expected_version":1}`))
	r.Header.Set(RoleHeader, "full")
	s.ServeHTTP(w, r)
	f.onBroadcast = nil
	var result struct{ Action wire.ViewAction }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Action.Key != key || result.Action.ClientMsgID != "tap" || result.Action.Status != "sent" || result.Action.Action != "save" || result.Action.Version != 1 || result.Action.At.IsZero() {
		t.Fatalf("client response: %d %s", w.Code, w.Body)
	}
	if !seenBeforeResponse {
		t.Fatal("no immediate card event")
	}
	row, err := d.AssistantView(ctx, conv, key)
	if err != nil {
		t.Fatal(err)
	}
	v, err := wire.ParseAssistantView(row.View)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range wire.Actions(v) {
		if a.Reply != "" {
			t.Fatal("saved card still has reply buttons")
		}
	}
	if retry := strengthActionHTTP(s, conv, key, "save", "tap", 1); retry.Code != 200 {
		t.Fatal("retry", retry.Code, retry.Body)
	}
	if other := strengthActionHTTP(s, conv, key, "save", "another-tap", 1); other.Code != 409 || !strings.Contains(other.Body.String(), "already_answered") {
		t.Fatal("second save", other.Code, other.Body)
	}
	drafts, err := d.HealthStrengthDraftByCard(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	sets, err := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, set := range sets {
		if set.DraftID != nil && *set.DraftID == drafts.ID {
			count++
		}
	}
	if count != 2 {
		t.Fatal("save wrote duplicate sets", count)
	}
	actions, err := d.AssistantViewActions(ctx, conv)
	if err != nil || len(actions) != 0 {
		t.Fatal("module entered generic action outbox", actions, err)
	}
	for _, call := range f.calls[before:] {
		if call.Method == DeviceSessionSendOnce || call.Method == DeviceAssistantSend || call.Method == DeviceSessionSend {
			t.Fatal("card started a model turn", call.Method)
		}
	}
	if len(f.pushed()) != 0 {
		t.Fatal("card sent a push")
	}
}
func TestHealthStrengthCardHTTPGuards(t *testing.T) {
	s, f, _, conv, key := strengthCardHarness(t)
	for _, tc := range []struct {
		conversation, action string
		version              int64
		status               int
		code                 string
	}{{"other", "save", 1, 404, "not_found"}, {conv, "save", 2, 409, "view_changed"}, {conv, "save", 0, 400, ""}, {conv, "adjust", 1, 400, ""}} {
		w := strengthActionHTTP(s, tc.conversation, key, tc.action, "tap", tc.version)
		if w.Code != tc.status || tc.code != "" && !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body)
		}
	}
	for i := range f.sessions["server"] {
		if f.sessions["server"][i].SessionID == conv {
			f.sessions["server"][i].Space = ""
			f.sessions["server"][i].Cwd = "/home/me/.persona/assistant"
		}
	}
	w := strengthActionHTTP(s, conv, key, "save", "tap", 1)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal(w.Code, w.Body)
	}
	f.sessions["server"] = slices.DeleteFunc(f.sessions["server"], func(c proto.Session) bool { return c.SessionID == conv })
	w = strengthActionHTTP(s, conv, key, "save", "offline-tap", 1)
	if w.Code != 200 {
		t.Fatal("offline card did not save", w.Code, w.Body)
	}
}
func TestHealthStrengthCardCannotBeRewrittenByModel(t *testing.T) {
	s, _, d, conv, key := strengthCardHarness(t)
	ctx := context.Background()
	if _, err := s.toolView(ctx, "server", conv, "view_update", json.RawMessage(`{"id":"`+key+`","view":{"fallback":"Changed","sections":[]}}`)); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatal("model changed module card", err)
	}
	if _, err := s.toolView(ctx, "server", conv, "view_get", json.RawMessage(`{"id":"`+key+`"}`)); err != nil {
		t.Fatal(err)
	}
	w := intakeHTTP(s, "POST", "/api/v1/assistant/conversations/"+conv+"/views/"+key+"/html-actions", `{"kind":"reply","component_key":"detail","client_msg_id":"html","text":"Forged","expected_version":1,"confirmed":true}`, "full")
	if w.Code != 400 || !strings.Contains(w.Body.String(), i18n.T(s.lang(ctx), "assistant-health_strength.html_forbidden")) {
		t.Fatal(w.Code, w.Body)
	}
	if row, err := d.AssistantView(ctx, conv, key); err != nil || row.Version != 1 {
		t.Fatal("rejected request mutated card", row, err)
	}
	out, err := s.toolView(ctx, "server", conv, "view_show", json.RawMessage(`{"view":{"fallback":"Plain card","sections":[{"items":[{"type":"text","text":"Plain card"}]}]}}`))
	if err != nil || strings.Contains(out, healthhost.CardPrefix) || !strings.Contains(out, "view_") {
		t.Fatal(out, err)
	}
}
func TestHealthStrengthCardDiscardAndConcurrentSaveHTTP(t *testing.T) {
	s, _, d, conv, key := strengthCardHarness(t)
	w := strengthActionHTTP(s, conv, key, "discard", "discard-tap", 1)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	draft, err := d.HealthStrengthDraftByCard(context.Background(), key)
	if err != nil || draft.Status != "discarded" {
		t.Fatal(draft, err)
	}
	changes, err := d.HealthStrengthSetChanges(context.Background(), 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range changes {
		if v.DraftID != nil && *v.DraftID == draft.ID {
			t.Fatal("discard leaked sync data")
		}
	}
	s, _, d, conv, key = strengthCardHarness(t)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := range 2 {
		wg.Go(func() { w := strengthActionHTTP(s, conv, key, "save", fmt.Sprintf("tap-%d", i), 1); codes <- w.Code })
	}
	wg.Wait()
	close(codes)
	got := []int{}
	for code := range codes {
		got = append(got, code)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{200, 409}) {
		t.Fatal("concurrent replies", got)
	}
}
func TestHealthStrengthCardAbsentModule(t *testing.T) {
	needsHealthModule(t)
	s, _, d, conv, key := strengthCardHarness(t)
	s.healthMod = nil
	w := strengthActionHTTP(s, conv, key, "save", "tap", 1)
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	row, err := d.AssistantView(context.Background(), conv, key)
	if err != nil || row.Version != 1 {
		t.Fatal(row, err)
	}
}

func TestHealthStrengthCapabilityFollowsModule(t *testing.T) {
	for _, off := range []bool{false, true} {
		var disabled []string
		if off {
			disabled = []string{healthhost.ModuleID}
		}
		f := healthHub()
		s := New(Options{Hub: f, Store: func() Store { return &intakeTestStore{} }, ModulesOff: disabled})
		absent := off || !healthhost.InBuild(moduleswitch.Set{})
		caps := withHealth([]string{assistantwire.CapabilityHealthStrength}, s)
		if slices.Contains(caps, assistantwire.CapabilityHealthStrength) == absent {
			t.Fatalf("capability absent=%v caps=%v", absent, caps)
		}
		if absent {
			if _, err := s.healthTool(context.Background(), healthhost.ToolCall{Name: healthhost.StrengthTool}); err == nil || !strings.Contains(err.Error(), healthhost.StrengthTool) {
				t.Fatal("absent strength handler", err)
			}
		}
	}
}

func TestHealthStrengthDeletedExerciseCardReturnsBadRequest(t *testing.T) {
	s, _, d, conv, key := strengthCardHarness(t)
	ctx := context.Background()
	draft, err := d.HealthStrengthDraftByCard(ctx, key)
	if err != nil || draft == nil {
		t.Fatal(draft, err)
	}
	var body struct {
		Sets []struct {
			Exercise string `json:"exercise"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(draft.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, err := d.HealthStrengthExerciseDelete(ctx, body.Sets[0].Exercise); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "zh-Hans", "ja"} {
		input, _ := json.Marshal(wire.ViewActionInput{Action: "save", ClientMsgID: "deleted-save", ExpectedVersion: 1})
		r := httptest.NewRequest("POST", "/api/v1/assistant/conversations/"+conv+"/views/"+key+"/actions", strings.NewReader(string(input)))
		r.Header.Set(RoleHeader, "full")
		r.Header.Set("Accept-Language", lang)
		w := httptest.NewRecorder()
		// The manager's bus recorder carries the request language in production.
		s.ServeHTTP(guardLangWriter{ResponseRecorder: w, lang: i18n.Lang(lang)}, r)
		var got struct {
			Error     string `json:"error"`
			Retryable bool   `json:"retryable"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := i18n.T(i18n.Lang(lang), "assistant-health_strength.invalid_draft")
		if w.Code != 400 || got.Retryable || got.Error != want {
			t.Fatalf("%s: %d %s; want %s", lang, w.Code, w.Body, want)
		}
	}
	if w := strengthActionHTTP(s, conv, key, "discard", "discard-deleted", 1); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}
