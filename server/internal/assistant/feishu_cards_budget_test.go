package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

// countCardComponents recursively counts elements/tags in a Card JSON.
func countCardComponents(t *testing.T, js string) int {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		t.Fatalf("unmarshal card json: %v", err)
	}
	var count int
	var walk func(x any)
	walk = func(x any) {
		switch m := x.(type) {
		case map[string]any:
			if _, hasTag := m["tag"]; hasTag {
				count++
			}
			for _, val := range m {
				walk(val)
			}
		case []any:
			for _, val := range m {
				walk(val)
			}
		}
	}
	walk(v)
	return count
}

func TestFeishuInteractionCardBudget(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	s := newTestService(t, newFakeHub())
	ctx := context.Background()

	const maxBytes = 24000
	const maxComponents = 200

	cases := []struct {
		name string
		ix   proto.Interaction
	}{
		{
			name: "screen dialog 28000 runes",
			ix: proto.Interaction{
				Kind: proto.IxScreen,
				Dialog: &screen.Dialog{
					Detail:   strings.Repeat("详", 14000),
					Question: strings.Repeat("问", 14000),
					Options: []screen.Option{
						{Key: "1", Label: "选项1"},
						{Key: "2", Label: "选项2"},
					},
				},
			},
		},
		{
			name: "screen no dialog 20 lines by 2000 chars",
			ix: proto.Interaction{
				Kind:   proto.IxScreen,
				Screen: strings.Repeat(strings.Repeat("a", 2000)+"\n", 20),
			},
		},
		{
			name: "30 questions by 8 options",
			ix: func() proto.Interaction {
				var qs []proto.Question
				for i := 0; i < 30; i++ {
					var opts []proto.QuestionOption
					for j := 0; j < 8; j++ {
						opts = append(opts, proto.QuestionOption{Label: fmt.Sprintf("选项%d-%d", i, j)})
					}
					qs = append(qs, proto.Question{
						Header:   fmt.Sprintf("第%d问", i),
						Question: fmt.Sprintf("这是关于第%d个模块的配置问题", i),
						Options:  opts,
					})
				}
				return proto.Interaction{Kind: proto.IxQuestion, Questions: qs}
			}(),
		},
		{
			name: "40 questions by 8 options",
			ix: func() proto.Interaction {
				var qs []proto.Question
				for i := 0; i < 40; i++ {
					var opts []proto.QuestionOption
					for j := 0; j < 8; j++ {
						opts = append(opts, proto.QuestionOption{Label: fmt.Sprintf("选项%d-%d", i, j)})
					}
					qs = append(qs, proto.Question{
						Header:   fmt.Sprintf("第%d问", i),
						Question: fmt.Sprintf("这是关于第%d个模块的配置问题", i),
						Options:  opts,
					})
				}
				return proto.Interaction{Kind: proto.IxQuestion, Questions: qs}
			}(),
		},
		{
			name: "single question with two 3500-char descriptions",
			ix: proto.Interaction{
				Kind: proto.IxQuestion,
				Questions: []proto.Question{
					{
						Question: "请选择部署模式",
						Options: []proto.QuestionOption{
							{Label: "模式A", Description: strings.Repeat("长描述A", 700)},
							{Label: "模式B", Description: strings.Repeat("长描述B", 700)},
						},
					},
				},
			},
		},
		{
			name: "permission reason 14000 chars markdown special",
			ix: proto.Interaction{
				Kind:     proto.IxPermission,
				ToolName: "bash",
				Reason:   strings.Repeat(`\*_[]~<>#|`, 1400),
			},
		},
		{
			name: "single question 10000 chars special markdown",
			ix: proto.Interaction{
				Kind: proto.IxQuestion,
				Questions: []proto.Question{
					{
						Question: strings.Repeat(`\*_[]~<>#|`, 1000),
					},
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			card := s.interactionCard(ctx, c.ix)
			js := card.JSON()
			if len(js) > maxBytes {
				t.Fatalf("card JSON size %d exceeds budget %d", len(js), maxBytes)
			}
			comps := countCardComponents(t, js)
			if comps > maxComponents {
				t.Fatalf("card components %d exceeds budget %d", comps, maxComponents)
			}
		})
	}
}

func TestFeishuAnsweredCardBudget(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	s := newTestService(t, newFakeHub())
	ctx := context.Background()

	const maxBytes = 24000
	const maxComponents = 200

	cases := []struct {
		name string
		ix   proto.Interaction
	}{
		{
			name: "answered single question 10000 chars",
			ix: proto.Interaction{
				Kind: proto.IxQuestion,
				Questions: []proto.Question{
					{Question: strings.Repeat("问题", 5000)},
				},
			},
		},
		{
			name: "answered 20 questions by 5000 chars",
			ix: func() proto.Interaction {
				var qs []proto.Question
				for i := 0; i < 20; i++ {
					qs = append(qs, proto.Question{
						Question: fmt.Sprintf("问题 %d: %s", i, strings.Repeat("字", 5000)),
					})
				}
				return proto.Interaction{Kind: proto.IxQuestion, Questions: qs}
			}(),
		},
		{
			name: "answered plan 10000 chars",
			ix: proto.Interaction{
				Kind: proto.IxPlan,
				Plan: strings.Repeat("计划内容\n", 1000),
			},
		},
		{
			name: "answered permission 5000 chars",
			ix: proto.Interaction{
				Kind:     proto.IxPermission,
				ToolName: "bash",
				Summary:  strings.Repeat("summary", 1000),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			card := s.answeredCard(ctx, c.ix, "已处理")
			js := card.JSON()
			if len(js) > maxBytes {
				t.Fatalf("answered card JSON size %d exceeds budget %d", len(js), maxBytes)
			}
			comps := countCardComponents(t, js)
			if comps > maxComponents {
				t.Fatalf("answered card components %d exceeds budget %d", comps, maxComponents)
			}
		})
	}
}

func TestFeishuBotCardDoneFallback(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	f := newFakeHub()
	s := newTestService(t, f)

	// UpdateCard fails, verify fallback botNote is triggered.
	f.answerApp(AppFeishu, FeishuUpdateCard, func(raw json.RawMessage) (any, error) {
		return nil, errors.New("update card failed: size exceeded")
	})
	var textCalled atomic.Bool
	f.answerApp(AppFeishu, FeishuText, func(raw json.RawMessage) (any, error) {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		if replyTo, _ := m["reply_to"].(string); replyTo == "msg-123" {
			if text, _ := m["text"].(string); strings.Contains(text, "✓ 已处理") {
				textCalled.Store(true)
			}
		}
		return map[string]any{"message_id": "note-1"}, nil
	})

	ix := proto.Interaction{
		ID:   "ix-1",
		Kind: proto.IxQuestion,
		Questions: []proto.Question{
			{Question: "测试问题"},
		},
	}
	keepChatCard(s, ix.ID, &botCard{MessageID: "msg-123", Ix: ix})
	s.feishuInteractionDone(ix, "已处理") // the card is closed in the background

	waitUntil(t, textCalled.Load)
}

// A form must carry every question: the submit handler asks for an answer to each, so a clipped form could never be sent.
func TestFeishuQuestionCardTooManyQuestionsGoesToApp(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	s := newTestService(t, newFakeHub())
	for _, n := range []int{maxFormQuestions, maxFormQuestions + 1} {
		var qs []proto.Question
		for i := 0; i < n; i++ {
			qs = append(qs, proto.Question{Question: fmt.Sprintf("q%d", i), Options: []proto.QuestionOption{{Label: "a"}, {Label: "b"}}})
		}
		js := s.interactionCard(context.Background(), proto.Interaction{Kind: proto.IxQuestion, Questions: qs}).JSON()
		if hasForm := strings.Contains(js, "qs_submit"); hasForm != (n <= maxFormQuestions) {
			t.Errorf("%d questions: form = %v", n, hasForm)
		}
		if n > maxFormQuestions && !strings.Contains(js, strconv.Itoa(n)) {
			t.Errorf("%d questions: the card does not say how many", n)
		}
	}
}
