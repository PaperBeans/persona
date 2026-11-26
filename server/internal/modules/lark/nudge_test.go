package lark

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The nudge under a card nobody answered (nudge.go, #6084): its line and its times. The sweep itself runs on the
// core's cards and clock, in the assistant's tests (feishu_nudge_test.go). All ids are placeholders.

// The line is one line, in the user's language: a long multi-line plan is cut to its first line.
func TestNudgeLineIsOneShortLine(t *testing.T) {
	s := newTestService(t, newFakeHub())
	ctx := context.Background()
	long := strings.Repeat("x", 300)
	for _, c := range []struct {
		name string
		ix   proto.Interaction
		want []string
	}{
		{"plan", proto.Interaction{Kind: proto.IxPlan, Plan: "1. Change the login\n2. Test it\n3. Ship"}, []string{"1. Change the login"}},
		{"question cut", proto.Interaction{Kind: proto.IxQuestion, Questions: []proto.Question{{Question: long}}}, nil},
		{"nothing to say", proto.Interaction{Kind: proto.IxQuestion}, nil},
		{"permission without a tool", proto.Interaction{Kind: proto.IxPermission}, nil},
	} {
		text := s.chat().nudgeText(ctx, c.ix)
		if strings.Contains(text, "\n") || len([]rune(text)) > 140 || text == "" {
			t.Errorf("%s: %q", c.name, text)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) || strings.Contains(text, "Test it") {
				t.Errorf("%s: %q", c.name, text)
			}
		}
	}
	// All three languages have all three lines.
	for _, l := range []i18n.Lang{"en", "zh-Hans", "ja"} {
		for _, key := range []string{"nudge", "nudge_permission", "nudge_bare"} {
			got := i18n.T(l, "assistant-feishu_cards."+key, i18n.Args{"what": "W", "tool": "T"})
			if got == "" || got == "assistant-feishu_cards."+key {
				t.Errorf("%s has no %s", l, key)
			}
		}
	}
}

func TestNudgeDeadlineAt(t *testing.T) {
	created := time.Date(2027, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		lifetime time.Duration
		want     time.Duration
		ok       bool
	}{
		{name: "half of a fifteen minute card", lifetime: 15 * time.Minute, want: 7*time.Minute + 30*time.Second, ok: true},
		{name: "five minute margin on a short card", lifetime: 8 * time.Minute, want: 3 * time.Minute, ok: true},
		{name: "two minute lower bound", lifetime: 4 * time.Minute, want: 2 * time.Minute, ok: true},
		{name: "shortest useful card", lifetime: 3 * time.Minute, want: 2 * time.Minute, ok: true},
		{name: "no room before expiry", lifetime: 2 * time.Minute},
		{name: "no lifetime", lifetime: 0},
		{name: "half of a long card", lifetime: time.Hour, want: 30 * time.Minute, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := deadlineNudgeAt(created, created.Add(tc.lifetime))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (at %s)", ok, tc.ok, got)
			}
			if ok && !got.Equal(created.Add(tc.want)) {
				t.Errorf("nudge at %s, want %s after creation", got.Sub(created), tc.want)
			}
		})
	}
	if _, ok := deadlineNudgeAt(created, time.Time{}); ok {
		t.Error("a card without a deadline has a deadline nudge")
	}
	if _, ok := deadlineNudgeAt(time.Time{}, created.Add(time.Hour)); ok {
		t.Error("a card without a creation time has a deadline nudge")
	}
}

// nudge_after is the hub's setting, in minutes: a hub that does not say is the default (an hour), 0 is off.
func TestNudgeAfterOfTheStatus(t *testing.T) {
	if n := nudgeAfterOf(FeishuStatus{}); n != time.Hour {
		t.Errorf("no setting = %v, want 1h", n)
	}
	zero := 0
	if n := nudgeAfterOf(FeishuStatus{NudgeAfter: &zero}); n != 0 {
		t.Errorf("0 = %v, want off", n)
	}
}
