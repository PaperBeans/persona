package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/proto"
)

// endingQueue has the hub answer interactions.get for ix: pending (with ix) until ended is set, then ok false with
// status.
func endingQueue(f *fakeHub, ix proto.Interaction, ended *atomic.Bool, status string) {
	f.answerApp(AppInteractions, assistantwire.InteractionsGet, func(json.RawMessage) (any, error) {
		if ended.Load() {
			return map[string]any{"ok": false, "status": status}, nil
		}
		return map[string]any{"interaction": ix, "ok": true}, nil
	})
}

// ixPress is a press of option v on the first question of card id.
func ixPress(id, v string) *feishubot.CardAction {
	var a feishubot.CardAction
	a.Action.Value = map[string]any{"a": "ix", "id": id, "q": "0", "v": v}
	a.Context.MessageID = "om_" + id
	return &a
}

// A typed answer or a press whose resolve fails on the way puts the card back only while it still waits: one that
// ended while the answer was out (its interaction.ended found no card) stays closed, not a ghost card (#5012).
func TestFailedAnswerRestoresOnlyAPendingCard(t *testing.T) {
	defer func(w time.Duration) { cardAnswerWait = w }(cardAnswerWait)
	for _, c := range []struct {
		name     string
		press    bool // the button path; else a typed answer
		late     bool // the press's answer comes back after its toast
		endsMid  bool // the interaction ends while the answer is out
		restored bool
	}{
		{"typed, still pending", false, false, false, true},
		{"typed, ended meanwhile", false, false, true, false},
		{"press, still pending", true, false, false, true},
		{"press, ended meanwhile", true, false, true, false},
		{"late press, still pending", true, true, false, true},
		{"late press, ended meanwhile", true, true, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cardAnswerWait = time.Second
			if c.late {
				cardAnswerWait = 10 * time.Millisecond
			}
			ix := qCard("ix_q", false, "北京", "上海")
			var ended atomic.Bool
			s, f, _, notes, _ := textAnswerBot(t, func(json.RawMessage) (any, error) {
				if c.late {
					time.Sleep(100 * time.Millisecond)
				}
				if c.endsMid {
					ended.Store(true) // its interaction.ended came while the answer was out
				}
				return nil, errors.New("hub restarted")
			}, ix)
			endingQueue(f, ix, &ended, "resolved")
			seedCard(s, ix)
			ctx := context.Background()
			if c.press {
				r := s.botCardAction(ctx, ixPress(ix.ID, "上海"), "oa_1")
				if r == nil || r.Toast == nil {
					t.Fatalf("no toast: %+v", r)
				}
			} else if !s.botAnswerQuestion(ctx, "om_r", "2", "oa_1", "", "") || !strings.Contains(noteText(notes), "没交上去") {
				t.Fatalf("typed answer not taken, notes %q", noteText(notes))
			}
			waits := func() bool {
				s.fbot.mu.Lock()
				defer s.fbot.mu.Unlock()
				_, ok := s.fbot.cards[ix.ID]
				return ok
			}
			if c.late {
				deadline := time.Now().Add(2 * time.Second)
				for c.restored && !waits() && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				time.Sleep(200 * time.Millisecond) // the late answer has landed either way
			}
			if got := waits(); got != c.restored {
				t.Fatalf("card waits %v, want %v", got, c.restored)
			}
		})
	}
}

