package recordingwire

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A timeline is taken whole or not at all: malformed or over the limits it is ignored, never failing the upload.
func TestParseTimeline(t *testing.T) {
	roles := func(n int) string {
		var parts []string
		for i := range n {
			parts = append(parts, `{"at":`+strconv.Itoa(i)+`,"attendance":"listener"}`)
		}
		return `{"roles":[` + strings.Join(parts, ",") + `]}`
	}
	spans := func(n int) string {
		var parts []string
		for i := range n {
			parts = append(parts, `{"start":`+strconv.Itoa(2*i)+`,"end":`+strconv.Itoa(2*i+1)+`}`)
		}
		return `{"speaking":[` + strings.Join(parts, ",") + `]}`
	}
	for _, c := range []struct {
		name, raw string
		ok        bool
		want      *Timeline // nil: not compared
	}{
		{"whole", `{"roles":[{"at":10,"attendance":"Participant"},{"at":0,"attendance":"listener"}],` +
			`"speaking":[{"start":5,"end":6,"text":"Later."},{"start":1,"end":2,"text":"We keep the cap."},{"start":1.5,"end":3,"text":"Bo owns it."}]}`, true,
			&Timeline{Roles: []TimelineRole{{At: 0, Attendance: AttendanceListener}, {At: 10, Attendance: AttendanceParticipant}},
				Speaking: []SpokenSpan{{Start: 1, End: 3, Text: "We keep the cap. Bo owns it."}, {Start: 5, End: 6, Text: "Later."}}}},
		{"spans without text, or null", `{"speaking":[{"start":1,"end":2},{"start":4,"end":4,"text":null}]}`, true, &Timeline{Speaking: spk(1, 2, 4, 4)}},
		{"text cut", `{"speaking":[{"start":0,"end":1,"text":"` + strings.Repeat("答", MaxSpokenText) + `"}]}`, true,
			&Timeline{Speaking: []SpokenSpan{{Start: 0, End: 1, Text: strings.Repeat("答", MaxSpokenText/3)}}}},
		{"empty object", `{}`, true, &Timeline{}},
		{"null parts", `{"roles":null,"speaking":null}`, true, &Timeline{}},
		{"same at twice: the later", `{"roles":[{"at":1,"attendance":"listener"},{"at":1,"attendance":"off"}]}`, true,
			&Timeline{Roles: []TimelineRole{{At: 1, Attendance: AttendanceOff}}}},
		{"absent", ``, false, nil},
		{"null", `null`, false, nil},
		{"not an object", `[1,2]`, false, nil},
		{"a string", `"x"`, false, nil},
		{"unknown role", `{"roles":[{"at":0,"attendance":"speaker"}]}`, false, nil},
		{"notes (#8851)", `{"roles":[{"at":0,"attendance":"notes"},{"at":30,"attendance":"Listener"}]}`, true,
			&Timeline{Roles: []TimelineRole{{At: 0, Attendance: AttendanceNotes}, {At: 30, Attendance: AttendanceListener}}}},
		{"role without at", `{"roles":[{"attendance":"listener"}]}`, false, nil},
		{"at a string", `{"roles":[{"at":"0","attendance":"listener"}]}`, false, nil},
		{"negative at", `{"roles":[{"at":-1,"attendance":"listener"}]}`, false, nil},
		{"span backwards", `{"speaking":[{"start":3,"end":2}]}`, false, nil},
		{"span negative", `{"speaking":[{"start":-1,"end":2}]}`, false, nil},
		{"span without end", `{"speaking":[{"start":1}]}`, false, nil},
		{"span start a string", `{"speaking":[{"start":"1","end":2}]}`, false, nil},
		{"text not a string", `{"speaking":[{"start":1,"end":2,"text":7}]}`, false, nil},
		{"a pair, not an object", `{"speaking":[[1,2]]}`, false, nil},
		{"roles not a list", `{"roles":{"at":0}}`, false, nil},
		{"100 roles", roles(100), true, nil},
		{"101 roles", roles(101), false, nil},
		{"1000 spans", spans(1000), true, nil},
		{"1001 spans", spans(1001), false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseTimeline(json.RawMessage(c.raw))
			if ok != c.ok {
				t.Fatalf("ok %v", ok)
			}
			if c.want != nil && !timelineEqual(got, *c.want) {
				t.Fatalf("got %+v, want %+v", got, *c.want)
			}
		})
	}
}

func timelineEqual(a, b Timeline) bool {
	return slices.Equal(a.Roles, b.Roles) && len(a.Speaking) == len(b.Speaking) && (len(a.Speaking) == 0 || slices.Equal(a.Speaking, b.Speaking))
}

// spk is the speaking spans of pairs of numbers (start, end), without text.
func spk(pairs ...float64) []SpokenSpan {
	var out []SpokenSpan
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, SpokenSpan{Start: pairs[i], End: pairs[i+1]})
	}
	return out
}

