package node

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// One transient snapshot per watch. The rollout reader and SQLite stamp are used
// by the single in-flight poll. Nothing is written into paged conversation history.
type toolWatch struct {
	reading atomic.Bool
	reader  codexLiveToolReader
	dbKey   string
	dbState storedToolState
	retry   codexRetryReader
	last    proto.LiveToolState
	sent    bool
}

func (n *Node) pumpTools(w *watch) {
	path := ""
	if w.f != nil {
		path = w.f.Where()
	}
	rollout := strings.HasPrefix(filepath.Base(path), "rollout-") && filepath.Ext(path) == ".jsonl"
	if (!rollout && !w.dbHistory) || !w.tools.reading.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer w.tools.reading.Store(false)
		var state proto.LiveToolState
		var err error
		var position toolActivityPosition
		var home string
		if !rollout && n.sessionScope == nil {
			// History may come from the database while the active segment is still
			// writing live call/output records. Prefer that segment when it exists.
			if src, ok := n.codexSource(w.id); ok && src.path != "" && fileExists(src.path) && !n.codexBlocked(w.id) && !n.ownedAssistantSource(w.id) {
				path, rollout = src.path, true
			}
		}
		if rollout && n.scopedTranscriptAllows(path) {
			w.tools.dbKey = ""
			state, err = w.tools.reader.Read(path)
			if n.sessionScope == nil {
				if src, ok := n.codexSource(w.id); ok {
					home = src.home
					position = toolActivityPosition{src.thread, w.tools.reader.turn, w.tools.reader.lastModelActivity}
				}
			}
		} else if n.sessionScope == nil {
			if src, t, ok := n.codexAppHistoryChat(w.id); ok {
				db := codexHistoryDBPath(src.home)
				if db != "" {
					key := db + "|" + t.ID + "|" + t.RolloutPath + "|" + fileStamp(db) + fileStamp(db+"-wal")
					if key != w.tools.dbKey {
						w.tools.dbState, err = storedLiveTools(w.ctx, db, t)
						if err == nil {
							w.tools.dbKey = key
						}
					}
					state = w.tools.dbState.LiveToolState
					position = w.tools.dbState.position
					home = src.home
				}
			}
		}
		if err != nil || w.ctx.Err() != nil {
			return
		}
		w.tools.retry.apply(w.ctx, home, position, time.Now(), &state)
		// An old read must not clear a replacement watch's snapshot.
		mu := n.pushLock(w.id)
		mu.Lock()
		defer mu.Unlock()
		n.mu.Lock()
		active := n.watches[w.id] == w || (n.watches[w.id] == nil && n.waiting[w.id] == w)
		n.mu.Unlock()
		if !active || w.ctx.Err() != nil || (w.tools.sent && (w.tools.last.Compacting == state.Compacting && w.tools.last.Active == state.Active && w.tools.last.Thinking == state.Thinking && w.tools.last.Summary == state.Summary && w.tools.last.RetryAttempt == state.RetryAttempt && w.tools.last.RetryMax == state.RetryMax && slices.Equal(w.tools.last.Tools, state.Tools))) {
			return
		}
		if state.Tools == nil {
			state.Tools = []proto.LiveTool{}
		}
		if n.peer != nil {
			if err := n.peer.Notify("session.live_tools", struct {
				SessionID string `json:"session_id"`
				proto.LiveToolState
			}{w.id, state}); err != nil {
				w.tools.dbKey = ""
				return
			}
		}
		w.tools.sent = true
		w.tools.last = state
		w.tools.last.Tools = slices.Clone(state.Tools)
	}()
}

// Read only tool identity from the most recent turn in the active SQLite lineage.
// Earlier abandoned turns can remain marked inProgress after the App restarts.
type storedToolState struct {
	proto.LiveToolState
	position toolActivityPosition
}

func storedLiveTools(ctx context.Context, db string, thread codexAppThread) (storedToolState, error) {
	var state storedToolState
	if !validID(thread.ID) {
		return state, nil
	}
	cte, err := codexHistoryTurnsSQL(filepath.Dir(db), thread)
	if err != nil {
		return state, err
	}
	// LEFT JOIN leaves a row even between tools. A currently open turn alone
	// does not identify reasoning; only an explicitly in-progress reasoning item does.
	const sql = `, latest AS (SELECT thread_id,turn_id,status FROM history ORDER BY history_ordinal DESC LIMIT 1)
	SELECT t.thread_id AS thread, t.turn_id AS turn, (SELECT max(a.created_at_ms) FROM thread_items a
	WHERE a.thread_id=t.thread_id AND a.turn_id=t.turn_id AND a.item_type IN ('reasoning','agentMessage')) AS activity_ms,
	json_extract(i.item_json,'$.id') AS id, i.item_type AS kind,
	CASE i.item_type WHEN 'commandExecution' THEN 'Bash' WHEN 'fileChange' THEN 'Edit'
	WHEN 'webSearch' THEN 'WebSearch' WHEN 'imageGeneration' THEN 'image_generation'
	WHEN 'mcpToolCall' THEN 'mcp__' || COALESCE(json_extract(i.item_json,'$.server'),'') || '__' || COALESCE(json_extract(i.item_json,'$.tool'),'')
	ELSE json_extract(i.item_json,'$.tool') END AS name
	FROM latest t LEFT JOIN thread_items i ON i.thread_id=t.thread_id AND i.turn_id=t.turn_id
	AND json_valid(i.item_json) AND json_extract(i.item_json,'$.status')='inProgress'
	AND i.item_type IN ('commandExecution','fileChange','reasoning','contextCompaction','webSearch','imageGeneration','mcpToolCall','dynamicToolCall')
	WHERE t.status='inProgress' ORDER BY i.rollout_ordinal LIMIT 128`
	var rows []struct {
		ID, Kind, Name, Thread, Turn string
		ActivityMS                   int64 `json:"activity_ms"`
	}
	if err := querySQLite(ctx, db, cte+sql, &rows); err != nil {
		return state, err
	}
	state.Active = len(rows) > 0
	if state.Active {
		row := rows[0]
		state.position = toolActivityPosition{row.Thread, row.Turn, time.UnixMilli(row.ActivityMS)}
	}
	for _, r := range rows {
		if r.Kind == "contextCompaction" {
			state.Compacting = true
		} else if r.Kind == "reasoning" {
			state.Thinking = true
		} else if r.ID != "" && r.Name != "" {
			state.Tools = append(state.Tools, proto.LiveTool{ID: r.ID, Name: r.Name})
		}
	}
	return state, nil
}
