package node

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestProviderRetryIsScopedToTheOpenTurnAndSupersededByProgress(t *testing.T) {
	sqlite := sqlite3WithJSON(t)
	home := t.TempDir()
	db := filepath.Join(home, "logs_2.sqlite")
	t0 := time.Now().UTC().Truncate(time.Second)
	run := func(q string) {
		t.Helper()
		if out, err := exec.Command(sqlite, db, q).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	run("CREATE TABLE logs(id INTEGER PRIMARY KEY,ts INTEGER,ts_nanos INTEGER,target TEXT,level TEXT,thread_id TEXT,feedback_log_body TEXT);CREATE INDEX scoped_time ON logs(thread_id,ts DESC,ts_nanos DESC,id DESC);")
	const thread, turn = "12345678-abcd", "abcdef13"
	insert := func(thread, turn, target string, at time.Time, n int) {
		t.Helper()
		run(fmt.Sprintf("INSERT INTO logs(ts,ts_nanos,target,level,thread_id,feedback_log_body) VALUES(%d,%d,'%s','WARN','%s','span{turn_id=%s model=example}: stream disconnected - retrying request retries=%d max_retries=5 sampling_error=private')", at.Unix(), at.Nanosecond(), target, thread, turn, n))
	}
	insert(thread, turn, "codex_core::responses_retry", t0, 1)
	insert("eeeeeeee", turn, "codex_core::responses_retry", t0, 4)
	insert(thread, "badbad00", "codex_core::responses_retry", t0, 4)
	insert(thread, turn, "hub_websocket", t0, 4)
	r := codexRetryReader{}
	pos := toolActivityPosition{thread, turn, t0.Add(-time.Second)}
	check := func(now time.Time, want int, active bool) {
		t.Helper()
		state := proto.LiveToolState{Active: active}
		r.apply(context.Background(), home, pos, now, &state)
		if state.RetryAttempt != want || state.RetryMax != 0 && state.RetryMax != 5 {
			t.Fatalf("got %+v; want attempt %d", state, want)
		}
	}
	check(t0, 1, true)
	insert(thread, turn, "codex_core::responses_retry", t0.Add(30*time.Second), 2)
	check(t0.Add(31*time.Second), 2, true)
	pos.after = t0.Add(32 * time.Second)
	check(t0.Add(32*time.Second), 0, true)
	check(t0.Add(32*time.Second), 0, false)
	pos.after = t0.Add(-time.Second)
	pos.turn = "0000abcd"
	check(t0.Add(32*time.Second), 0, true)
	pos.turn = turn
	check(t0.Add(30*time.Second+codexRetryTTL), 0, true)
}

func TestProviderRetryModelActivityClock(t *testing.T) {
	r := codexLiveToolReader{}
	for _, event := range []string{
		`{"type":"event_msg","timestamp":"2026-11-23T03:30:01Z","payload":{"type":"task_started","turn_id":"abcd1234"}}`,
		`{"type":"response_item","timestamp":"2026-11-23T03:30:02Z","payload":{"type":"function_call","name":"Read","call_id":"c"}}`,
		`{"type":"response_item","timestamp":"2026-11-23T03:30:03Z","payload":{"type":"function_call_output","call_id":"c"}}`,
		`{"type":"event_msg","timestamp":"2026-11-23T03:30:04Z","payload":{"type":"token_count"}}`,
	} {
		r.apply([]byte(event))
	}
	if got := r.lastModelActivity.UTC().Format(time.RFC3339); got != "2026-11-23T03:30:02Z" {
		t.Fatalf("local activity superseded the retry: %s", got)
	}
	r.apply([]byte(`{"type":"event_msg","timestamp":"2026-11-23T03:30:05Z","payload":{"type":"item_completed","item":{"type":"Reasoning","id":"r"}}}`))
	if got := r.lastModelActivity.UTC().Format(time.RFC3339); got != "2026-11-23T03:30:05Z" {
		t.Fatal(got)
	}
}
