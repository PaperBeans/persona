package kb

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// funcModel answers each call with f's reply (an error, or JSON).
type funcModel struct {
	f     func(n int, system, user string) (string, error)
	calls [][2]string
}

func (m *funcModel) JSONMax(_ context.Context, system, user string, _ int, out any) error {
	m.calls = append(m.calls, [2]string{system, user})
	reply, err := m.f(len(m.calls), system, user)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(reply), out)
}

// meetingUpload is a complete meeting recording of the given lines, 10 seconds each, started at start.
func meetingUpload(rev int64, start time.Time, texts ...string) RecordingUpload {
	u := recUpload(rev, start, true, texts...)
	u.Kind = RecordingMeeting
	return u
}

func putRecording(t *testing.T, e *Engine, id string, u RecordingUpload, now time.Time) {
	t.Helper()
	if st, err := e.PutRecording(id, u, now); err != nil || st.Status != RecordingStored {
		t.Fatalf("put %s: %v %v", id, st, err)
	}
}

func recordingNow(t *testing.T, e *Engine, id string) *Recording {
	t.Helper()
	r, ok := e.Store.Recording(id)
	if !ok {
		t.Fatalf("recording %s is gone", id)
	}
	return r
}

const minutesReply = `{
  "title": "发布评审",
  "where": "三号会议室",
  "attendees": ["Ada", "Bo", "Zed"],
  "points": [
    {"text": "导出超过 4 MB 的条目会被丢掉", "line": 2, "who": "Ada"},
    {"text": "编造的要点", "line": 99},
    {"text": "回归测试覆盖不到老版本", "line": "[4]", "who": "Zed"}
  ],
  "decisions": [{"text": "这一版先保留上限", "line": 3}],
  "actions": [
    {"who": "Bo", "what": "补上回归测试", "by": "周五前", "due_date": "2026-11-13", "line": "L4"},
    {"who": "", "what": "确认上限按条还是按录音算", "by": "", "due_date": "2025-01-01", "line": 5}
  ],
  "open": [{"text": "上限按条还是按录音", "line": 5}]
}`

// Minutes written before they carried start and end (#4713) get them from their recording in a recording's detail;
// every other field stays as stored, and minutes that have them are sent as they are.
func TestMinutesWithSpan(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*3600)
	started := time.Date(2026, 11, 10, 6, 0, 0, 500_000_000, time.UTC)
	ended := started.Add(70 * time.Minute)
	r := &Recording{StartedAt: started, EndedAt: &ended}
	old := []byte(`{"version":2,"title":"Design review","when":"2026-11-10 14:00–15:10","markdown":"# x","future":{"kept":true}}`)
	var got map[string]any
	if err := json.Unmarshal(minutesWithSpan(old, r, zone), &got); err != nil {
		t.Fatal(err)
	}
	if got["start"] != "2026-11-10T14:00:00+08:00" || got["end"] != "2026-11-10T15:10:00+08:00" {
		t.Errorf("span %v – %v", got["start"], got["end"])
	}
	if got["version"] != float64(2) || got["when"] != "2026-11-10 14:00–15:10" || got["markdown"] != "# x" || got["future"] == nil {
		t.Errorf("stored fields changed: %v", got)
	}
	current := []byte(`{"version":3,"when":"x","start":"2026-11-10T14:00:00+08:00","end":"2026-11-10T15:10:00+08:00"}`)
	for name, c := range map[string]struct {
		raw []byte
		r   *Recording
	}{"has its span": {current, r}, "no recording": {old, nil}, "recording without a start": {old, &Recording{}}, "not JSON": {[]byte("{"), r}} {
		if out := minutesWithSpan(c.raw, c.r, zone); string(out) != string(c.raw) {
			t.Errorf("%s: %s", name, out)
		}
	}
}
