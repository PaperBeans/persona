package writers

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Persona's echo on a meeting's upload (#5905). The bodies are JSON, as the phone sends them: segments may carry
// echo, and spoken says where Persona spoke and what it said.

// A spoken field of any shape never fails the upload; its well-formed spans still mark the echo.
func TestMalformedSpokenNeverFailsTheUpload(t *testing.T) {
	e := goldenEngine(t, nil)
	now := time.Date(2026, 11, 10, 16, 0, 0, 0, goldenZone)
	lines := `"segments":[{"start":10.2,"end":17.8,"text":"You kept the cap."},{"start":20,"end":24,"text":"Bo owns the regression test."}]`
	for i, spoken := range []string{`"yes"`, `7`, `{"start":10,"end":17.5}`, `[1,"x",null,{"start":"a"},{"start":10}]`,
		`[{"start":10,"end":17.5,"text":["You kept the cap."]}]`,
		"[" + strings.TrimSuffix(strings.Repeat(`{"start":10,"end":17.5,"text":"You kept the cap."},`, 1001), ",") + "]"} {
		u := echoUpload(t, now.Add(-time.Hour), lines+`,"spoken":`+spoken)
		u.Revision = int64(i + 1)
		putRecording(t, e, recID, u, now)
		for j, sg := range storedSegments(t, e, recID) {
			if _, marked := sg["echo"]; marked {
				t.Errorf("spoken %.40s: segment %d marked by a malformed span", spoken, j)
			}
		}
	}
	u := echoUpload(t, now.Add(-time.Hour), lines+`,"spoken":[1,{"start":"a","end":2},{"start":10,"end":17.5,"text":"You kept the cap."}]`)
	u.Revision = 10
	putRecording(t, e, recID, u, now)
	if segs := storedSegments(t, e, recID); segs[0]["echo"] != true || segs[1]["echo"] != nil {
		t.Errorf("the well-formed span beside malformed ones: %v", segs)
	}
}

// An all-day recording has no echo: Persona never speaks in one. A segment marked echo is stored unmarked by the
// append (POST …/segments) as by the PUT, so transcript.md shows it as the day's line, as the day's text counts it.
func TestDayRecordingDropsEchoMarks(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	e := dayEngine(t, nil, &now)
	u := dayUpload(1000, "八点")
	if err := json.Unmarshal([]byte(`[{"start":0,"end":5,"text":"八点","echo":true}]`), &u.Segments); err != nil {
		t.Fatal(err)
	}
	mustPut(t, e, dayID, u, now)
	var a RecordingAppend
	if err := json.Unmarshal([]byte(`{"base":1000,"revision":1001,"from":1,`+
		`"segments":[{"start":120,"end":125,"text":"八点零二","echo":true}]}`), &a); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, e, dayID, a)
	segs := storedSegments(t, e, dayID)
	if len(segs) != 2 {
		t.Fatalf("segments.json: %v", segs)
	}
	for i, sg := range segs {
		if _, marked := sg["echo"]; marked {
			t.Errorf("segment %d (%v) of an all-day recording stored as Persona's echo", i, sg["text"])
		}
	}
	md, err := os.ReadFile(e.Store.Path(KindRecording, dayID, "transcript.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(md), "Persona 回声") || !strings.Contains(string(md), "08:02:00 八点零二\n") {
		t.Errorf("transcript.md marks an all-day line as Persona's echo:\n%s", md)
	}
	if r := recordingNow(t, e, dayID); r.Chars != len([]rune("八点"))+len([]rune("八点零二")) {
		t.Errorf("chars %d", r.Chars)
	}
}
