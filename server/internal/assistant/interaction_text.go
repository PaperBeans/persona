package assistant

import (
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Wording of an interaction and how it was answered, shared by the cards, the briefing and the asks (moved out of feishu_cards.go, #13404).

// interactionText is what a card of ix says, for tagging it on its way out: what it asks first, its titles and the
// terminal last.
func interactionText(ix proto.Interaction) string {
	parts := []string{ix.Summary}
	for _, q := range ix.Questions {
		parts = append(parts, q.Header, q.Question)
		for _, o := range q.Options {
			parts = append(parts, o.Label, o.Description)
		}
	}
	parts = append(parts, ix.Plan, ix.Reason, ix.EditableText, ix.ReviewReport)
	if ix.Elicitation != nil {
		parts = append(parts, ix.Elicitation.Message)
	}
	parts = append(parts, jsonStrings(proto.RedactArgs(ix.ToolInput))...)
	parts = append(parts, ix.SessionTitle, ix.TaskTitle, ix.Screen)
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// endedText says in l why a card is no longer pending, from interactions.get's status: it expired, or it was
// withdrawn (cancelled); otherwise (answered, dismissed, unknown) it is the text of fallback, a key.
func endedText(l i18n.Lang, status, fallback string) string {
	switch status {
	case "expired":
		return i18n.T(l, "assistant-feishu_cards.card_expired")
	case "cancelled":
		return i18n.T(l, "assistant-feishu_cards.card_withdrawn")
	}
	return i18n.T(l, fallback)
}

// resolutionText says in l how an interaction was answered, for its closed card. An answer someone other than the user
// gave (AnsweredBy: the assistant or a supervisor, the recommendation taken at a deadline, the terminal) says who gave
// it, so it never reads as the user's own "Allowed" or "Denied".
func resolutionText(l i18n.Lang, ix proto.Interaction, r proto.Resolution) string {
	how := answerText(l, ix, r)
	switch r.AnsweredBy {
	case "":
		return how
	case proto.AnsweredByAssistant:
		return i18n.T(l, "assistant-feishu_cards.by_assistant", i18n.Args{"how": how})
	case proto.AnsweredByDefault:
		return i18n.T(l, "assistant-feishu_cards.by_default", i18n.Args{"how": how})
	case proto.AnsweredByTerminal:
		return i18n.T(l, "assistant-feishu_cards.by_terminal", i18n.Args{"how": how})
	}
	return i18n.T(l, "assistant-feishu_cards.by_other", i18n.Args{"how": how})
}

// answerText is resolutionText without who answered.
func answerText(l i18n.Lang, ix proto.Interaction, r proto.Resolution) string {
	switch {
	case r.Dismiss && r.Reason == proto.DismissedInChat && r.AnsweredBy == "":
		return i18n.T(l, "assistant-feishu_cards.replied_in_chat")
	case r.Dismiss && r.Reason == proto.DismissedExpired && r.AnsweredBy == "":
		return i18n.T(l, "assistant-feishu_cards.moved_to_todos")
	case r.Dismiss:
		return i18n.T(l, "assistant-feishu_cards.dismissed")
	case r.Approve != nil && *r.Approve:
		switch r.Mode {
		case "bypassPermissions":
			return i18n.T(l, "assistant-feishu_cards.approved_run")
		case "acceptEdits":
			return i18n.T(l, "assistant-feishu_cards.approved_edits")
		}
		return i18n.T(l, "assistant-feishu_cards.approved")
	case r.Approve != nil:
		if r.Feedback != "" {
			return i18n.T(l, "assistant-feishu_cards.sent_back_note", i18n.Args{"note": clip(r.Feedback, 200)})
		}
		return i18n.T(l, "assistant-feishu_cards.sent_back")
	case r.Allow != nil && *r.Allow:
		if r.Always {
			return i18n.T(l, "assistant-feishu_cards.allowed_always")
		}
		return i18n.T(l, "assistant-feishu_cards.allowed")
	case r.Allow != nil:
		if r.Message != "" {
			return i18n.T(l, "assistant-feishu_cards.denied_note", i18n.Args{"note": clip(r.Message, 200)})
		}
		return i18n.T(l, "assistant-feishu_cards.denied")
	case len(r.Answers) > 0:
		var parts []string
		for _, q := range ix.Questions {
			if a, ok := r.Answers[q.Question]; ok {
				parts = append(parts, a)
			}
		}
		if len(parts) == 0 {
			for _, a := range r.Answers {
				parts = append(parts, a)
			}
		}
		return i18n.T(l, "assistant-feishu_cards.answered_with", i18n.Args{"answers": clip(strings.Join(parts, i18n.T(l, "assistant-feishu_cards.answer_sep")), 300)})
	case r.Option != "" && ix.Dialog != nil:
		if o := ix.Dialog.Option(r.Option); o != nil {
			return i18n.T(l, "assistant-feishu_cards.picked", i18n.Args{"option": o.Label})
		}
	case len(r.Keys) > 0 || r.Text != "":
		return i18n.T(l, "assistant-feishu_cards.responded")
	}
	return i18n.T(l, "assistant-feishu_cards.handled")
}
