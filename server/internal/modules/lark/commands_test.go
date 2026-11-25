package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// A command is still a command when a line break, a tab or a full-width space follows it, or when it starts with a
// full-width slash; what comes after it goes on whole, line breaks inside included (#3897).
func TestBotCommandSplitsOnAnyWhiteSpace(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, text string
		handled    bool
		rest       string
	}{
		{"line break", "/new\n第一句话", false, "第一句话"},
		{"full-width space", "/new\u3000第一句话", false, "第一句话"},
		{"tab", "/new\t第一句话", false, "第一句话"},
		{"full-width slash", "／new 第一句话", false, "第一句话"},
		{"white space around", " \u3000/new 第一句话 \n", false, "第一句话"},
		{"multi-line rest", "/new\n第一行\n\n  第二行\n", false, "第一行\n\n  第二行"},
		{"bare after a line break", "/new\n", true, ""},
		{"stop alias", "/停止", true, ""},
		{"short stop alias", "/停", true, ""},
		{"full-width stop", "／stop", true, ""},
		{"stop then a line break", "/stop\n", true, ""},
		{"unknown command goes on as it is", "/compact", false, "/compact"},
		{"a word glued to the command", "/stopnow", false, "/stopnow"},
		{"a slash mid-text", "看看 /stop 是什么", false, "看看 /stop 是什么"},
		{"a path", "/tmp/a 是什么", false, "/tmp/a 是什么"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newTestService(t, newFakeHub())
			h, rest := s.chat().botCommand(ctx, FeishuFeatures{Main: true}, "om", c.text, "", "")
			if h != c.handled || rest != c.rest {
				t.Fatalf("botCommand(%q) = %v, %q; want %v, %q", c.text, h, rest, c.handled, c.rest)
			}
		})
	}
}

func TestParseBotCommand(t *testing.T) {
	for _, c := range []struct {
		text, cmd, rest string
		ok              bool
	}{
		{"/new 第一句话", "/new", "第一句话", true},
		{"/new\n第一句话", "/new", "第一句话", true},
		{"/new\r\n第一句话", "/new", "第一句话", true},
		{"/new\u3000第一句话", "/new", "第一句话", true},
		{"/new\u3000\u3000第一句话\u3000", "/new", "第一句话", true},
		{"／new 第一句话", "/new", "第一句话", true},
		{"／STOP", "/stop", "", true},
		{"\u3000\n /help", "/help", "", true},
		{"/new 第一行\n第二行\n\n第三行", "/new", "第一行\n第二行\n\n第三行", true},
		{"/agents\tweb\u3000app", "/agents", "web\u3000app", true},
		{"/", "/", "", true},
		{"/tmp/a 是什么", "/tmp/a", "是什么", true},
		{"看看 /stop 是什么", "", "", false},
		{"第一行\n/stop", "", "", false},
		{"停止", "", "", false},
		{"\\stop", "", "", false},
		{"", "", "", false},
		{"\u3000\n", "", "", false},
	} {
		cmd, rest, ok := parseBotCommand(c.text)
		if cmd != c.cmd || rest != c.rest || ok != c.ok {
			t.Errorf("parseBotCommand(%q) = %q, %q, %v; want %q, %q, %v", c.text, cmd, rest, ok, c.cmd, c.rest, c.ok)
		}
	}
}

// "/new <text>" goes on with just the text; a command the bot does not know goes on as it is (it may be the
// conversation's own).
func TestBotCommandsPassTheRestOn(t *testing.T) {
	s := newTestService(t, newFakeHub())
	ctx := context.Background()
	if h, rest := s.chat().botCommand(ctx, FeishuFeatures{Main: true}, "om", "/new 帮我写周报", "", ""); h || rest != "帮我写周报" {
		t.Fatalf("/new with text: handled=%v rest=%q", h, rest)
	}
	if got := s.layer.Route("", ""); got.Kind != targetPersona || got.SessionID != "" {
		t.Fatalf("/new points the chat at a new conversation: %+v", got)
	}
	if h, rest := s.chat().botCommand(ctx, FeishuFeatures{}, "om", "/compact", "", ""); h || rest != "/compact" {
		t.Fatalf("an unknown command goes on as it is: handled=%v rest=%q", h, rest)
	}
}

// The help reads in the user's language and lists only what the bot's switches allow (the assistant's
// TestBotTextInLanguages checks its other lines).
func TestBotHelpInEnglish(t *testing.T) {
	if got := botHelp(i18n.En, FeishuFeatures{Main: true}); !strings.Contains(got, "`/main` back to the main chat") || strings.Contains(got, "/agents") {
		t.Fatal(got)
	}
}

// /help with the workbench on names its commands.
func TestWorkbenchHelpNamesItsCommands(t *testing.T) {
	l := i18n.Lang("en")
	if h := botHelp(l, FeishuFeatures{Main: true, Agents: true, Workbench: true}); !strings.Contains(h, "/home") || !strings.Contains(h, "/inbox") {
		t.Fatalf("help: %s", h)
	}
}

// An agent's row shows its state as a tag, its label and computer, and its last words without their Markdown; an agent
// named by its herdr tab shows its other name under the label. (The assistant's home-card test draws the card.)
func TestAgentRowMarkdown(t *testing.T) {
	sess := proto.Session{State: proto.StateWorking, LastText: "在跑**测试**"}
	row := agentRowMarkdown(i18n.ZhHans, "修登录", "", "box", sess, 70)
	if row != "<text_tag color='blue'>工作中</text_tag> **修登录** · box\n<font color='grey'>在跑测试</font>" {
		t.Fatalf("agent row %q", row)
	}
	// An agent named by its herdr tab shows its other name under the label.
	row = agentRowMarkdown(i18n.ZhHans, "登录", "修登录的测试", "box", sess, 70)
	if row != "<text_tag color='blue'>工作中</text_tag> **登录** · box\n修登录的测试\n<font color='grey'>在跑测试</font>" {
		t.Fatalf("agent row with a secondary name %q", row)
	}
}
