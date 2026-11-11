package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// minutes_days (#3780, #3862): forever (0) until changed, 0–3650 else 400, changed by full credentials only.
// GET returns 0 for forever or the finite days in effect, never fewer than recording_days; the knowledge base reads
// the saved setting and applies the same finite floor itself.
func TestMinutesDaysSetting(t *testing.T) {
	m := newMeetingFixture(t)
	if got := m.do("GET", "/api/v1/assistant", "", nil, http.StatusOK); got["minutes_days"] != float64(0) || got["recording_days"] != float64(14) {
		t.Fatalf("defaults: minutes %v, recordings %v", got["minutes_days"], got["recording_days"])
	}
	if d := m.engine().MinutesDays(); d != kb.DefaultMinutesDays {
		t.Fatalf("engine default %d", d)
	}
	for _, bad := range []string{`-1`, `3651`, `"30"`, `"forever"`, `true`, `null`, `1.5`} {
		m.do("PUT", "/api/v1/assistant", "", `{"minutes_days":`+bad+`}`, http.StatusBadRequest)
	}
	if out := m.do("PUT", "/api/v1/assistant", "", `{"minutes_days":4000}`, http.StatusBadRequest); !strings.Contains(fmt.Sprint(out["error"]), "3650") {
		t.Fatalf("400 message: %v", out)
	}
	m.do("PUT", "/api/v1/assistant", "answer", `{"minutes_days":30}`, http.StatusForbidden)
	if got := m.do("PUT", "/api/v1/assistant", "", `{"minutes_days":30}`, http.StatusOK); got["minutes_days"] != float64(30) {
		t.Fatalf("PUT 30: %v", got["minutes_days"])
	}
	// recording_days above it is what is in effect; the saved 30 counts again once recording_days is shorter.
	if got := m.do("PUT", "/api/v1/assistant", "", `{"recording_days":60}`, http.StatusOK); got["minutes_days"] != float64(60) || got["recording_days"] != float64(60) {
		t.Fatalf("below recording_days: minutes %v, recordings %v", got["minutes_days"], got["recording_days"])
	}
	if d, e := m.s.minutesDaysNow(), m.engine().MinutesDays(); d != 30 || e != 60 {
		t.Fatalf("saved %d, engine %d; want 30 and 60", d, e)
	}
	if got := m.do("PUT", "/api/v1/assistant", "", `{"recording_days":14}`, http.StatusOK); got["minutes_days"] != float64(30) {
		t.Fatalf("back to 14: %v", got["minutes_days"])
	}
	if got := m.do("GET", "/api/v1/assistant", "", nil, http.StatusOK); got["minutes_days"] != float64(30) || m.engine().MinutesDays() != 30 {
		t.Fatalf("GET after: %v, engine %d", got["minutes_days"], m.engine().MinutesDays())
	}
	if got := m.do("PUT", "/api/v1/assistant", "", `{"minutes_days":0,"recording_days":365}`, http.StatusOK); got["minutes_days"] != float64(0) || m.engine().MinutesDays() != 0 {
		t.Fatalf("forever with longer transcripts: %v", got)
	}
	if got := m.do("GET", "/api/v1/assistant", "", nil, http.StatusOK); got["minutes_days"] != float64(0) {
		t.Fatalf("forever readback: %v", got)
	}
}

