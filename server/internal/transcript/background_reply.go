package transcript

import (
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// ReplyPlaceholder reports a known whole-reply stand-in emitted by a runtime.
func ReplyPlaceholder(text string) bool {
	switch strings.TrimRight(strings.ToLower(strings.TrimSpace(text)), ".。!！ ") {
	case "no response requested", "(no content)", "no content", "[no content]":
		return true
	}
	return false
}

// FoldBackgroundPlaceholders displays a background turn's placeholder as folded work. Keep the item ID so
// a previously loaded reply is replaced by the projection. The native transcript is still the source of truth.
func (f *File) FoldBackgroundPlaceholders(items []proto.Item) []proto.Item {
	var out []proto.Item
	for i, item := range items {
		if item.Seq <= 0 || item.Role != "assistant" || item.Kind != proto.KindText || item.Narration ||
			item.FullSize != 0 || !ReplyPlaceholder(item.Text) {
			continue
		}
		// An incremental watch or a page can start inside the round. Read the actual preceding input.
		before, _, _ := f.PageTurns(item.Seq, 1, 2000)
		if _, background := lastBackgroundInput(before); background {
			if out == nil {
				out = slices.Clone(items)
			}
			out[i].Kind = proto.KindThinking
		}
	}
	if out == nil {
		return items
	}
	return out
}

// BackgroundReplyMissing checks the latest verified background input and its final text. A projected placeholder
// can already be folded as thinking, which carries the same words. A normal answer overrides earlier work.
func BackgroundReplyMissing(items []proto.Item) bool {
	items, start, background := quietBackgroundInput(items)
	if !background {
		return false
	}
	if start == len(items)-1 {
		if previous, previousBackground := lastBackgroundInput(items[:start]); previous >= 0 && !previousBackground {
			// A trailing input with no output yet belongs to the next turn. It must not silence the completed
			// non-background exchange above it; an earlier background turn keeps the missing-reply behavior.
			return false
		}
	}
	for i := len(items) - 1; i > start; i-- {
		item := items[i]
		if item.Role != "assistant" || item.Narration {
			continue
		}
		if item.Kind == proto.KindText && strings.TrimSpace(item.Text) != "" {
			return item.FullSize == 0 && ReplyPlaceholder(item.Text)
		}
		if item.Kind == proto.KindThinking && item.FullSize == 0 && ReplyPlaceholder(item.Text) {
			return true
		}
	}
	return true // a known background run with no written answer
}

// RoutineConversationReply identifies a completed relay with a substantive answer and no explicit owner action.
// A missing, interrupted or unrecognizable answer cannot authorize silencing another runtime's finished alert.
func RoutineConversationReply(items []proto.Item) bool {
	items, start, background := quietBackgroundInput(items)
	if !background {
		return false
	}
	source := prompts.ItemSource(items[start])
	if source == nil || source.Kind != proto.SourceConversation {
		return false
	}
	reply := items[start+1:]
	for i := len(reply) - 1; i >= 0; i-- {
		it := reply[i]
		if it.IsError || it.Wake || it.Kind == proto.KindToolUse || it.Kind == proto.KindThinking || it.Kind == proto.KindFold {
			return false
		}
		if it.Role == "assistant" && it.Kind == proto.KindText && strings.TrimSpace(it.Text) != "" {
			return !it.Narration && !it.CodexHeartbeatQuiet && (it.FullSize > 0 || !ReplyPlaceholder(it.Text))
		}
	}
	return false
}

// A new relay can arrive between a completion and its delayed push. Skip its preliminary work until it has
// an outcome. Only a background round without a failure or an explicit action for the owner may go quiet.
func quietBackgroundInput(items []proto.Item) ([]proto.Item, int, bool) {
	for {
		start, background := lastBackgroundInput(items)
		if !background {
			return items, start, false
		}
		turn := items[start:]
		for _, it := range turn {
			promoted := it.SupervisionUser || it.Fold != nil && it.Fold.SupervisionUser
			failedNotice := it.Kind == proto.KindNotice && (it.IsError || it.Text == InterruptedNotice || strings.HasPrefix(it.Text, APIErrorPrefix))
			failedAnswer := it.Role == "assistant" && it.Kind == proto.KindText && !it.Narration && (it.IsError || assistantwire.CutOff(it.Text))
			if promoted || failedNotice || failedAnswer || it.ModelRefusal != nil {
				return items, start, false
			}
		}
		if HasUserVisibleAction(turn) {
			return items, start, false
		}
		source := prompts.ItemSource(items[start])
		if start == 0 || source == nil || source.Kind != proto.SourceConversation || relayOutcomeIn(turn) {
			return items, start, background
		}
		items = items[:start]
	}
}

// The runtime can acknowledge completion before it supplies the text. A duration for the previous input
// can arrive late and cannot establish that the new relay finished.
func relayOutcomeIn(turn []proto.Item) bool {
	for _, it := range turn[1:] {
		if it.TurnDuration != nil && it.TurnDuration.UserItemID != "" && it.TurnDuration.UserItemID == turn[0].ID {
			return true
		}
		if it.Role == "assistant" && !it.Narration && !it.CodexHeartbeatQuiet && (it.Kind == proto.KindText && strings.TrimSpace(it.Text) != "" ||
			it.Kind == proto.KindThinking && ReplyPlaceholder(it.Text)) {
			return true
		}
	}
	return false
}

// lastBackgroundInput stops at the last input or wake. Tool results are part of the input's turn.
func lastBackgroundInput(items []proto.Item) (int, bool) {
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.Wake {
			return i, false
		}
		if item.Role != "user" || item.ToolUseID != "" {
			continue
		}
		if item.Kind == proto.KindImage {
			if i == 0 || item.Seq != items[i-1].Seq {
				return i, false
			}
			continue // the accompanying text on the same line carries the source
		}
		if item.Kind != proto.KindText {
			continue
		}
		if item.ClientMsgID != "" || item.Queued {
			return i, false
		}
		source := prompts.ItemSource(item)
		if source == nil {
			return i, false
		}
		switch source.Kind {
		case proto.SourceSupervision, proto.SourcePatrol, proto.SourceScheduledTask, proto.SourceConversation, proto.SourceNotice:
			return i, true
		case proto.SourceFollowup:
			// A scheduled follow-up has its own title. A wake or usage-limit continuation can be the owner's turn.
			in := item.Injected
			if in == nil {
				in = prompts.ParseInjected(item.Text)
			}
			return i, strings.TrimSpace(source.Title) != "" && !prompts.ContinuesOwnWork(in)
		}
		return i, false
	}
	return -1, false
}
