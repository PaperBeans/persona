package lark

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A bare bot command slipped (/stpo) to a Persona conversation is not sent on: the owner is asked what was meant, and
// told how to send it as it is. Words after a slash word make it a message; real words, an agent's own commands and
// anything to an agent go on; a doubled slash sends the text itself.
func TestBotCommandTypoIsAsked(t *testing.T) {
	f, _ := feishuHub("ou_a", time.Unix(100, 0))
	var notes []string
	f.answerApp(AppFeishu, FeishuText, func(p json.RawMessage) (any, error) {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		notes = append(notes, m["text"].(string))
		return map[string]any{}, nil
	})
	s := newTestService(t, f)
	ctx := context.Background()
	for _, typo := range []string{"/stpo", "/stp", "/sotp", "/satus", "/nwe", "/hepl", " /stpo", "/taks", "/tsaks"} {
		notes = nil
		if h, _ := s.chat().botCommand(ctx, FeishuFeatures{Main: true}, "om", typo, "", ""); !h {
			t.Fatalf("%q went on to the conversation", typo)
		}
		got := strings.TrimSpace(typo)
		if len(notes) != 1 || !strings.Contains(notes[0], "是想发") || !strings.Contains(notes[0], "/"+got) {
			t.Fatalf("%q: notes %v", typo, notes)
		}
	}
	for _, msg := range []string{"/news 今天有什么新闻", "/now 几点了", "/last 三天", "/stpo 这个词什么意思"} {
		if h, rest := s.chat().botCommand(ctx, FeishuFeatures{Main: true}, "om", msg, "", ""); h || rest != msg {
			t.Fatalf("%q: handled=%v rest=%q", msg, h, rest)
		}
	}
	for _, own := range []string{"/compact", "/stats", "/model", "/review", "/news", "/now", "/net", "/few", "/man", "/step", "/shop",
		"/star", "/stat", "/lint", "/todos"} {
		if h, rest := s.chat().botCommand(ctx, FeishuFeatures{Main: true}, "om", own, "", ""); h || rest != own {
			t.Fatalf("%s: handled=%v rest=%q", own, h, rest)
		}
	}
	for in, want := range map[string]string{"//stpo": "/stpo", " //stop": "/stop", "//new 一段话": "/new 一段话", "//tasks": "/tasks", "//taks": "/taks"} {
		if h, rest := s.chat().botCommand(ctx, FeishuFeatures{Main: true}, "om", in, "", ""); h || rest != want {
			t.Fatalf("%q: handled=%v rest=%q", in, h, rest)
		}
	}
	// any other text starting with // goes as written
	for _, plain := range []string{"// TODO fix this", "//example.com/x", "//lint"} {
		if h, rest := s.chat().botCommand(ctx, FeishuFeatures{Main: true}, "om", plain, "", ""); h || rest != plain {
			t.Fatalf("%q: handled=%v rest=%q", plain, h, rest)
		}
	}
	s.layer.RememberThread("om_agent", botTarget{Kind: targetAgent, MachineID: "server", SessionID: "a1"}, "Claude")
	for _, in := range []string{"/stpo", "//stpo"} {
		if h, rest := s.chat().botCommand(ctx, FeishuFeatures{Main: true, Agents: true}, "om", in, "om_agent", "om_agent"); h || rest != in {
			t.Fatalf("%q to an agent: handled=%v rest=%q", in, h, rest)
		}
	}
}

// /tasks is one of the bot's commands, so a slip of it is caught; /todos is Claude Code's own and goes on.
func TestBotCommandTypoKnowsTasks(t *testing.T) {
	for _, typo := range []string{"/taks", "/tsaks"} {
		if got := botCommandTypo(typo); got != "/tasks" {
			t.Errorf("%s: %q, want /tasks", typo, got)
		}
	}
	if got := botCommandTypo("/todos"); got != "" {
		t.Errorf("/todos: %q, want none", got)
	}
}

func TestSlipOf(t *testing.T) {
	for _, c := range []struct {
		got, want string
		slip      bool
	}{{"/stpo", "/stop", true}, {"/stp", "/stop", true}, {"/tsop", "/stop", true}, {"/sotp", "/stop", true},
		{"/stopp", "/stop", false}, {"/stap", "/stop", false}, {"/news", "/new", false}, {"/now", "/new", false},
		{"/stop", "/stop", false}, {"/compact", "/stop", false}, {"/tspo", "/stop", false}} {
		if got := slipOf(c.got, c.want); got != c.slip {
			t.Errorf("%s %s: %v", c.got, c.want, got)
		}
	}
}