// persona_settings reads and changes assistant.minutes_days: the days in effect, an approval card from before to after;
// a value below recording_days changes nothing in effect but is saved as asked for (its card says so), and only the
// value saved already asks nothing.
func TestMinutesDaysSettingsTool(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFakeHub().withMachine())
	const key = "assistant.minutes_days"
	tool := func(args string) (string, bool) {
		r := s.Tool(ctx, ToolCall{Name: "persona_settings", Arguments: json.RawMessage(args), MachineID: "server", SessionID: "c1"})
		return r.Content[0].Text, r.IsError
	}
	get := func() string { out, _ := tool(`{"action":"get","key":"` + key + `"}`); return out }
	set := func(value string) (string, bool) {
		return tool(`{"action":"set","key":"` + key + `","value":` + value + `}`)
	}
	label := func(days int) string { return minutesDaysLabel(s.lang(ctx), days) }
	if out := get(); out != label(0) {
		t.Fatalf("default: %s", out)
	}
	if out, isErr := set(`0`); isErr || !strings.Contains(out, label(0)) || len(s.pendingQuestions()) != 0 {
		t.Fatalf("set to what it is: %s", out)
	}
	for _, bad := range []string{`-1`, `3651`, `"30"`, `"never"`, `null`, `1.5`, `true`} {
		if out, isErr := set(bad); !isErr || !strings.Contains(out, "3650") {
			t.Fatalf("%s: %s", bad, out)
		}
	}
	approve := func(card string) {
		go func() {
			q, ok := awaitCard(s)
			if !ok || q.Summary != card {
				t.Errorf("card %+v, want %q", q, card)
				return
			}
			allow := true
			raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
			if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: q.ID, Answer: raw}); err != nil {
				t.Error(err)
			}
		}()
	}
	approve(i18n.T(i18n.Default, "assistant-settings_tool.change_minutes_days", i18n.Args{"before": label(0), "after": label(30)}))
	if out, _ := set(`30`); !strings.Contains(out, label(30)) || get() != label(30) {
		t.Fatalf("approved: %s", out)
	}
	setSettings(t, s, func(v *Settings) { d := 60; v.RecordingDays = &d })
	if out := get(); out != label(60) {
		t.Fatalf("below recording_days: %s", out)
	}
	// 45 is below recording_days too: nothing changes now, yet it is what counts once recording_days is shorter.
	approve(i18n.T(i18n.Default, "assistant-settings_tool.change_minutes_days_saved", i18n.Args{"previous": label(30), "saved": label(45), "after": label(60)}))
	if out, isErr := set(`45`); isErr || !strings.Contains(out, label(45)) || get() != label(60) || s.minutesDaysNow() != 45 {
		t.Fatalf("a value below recording_days: %s (saved %d)", out, s.minutesDaysNow())
	}
	if out, isErr := set(`45`); isErr || !strings.Contains(out, label(45)) || len(s.pendingQuestions()) != 0 {
		t.Fatalf("set to what is saved: %s", out)
	}
	setSettings(t, s, func(v *Settings) { d := 14; v.RecordingDays = &d })
	if out := get(); out != label(45) {
		t.Fatalf("recording_days shorter: %s, want the 45 asked for", out)
	}
	approve(i18n.T(i18n.Default, "assistant-settings_tool.change_minutes_days", i18n.Args{"before": label(45), "after": label(0)}))
	if out, isErr := set(`"forever"`); isErr || !strings.Contains(out, label(0)) || get() != label(0) || s.minutesDaysNow() != 0 {
		t.Fatalf("forever: %s", out)
	}
	if out, isErr := set(`0`); isErr || len(s.pendingQuestions()) != 0 || !strings.Contains(out, label(0)) {
		t.Fatalf("zero is already forever: %s", out)
	}
}

// A recording past recording_days keeps its minutes: the phone's routes and the assistant's tools see a trimmed
// recording, which takes no upload and makes no minutes again, while its action items still become to-dos.
func TestTrimmedRecordingRoutes(t *testing.T) {
	m := newMeetingFixture(t)
	e := m.engine()
	model := minutesModel{reply: `{"title":"Design review","decisions":[{"text":"Keep the cap","line":1}],"actions":[{"what":"Write the plan","line":2}]}`}
	e.Model, e.ModelFor = model, func(string) kb.Model { return model }
	const id = "55555555-5555-4555-8555-555555555555"
	segments := []map[string]any{{"start": 0, "end": 9, "text": "orchardword keep the cap"}, {"start": 10, "end": 19, "text": "write the plan"}}
	m.upload(id, 1, map[string]any{"complete": true, "segments": segments})
	if n := e.WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	if n := e.PruneRecordings(time.Now().AddDate(0, 0, 20)); n != 0 {
		t.Fatalf("pruned %d, want it trimmed", n)
	}
	path := "/api/v1/recordings/" + id
	wantCode(t, m.do("POST", path+"/minutes", "", nil, http.StatusConflict), codeTranscriptExpired)
	detail := m.do("GET", "/api/v1/kb/recordings/"+id, "", nil, http.StatusOK)["detail"].(map[string]any)
	meta := detail["meta"].(map[string]any)
	if meta["trimmed_at"] == nil || meta["minutes_state"] != "ready" || detail["segments"] != nil || detail["minutes"] == nil {
		t.Fatalf("detail %v", detail)
	}
	list := m.do("GET", "/api/v1/recordings", "", nil, http.StatusOK)
	if recs := list["recordings"].([]any); len(recs) != 1 || recs[0].(map[string]any)["trimmed_at"] == nil || list["days"] != float64(14) {
		t.Fatalf("reconciliation %v", list)
	}
	if out := m.upload(id, 2, map[string]any{"complete": true, "segments": segments}); out["status"] != kb.RecordingExpired {
		t.Fatalf("an upload of a trimmed recording: %v", out)
	}
	if out := m.do("POST", path+"/minutes/todos", "", map[string]any{"version": 1, "actions": []string{"a1"}}, http.StatusOK); len(out["todos"].([]any)) != 1 {
		t.Fatalf("confirming: %v", out)
	}
	got, err := m.s.toolKB(context.Background(), "kb_get", json.RawMessage(`{"kind":"recordings","id":"`+id+`"}`))
	if err != nil || !strings.Contains(got, "minutes.md:\n# Design review") || !strings.Contains(got, "transcript.md: expired") ||
		strings.Contains(got, "orchardword") {
		t.Fatalf("kb_get: %v\n%s", err, got)
	}
	if list, _ := m.s.toolKB(context.Background(), "kb_list", json.RawMessage(`{"kind":"recordings"}`)); !strings.Contains(list, " minutes transcript-expired") {
		t.Fatalf("kb_list: %s", list)
	}
}
