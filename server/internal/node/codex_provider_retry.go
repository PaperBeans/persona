package node

import (
	"context"
	"fmt"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Captured in the Codex runtime diagnostics, not in the transcript or the Hub
// connection state. Read only the counters from the exact provider target.
const codexRetryTTL = 2 * time.Minute

type toolActivityPosition struct {
	thread, turn string
	after        time.Time
}

type codexRetrySample struct {
	Seconds, Nanos   int64
	Attempt, Maximum int
}

type codexRetryReader struct {
	home, thread, turn string
	stamp              string
	next               time.Time
	sample             codexRetrySample
}

func (r *codexRetryReader) apply(ctx context.Context, home string, pos toolActivityPosition, now time.Time, state *proto.LiveToolState) {
	if !state.Active || home == "" || !validID(pos.thread) || !validID(pos.turn) {
		*r = codexRetryReader{}
		return
	}
	if r.home != home || r.thread != pos.thread || r.turn != pos.turn {
		*r = codexRetryReader{home: home, thread: pos.thread, turn: pos.turn}
	}
	if !now.Before(r.next) {
		r.next = now.Add(3 * time.Second)
		db := codexNumberedDB(home, "logs_")
		if db == "" {
			r.stamp, r.sample = "", codexRetrySample{}
			return
		}
		stamp := db + fileStamp(db) + fileStamp(db+"-wal")
		if stamp != r.stamp {
			sample, err := readCodexRetry(ctx, db, pos, now)
			if err == nil {
				r.stamp, r.sample = stamp, sample
			}
		}
	}
	at := time.Unix(r.sample.Seconds, r.sample.Nanos)
	if r.sample.Attempt > 0 && r.sample.Attempt <= r.sample.Maximum && !at.After(now) && now.Sub(at) < codexRetryTTL && at.After(pos.after) {
		state.RetryAttempt, state.RetryMax = r.sample.Attempt, r.sample.Maximum
	}
}

func readCodexRetry(ctx context.Context, db string, pos toolActivityPosition, now time.Time) (codexRetrySample, error) {
	var rows []codexRetrySample
	// The thread/time index bounds this read to recent diagnostics for the one
	// watch. Neither log bodies, span fields, nor upstream error details leave SQLite.
	const query = `SELECT ts AS seconds, ts_nanos AS nanos,
	CAST(substr(feedback_log_body,instr(feedback_log_body,' retries=')+9) AS INTEGER) AS attempt,
	CAST(substr(feedback_log_body,instr(feedback_log_body,' max_retries=')+13) AS INTEGER) AS maximum
	FROM logs WHERE thread_id='%s' AND ts>=%d AND (ts,ts_nanos)<=(%d,%d) AND target='codex_core::responses_retry' AND level='WARN'
	AND instr(feedback_log_body,'stream disconnected - retrying')>0
	AND (instr(feedback_log_body,'turn_id=%s ')>0 OR instr(feedback_log_body,'turn_id=%s}')>0)
	AND instr(feedback_log_body,' retries=')>0 AND instr(feedback_log_body,' max_retries=')>0
	ORDER BY ts DESC,ts_nanos DESC,id DESC LIMIT 1`
	if !validID(pos.thread) || !validID(pos.turn) {
		return codexRetrySample{}, nil
	}
	if err := querySQLite(ctx, db, fmt.Sprintf(query, pos.thread, now.Add(-codexRetryTTL).Unix(), now.Unix(), now.Nanosecond(), pos.turn, pos.turn), &rows); err != nil || len(rows) == 0 {
		return codexRetrySample{}, err
	}
	return rows[0], nil
}

func (m codexLiveRecord) modelProgress() bool {
	p := m.Payload
	switch m.Type {
	case "response_item":
		return p.Type == "function_call" || p.Type == "custom_tool_call"
	case "event_msg":
		switch p.Type {
		case "agent_reasoning_delta", "agent_reasoning_raw_content_delta", "agent_message_delta":
			return true
		case "item_started", "item_completed":
			return p.Item.Type == "Reasoning" || p.Item.Type == "AgentMessage"
		}
	case "history_mutation":
		for _, c := range p.Completions {
			if c.Item.Type == "Reasoning" || c.Item.Type == "AgentMessage" {
				return true
			}
		}
	}
	return false
}