// The union: one role change per at (the later wins), spans merged when they overlap or touch; the limits apply to the
// union (the first ones kept); RoleAt is the last change at or before a moment, the meeting's role before the first.
func TestTimelineMergeAndRoleAt(t *testing.T) {
	tl := Timeline{Roles: []TimelineRole{{At: 0, Attendance: AttendanceListener}, {At: 60, Attendance: AttendanceOff}}, Speaking: spk(10, 12)}
	if tl.Merge(Timeline{Roles: []TimelineRole{{At: 0, Attendance: AttendanceListener}}, Speaking: spk(10, 12)}) {
		t.Fatal("the same timeline changed it")
	}
	if !tl.Merge(Timeline{Roles: []TimelineRole{{At: 60, Attendance: AttendanceParticipant}, {At: 30, Attendance: AttendanceOff}, {At: 5, Attendance: "bogus"}}, Speaking: spk(12, 15, 20, 21, 11, 11.5)}) {
		t.Fatal("no change")
	}
	want := Timeline{Roles: []TimelineRole{{At: 0, Attendance: AttendanceListener}, {At: 30, Attendance: AttendanceOff}, {At: 60, Attendance: AttendanceParticipant}}, Speaking: spk(10, 15, 20, 21)}
	if !timelineEqual(tl, want) {
		t.Fatalf("merged %+v", tl)
	}
	for _, c := range []struct {
		at   float64
		want string
	}{{-1, AttendanceParticipant}, {0, AttendanceListener}, {29.9, AttendanceListener}, {30, AttendanceOff}, {61, AttendanceParticipant}} {
		if got := tl.RoleAt(c.at, AttendanceParticipant); got != c.want {
			t.Errorf("RoleAt(%v) = %q, want %q", c.at, got, c.want)
		}
	}
	if got := (Timeline{}).RoleAt(5, AttendanceListener); got != AttendanceListener {
		t.Errorf("empty timeline: %q", got)
	}
	// Past the limits: the first ones.
	var big Timeline
	for i := range MaxTimelineRoles + 20 {
		big.Roles = append(big.Roles, TimelineRole{At: float64(i), Attendance: AttendanceListener})
	}
	for i := range MaxTimelineSpans + 20 {
		big.Speaking = append(big.Speaking, SpokenSpan{Start: float64(3 * i), End: float64(3*i + 1)})
	}
	var u Timeline
	u.Merge(big)
	if len(u.Roles) != MaxTimelineRoles || len(u.Speaking) != MaxTimelineSpans || u.Roles[0].At != 0 || u.Speaking[0].Start != 0 {
		t.Fatalf("limits: %d roles, %d spans", len(u.Roles), len(u.Speaking))
	}
}

// minutesWaitFixtures are the cases the hub, the iPhone and the web share (spec/fixtures/minutes-wait, #13461).
const minutesWaitFixtures = "../../../spec/fixtures/minutes-wait/cases.json"

// The view carries final_done and end exactly when the shared cases say, with their values: while the final pass runs
// (finishing, started) and the end is known, final_done at most the end.
func TestMinutesWaitFixture(t *testing.T) {
	raw, err := os.ReadFile(minutesWaitFixtures)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Version int `json:"version"`
		Cases   []struct {
			Name string `json:"name"`
			Hub  *struct {
				State       string  `json:"state"`
				PassStarted bool    `json:"pass_started"`
				FinalDone   float64 `json:"final_done"`
				End         float64 `json:"end"`
			} `json:"hub"`
			Transcription map[string]json.RawMessage `json:"transcription"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.Version != 1 {
		t.Fatalf("version %d", file.Version)
	}
	checked := 0
	for _, c := range file.Cases {
		if c.Hub == nil {
			continue
		}
		checked++
		t.Run(c.Name, func(t *testing.T) {
			tr := &MeetingTranscription{Mode: TranscriptionHub, State: c.Hub.State, FinalDone: c.Hub.FinalDone, End: c.Hub.End}
			if c.Hub.PassStarted {
				tr.FinalFrom = time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
			}
			got, err := json.Marshal(Meeting{Transcription: tr}.TranscriptionView(nil))
			if err != nil {
				t.Fatal(err)
			}
			var view map[string]json.RawMessage
			if err := json.Unmarshal(got, &view); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"mode", "state", "final_done", "end"} {
				want, wok := c.Transcription[key]
				have, hok := view[key]
				if wok != hok || (wok && !sameNumberOrText(t, want, have)) {
					t.Errorf("%s: view %s, want %s", key, got, mustJSON(t, c.Transcription))
				}
			}
		})
	}
	if checked < 8 {
		t.Fatalf("only %d hub cases", checked)
	}
}

// sameNumberOrText: two JSON values are the same number, or the same text.
func sameNumberOrText(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return x == y
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