// A press whose answer comes back after cardAnswerWait (its toast already said "Working on it…") still says what became
// of it: one that failed posts a note under the card, which waits again for another press; one that lost to another
// surface closes the card as handled instead of leaving its buttons live (#4123).
func TestLatePressOutcomeIsSaidInTheChat(t *testing.T) {
	defer func(w time.Duration) { cardAnswerWait = w }(cardAnswerWait)
	cardAnswerWait = 10 * time.Millisecond
	waits := func(s *Service, id string) bool {
		s.fbot.mu.Lock()
		defer s.fbot.mu.Unlock()
		_, ok := s.fbot.cards[id]
		return ok
	}
	// record replaces the fake hub's handler of app/method with one that keeps what it was called with: the answer is
	// posted from a goroutine after the callback returned, so the test reads it under mu, never as it is written.
	record := func(f *fakeHub, app, method string) (read func() []map[string]any) {
		var mu sync.Mutex
		var calls []map[string]any
		f.answerApp(app, method, func(p json.RawMessage) (any, error) {
			var m map[string]any
			_ = json.Unmarshal(p, &m)
			mu.Lock()
			calls = append(calls, m)
			mu.Unlock()
			return map[string]any{}, nil
		})
		return func() []map[string]any {
			deadline := time.Now().Add(2 * time.Second)
			for {
				mu.Lock()
				got := append([]map[string]any(nil), calls...)
				mu.Unlock()
				if len(got) > 0 || time.Now().After(deadline) {
					return got
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}

	t.Run("fails", func(t *testing.T) {
		ix := qCard("ix_q", false, "北京", "上海")
		s, f, _, _, _ := textAnswerBot(t, func(json.RawMessage) (any, error) {
			time.Sleep(100 * time.Millisecond)
			return nil, errors.New("hub restarted")
		}, ix)
		endingQueue(f, ix, new(atomic.Bool), "resolved")
		notes := record(f, AppFeishu, FeishuText)
		seedCard(s, ix)
		r := s.botCardAction(context.Background(), ixPress(ix.ID, "上海"), "oa_1")
		if r == nil || r.Toast == nil || r.Toast.Content != "正在处理…" {
			t.Fatalf("toast %+v", r)
		}
		var text string
		for _, n := range notes() {
			text += n["text"].(string)
		}
		if !strings.Contains(text, "没交上去") || !strings.Contains(text, "hub restarted") {
			t.Fatalf("note %q: the failed press was not said", text)
		}
		if !waits(s, ix.ID) {
			t.Fatal("the card must wait again for another press")
		}
	})

	t.Run("already handled", func(t *testing.T) {
		ix := qCard("ix_q", false, "北京", "上海")
		var ended atomic.Bool
		s, f, _, _, _ := textAnswerBot(t, func(json.RawMessage) (any, error) {
			time.Sleep(100 * time.Millisecond)
			ended.Store(true) // another surface answered it while this press was out
			return map[string]any{"ok": false}, nil
		}, ix)
		endingQueue(f, ix, &ended, "resolved")
		closed := record(f, AppFeishu, FeishuUpdateCard)
		seedCard(s, ix)
		r := s.botCardAction(context.Background(), ixPress(ix.ID, "上海"), "oa_1")
		if r == nil || r.Toast == nil || r.Toast.Content != "正在处理…" {
			t.Fatalf("toast %+v", r)
		}
		got := closed()
		if len(got) != 1 {
			t.Fatalf("the card was not closed: %v", got)
		}
		if b, _ := json.Marshal(got[0]); !strings.Contains(string(b), "已处理") {
			t.Fatalf("the card was not closed as handled: %s", b)
		}
		if waits(s, ix.ID) {
			t.Fatal("a card answered elsewhere must not wait again")
		}
	})
}

// A press or typed answer on a card the hub no longer has pending says why: it expired, it was withdrawn, or (answered,
// dismissed, unknown) it was handled elsewhere (#5012).
func TestEndedCardSaysWhy(t *testing.T) {
	for _, c := range []struct {
		status, toast, note, closed string
	}{
		{"expired", "这张卡片已经过期了", "这张卡片已经过期了", "这张卡片已经过期了"},
		{"cancelled", "这张卡片已经撤回了", "这张卡片已经撤回了", "这张卡片已经撤回了"},
		{"resolved", "已经处理过了", "已经在别处处理过了", "已处理"},
		{"", "已经处理过了", "已经在别处处理过了", "已处理"},
	} {
		t.Run("status "+c.status, func(t *testing.T) {
			ix := qCard("ix_q", false, "北京", "上海")
			ended := new(atomic.Bool)
			ended.Store(true)

			s, f, _, _, rs := textAnswerBot(t, nil, ix)
			endingQueue(f, ix, ended, c.status)
			seedCard(s, ix)
			r := s.botCardAction(context.Background(), ixPress(ix.ID, "上海"), "oa_1")
			if r == nil || r.Toast == nil || r.Toast.Content != c.toast || len(*rs) != 0 {
				t.Fatalf("press toast %+v resolves %v, want %q", r, *rs, c.toast)
			}

			s, f, posts, notes, rs := textAnswerBot(t, nil, ix)
			endingQueue(f, ix, ended, c.status)
			seedCard(s, ix)
			if !s.botAnswerQuestion(context.Background(), "om_r", "2", "oa_1", "", "") || len(*rs) != 0 {
				t.Fatalf("typed answer not swallowed: resolves %v", *rs)
			}
			if got := noteText(notes); !strings.Contains(got, c.note) {
				t.Fatalf("note %q, want %q", got, c.note)
			}
			closed, _ := json.Marshal(*posts)
			if len(*posts) != 1 || !strings.Contains(string(closed), c.closed) {
				t.Fatalf("closed card %v, want %q", *posts, c.closed)
			}
		})
	}
}
