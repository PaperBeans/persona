package node

import (
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The handoff summary of a conversation moving between Claude Code and Codex (#5054, engine_switch.go): what the
// other engine is given, in front of the user's message, to go on with the conversation. Built by code from the
// conversation's transcripts, bounded, the same for the same transcripts; no model is asked.

// handoffSpan is one stretch of the conversation a handoff covers: the items of session SessionID (on engine Agent,
// "claude" or "codex") after After and up to Until (zero: unbounded on that side).
type handoffSpan struct {
	SessionID string
	Agent     string
	After     time.Time
	Until     time.Time
}

// engineHandoff is the handoff text for a switch of the conversation from engine from to engine to ("claude" or
// "codex"), covering spans, oldest first. delta: the conversation goes back to a session of to that ran it before
// (resumed), which remembers what came before it left, so spans are only what happened meanwhile.
//
// The note (buildHandoff, engine_handoff_build.go) names the transcript files of the spans with items in them, for
// the details it leaves out. A span whose transcript cannot be read is left out; an error only when none can be.
func (n *Node) engineHandoff(spans []handoffSpan, from, to string, delta bool) (string, error) {
	note := handoffNote{From: from, To: to, Delta: delta}
	var failed error
	read := 0
	for _, sp := range spans {
		items, err := n.handoffItems(sp)
		if err != nil {
			if failed == nil {
				failed = err
			}
			continue
		}
		read++
		if len(items) == 0 {
			continue
		}
		note.Items = append(note.Items, items...)
		if p := n.transcriptPath(sp.SessionID); p != "" { // a session listed twice is named once, where it ran last
			note.Transcripts = slices.DeleteFunc(note.Transcripts, func(t prompts.EngineHandoffTranscript) bool {
				return t.Session == sp.SessionID
			})
			note.Transcripts = append(note.Transcripts,
				prompts.EngineHandoffTranscript{Engine: engineName(sp.Agent), Session: sp.SessionID, Path: p})
		}
	}
	if read == 0 && failed != nil {
		return "", failed
	}
	return buildHandoff(note), nil
}

// A span reads its transcript back from the end a page at a time, at most handoffScanPages pages, and keeps at most
// handoffItemsPerSpan items, the newest of its window.
const (
	handoffItemsPerSpan = 3000
	handoffPageItems    = 1000
	handoffScanPages    = 20
)

// handoffItems are span's items, oldest first: session SessionID's after After and up to Until. An item without a
// time takes the time of the item after it (none: it is the newest).
func (n *Node) handoffItems(sp handoffSpan) ([]proto.Item, error) {
	f, err := n.file(sp.SessionID)
	if err != nil {
		return nil, err
	}
	if err := n.refresh(sp.SessionID, f); err != nil {
		return nil, err
	}
	var picked []proto.Item // newest first
	var newer time.Time
	before := 0
	for range handoffScanPages {
		page, more, _ := f.Page(before, handoffPageItems)
		for i := len(page) - 1; i >= 0; i-- {
			at := page[i].TS
			if at.IsZero() {
				at = newer
			} else {
				newer = at
			}
			if !sp.Until.IsZero() && (at.IsZero() || at.After(sp.Until)) {
				continue
			}
			if !sp.After.IsZero() && !at.IsZero() && !at.After(sp.After) {
				slices.Reverse(picked)
				return picked, nil
			}
			if picked = append(picked, page[i]); len(picked) == handoffItemsPerSpan {
				slices.Reverse(picked)
				return picked, nil
			}
		}
		if !more || len(page) == 0 || page[0].Seq <= 1 {
			break
		}
		before = page[0].Seq
	}
	slices.Reverse(picked)
	return picked, nil
}

// handoffMessage is the user's message (message) behind the handoff text, as a switch types it in front of the other
// engine (prompts.EngineHandoffMessage): from and to are the engines ("claude" / "codex"), model the model the new
// engine runs ("" when not known), resumed whether the conversation's earlier session of to was resumed.
func handoffMessage(text, from, to, model string, resumed bool, message string) string {
	return prompts.RenderEngineHandoffMessage(prompts.EngineHandoffMessage{From: from, To: to, Model: model, Resumed: resumed,
		Text: strings.TrimSpace(text), Message: message})
}
