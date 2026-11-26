package hub

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A node's coded refusal of a send is explained in the request's language and names the machine (docs/assistant/onboarding.md
// 4.6 rule 2).
func TestSendRefusalSaysWhyInTheRequestLanguage(t *testing.T) {
	h := newTestHub(t)
	pipeNode(t, h, "box", func(context.Context) error {
		return &rpc.Error{Code: proto.CodeClaudeMissing, Message: "zsh:1: command not found: claude",
			Data: json.RawMessage(`{"delivered":"none","launcher":"claude"}`)}
	})
	h.byID["box"].setName("Work Laptop")
	ctx := i18n.With(context.Background(), i18n.En)
	status, body, _ := h.deliverOnce(ctx, h.byID["box"], map[string]any{"session": "s1", "text": "hi"})
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if status != 409 || out["code"] != proto.CodeClaudeMissing || out["machine_name"] != "Work Laptop" ||
		out["error"] != "Claude Code isn't installed on Work Laptop yet." || out["action"] != "install_claude" {
		t.Fatalf("refusal: %d %v", status, out)
	}
}

func TestSendNotDeliveredRefusalLocalized(t *testing.T) {
	t.Run("generic terminal refusal localizes by reason", func(t *testing.T) {
		for _, tc := range []struct {
			reason string
			lang   i18n.Lang
			want   string
		}{
			{"terminal_refused", i18n.En, "The terminal on the computer didn’t accept this message, and nothing was typed. Try again shortly."},
			{"terminal_refused", i18n.ZhHans, "电脑上的终端这次没有收下这条消息，什么都没输入：稍后再发。"},
			{"terminal_refused", i18n.Ja, "コンピューターのターミナルがこのメッセージを受け取れず、何も入力されませんでした。少し待ってからもう一度お試しください。"},
			{"terminal_finished", i18n.En, "This agent’s terminal has already ended, so nothing was typed. Reopen or resume it on the computer before sending again."},
			{"terminal_finished", i18n.ZhHans, "电脑上这个 Agent 的终端已经结束，这次什么都没输入：请在电脑上重新打开或恢复后再发。"},
			{"terminal_finished", i18n.Ja, "コンピューターのこのエージェントのターミナルはすでに終了しているため、何も入力されませんでした。コンピューターで開き直すか再開してから送信してください。"},
		} {
			t.Run(tc.reason+"/"+string(tc.lang), func(t *testing.T) {
				h := newTestHub(t)
				pipeNode(t, h, "box", func(context.Context) error {
					return &rpc.Error{
						Code:    "not_delivered",
						Message: "电脑上的终端这次没有收下这条消息，什么都没输入：稍后再发。",
						Data:    json.RawMessage(`{"delivered":"none","reason":"` + tc.reason + `"}`),
					}
				})
				ctx := i18n.With(context.Background(), tc.lang)
				status, body, _ := h.deliverOnce(ctx, h.byID["box"], map[string]any{"session": "s1", "text": "hi"})
				var out map[string]any
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("body %s: %v", body, err)
				}
				if status != 409 || out["code"] != "not_delivered" || out["delivered"] != "none" || out["error"] != tc.want {
					t.Fatalf("refusal(%s/%s) = %d %v, want 409 error=%q", tc.reason, tc.lang, status, out, tc.want)
				}
			})
		}
	})

	t.Run("specific not_delivered recovery guidance is preserved", func(t *testing.T) {
		const specific = "终端上的这个对话框太长，读不全（这台电脑的终端一次只能读回 8 KB），这次什么都没按：在电脑上回答。"
		h := newTestHub(t)
		pipeNode(t, h, "box", func(context.Context) error {
			return &rpc.Error{
				Code:    "not_delivered",
				Message: specific,
				Data:    json.RawMessage(`{"delivered":"none"}`),
			}
		})
		ctx := i18n.With(context.Background(), i18n.En)
		status, body, _ := h.deliverOnce(ctx, h.byID["box"], map[string]any{"session": "s1", "text": "hi"})
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("body %s: %v", body, err)
		}
		if status != 409 || out["code"] != "not_delivered" || out["delivered"] != "none" || out["error"] != specific {
			t.Fatalf("specific refusal = %d %v, want preserved %q", status, out, specific)
		}
	})

	t.Run("partial settings refusals do not say nothing was typed", func(t *testing.T) {
		for _, tc := range []struct {
			name, code, reason, wantKey string
		}{
			{"terminal_refused", "not_delivered", "terminal_refused", "rpc-errors.modes_partial_not_delivered"},
			{"terminal_finished", "not_delivered", "terminal_finished", "rpc-errors.modes_partial_terminal_finished"},
			{"dialog_open", "dialog_open", "", "rpc-errors.modes_partial_dialog_open"},
		} {
			for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
				t.Run(tc.name+"/"+string(lang), func(t *testing.T) {
					h := newTestHub(t)
					const origDetail = "original refusal diagnostic"
					data := map[string]any{"delivered": "unknown", "partial": true, "detail": origDetail}
					if tc.reason != "" {
						data["reason"] = tc.reason
					}
					rawData, _ := json.Marshal(data)
					pipeNode(t, h, "box", func(context.Context) error {
						return &rpc.Error{Code: tc.code, Message: i18n.T(i18n.Default, tc.wantKey), Data: rawData}
					})
					ctx := i18n.With(context.Background(), lang)
					status, body, _ := h.deliverOnce(ctx, h.byID["box"], map[string]any{"session": "s1", "text": "hi"})
					var out map[string]any
					if err := json.Unmarshal(body, &out); err != nil {
						t.Fatalf("body %s: %v", body, err)
					}
					want := i18n.T(lang, tc.wantKey)
					if status != 409 || out["code"] != tc.code || out["delivered"] != "unknown" || out["error"] != want || out["detail"] != origDetail {
						t.Fatalf("partial(%s/%s) = %d %v, want 409 error=%q detail=%q", tc.name, lang, status, out, want, origDetail)
					}
				})
			}
		}
	})

	t.Run("delivery_unknown localizes to in_doubt with 502 and unknown delivery", func(t *testing.T) {
		for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
			t.Run(string(lang), func(t *testing.T) {
				h := newTestHub(t)
				const detail = "guide retry blocked after first attempt"
				pipeNode(t, h, "box", func(context.Context) error {
					return &rpc.Error{
						Code:    "delivery_unknown",
						Message: detail,
						Data:    json.RawMessage(`{"delivered":"unknown"}`),
					}
				})
				ctx := i18n.With(context.Background(), lang)
				status, body, _ := h.deliverOnce(ctx, h.byID["box"], map[string]any{"session": "s1", "text": "hi"})
				var out map[string]any
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("body %s: %v", body, err)
				}
				want := i18n.T(lang, "rpc-errors.in_doubt")
				if status != 502 || out["code"] != "delivery_unknown" || out["delivered"] != "unknown" || out["error"] != want || out["detail"] != detail {
					t.Fatalf("delivery_unknown(%s) = %d %v, want 502 error=%q detail=%q", lang, status, out, want, detail)
				}
			})
		}
	})

	t.Run("guide_retry_refused localizes structured fields in every language", func(t *testing.T) {
		for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
			t.Run(string(lang), func(t *testing.T) {
				h := newTestHub(t)
				pipeNode(t, h, "box", func(context.Context) error {
					return &rpc.Error{
						Code:    "delivery_unknown",
						Message: i18n.T(i18n.Default, "rpc-errors.guide_retry_refused", i18n.Args{"dialog": "Teach auto mode", "retry_code": "dialog_open"}),
						Data:    json.RawMessage(`{"delivered":"unknown","reason":"guide_retry_refused","dialog":"Teach auto mode","retry_code":"dialog_open"}`),
					}
				})
				ctx := i18n.With(context.Background(), lang)
				status, body, _ := h.deliverOnce(ctx, h.byID["box"], map[string]any{"session": "s1", "text": "hi"})
				var out map[string]any
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("body %s: %v", body, err)
				}
				want := i18n.T(lang, "rpc-errors.guide_retry_refused", i18n.Args{"dialog": "Teach auto mode", "retry_code": "dialog_open"})
				if status != 502 || out["code"] != "delivery_unknown" || out["delivered"] != "unknown" || out["error"] != want {
					t.Fatalf("guide_retry_refused(%s) = %d %v, want 502 error=%q", lang, status, out, want)
				}
			})
		}
	})
}
