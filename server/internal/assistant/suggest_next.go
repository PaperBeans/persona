package assistant

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The next-message suggestion (docs/assistant/assistant.md "Next-message suggestions", #4644): the strip over an
// empty composer (#2978) has two sources and one field, proto.Session.Suggestion.
//
//   - A Claude Code session in a terminal keeps the one Claude Code draws on its own screen, which the node reads
//     (node/suggestion.go). No model call is made for it here.
//   - Every other chat (Persona's own conversations, Codex and the other runtimes, a Claude session with no terminal)
//     gets one from a small model under the suggest.next purpose, when the user turned the switch on
//     (Settings.NextSuggestions, off by default).
//
// One call per turn: after a turn that ended well (not failed, not interrupted, not a subagent's), and only while an
// app has the chat open (the hub watches the session for it). Every app with the chat open sees the same one; a new
// turn or a message going in drops it and cancels a call still on its way. The model reads at most the last
// suggestMaxMessages visible user and assistant messages, suggestMaxChars characters in all: no tool calls or
// results, no thinking, no images or attachments, and it runs with no tools. An answer that is not one short line
// shows nothing: there is no stand-in and no retry. Whatever the source, a suggestion that names a command, a path, a
// link, a secret-looking string or a risky action (riskyWords) is marked SuggestionRisky: the apps fill it in but
// offer no one-tap send.

const purposeSuggestNext = "suggest.next"

const (
	suggestMaxMessages = 8
	suggestMaxChars    = 12000
	suggestMaxRunes    = 200 // a longer answer is not one message the user would send as it is
	suggestTurns       = 4   // turns read back: enough for suggestMaxMessages user and assistant messages
	suggestMaxTokens   = 1024
)

// suggestCallTimeout bounds the whole suggestion (reading the messages, the model call); tests shorten it.
var suggestCallTimeout = 45 * time.Second

// nextSuggest is one session's hub-made suggestion: the one shown, or the call making it.
type nextSuggest struct {
	text   string
	cancel context.CancelFunc // the call still on its way; nil once it answered
}

type nextSuggestBook struct {
	mu sync.Mutex
	m  map[[2]string]*nextSuggest // machine, session
	// Tests: watched stands in for the hub's watches, done sees every call end (its suggestion, "" for none).
	watched func(machine, sid string) bool
	done    func(machine, sid, text string)
}

// nextSuggestionsOn is the switch (next_suggestions, off by default).
func (s *Service) nextSuggestionsOn(ctx context.Context) bool {
	v := s.settings(ctx).NextSuggestions
	return v != nil && *v
}

// hubSuggests says whether the hub makes sess's suggestion: not for a Claude Code terminal session (it has its own),
// nor for a subagent.
func hubSuggests(sess *proto.Session) bool {
	if sess.IsSubagent || sess.ParentThreadID != "" || strings.Contains(sess.SessionID, "@") {
		return false
	}
	return !claudeInTerminal(sess)
}

// claudeInTerminal: Claude Code in a terminal pane, whose suggestion the node reads off the screen.
func claudeInTerminal(sess *proto.Session) bool {
	return sess.PaneID != "" && (sess.Agent == "" || sess.Agent == "claude")
}

// sessionWatched says whether an app has the session open: the hub watches it then (session.watch).
func (s *Service) sessionWatched(machine, sid string) bool {
	b := &s.nextSug
	b.mu.Lock()
	f := b.watched
	b.mu.Unlock()
	if f != nil {
		return f(machine, sid)
	}
	return s.devs != nil && s.devs.Watched(machine, sid)
}

// suggestDrop forgets the session's suggestion and cancels a call still on its way: a turn started or a message went
// in. It reports whether there was one to drop.
func (s *Service) suggestDrop(machine, sid string) bool {
	b := &s.nextSug
	k := [2]string{machine, sid}
	b.mu.Lock()
	st := b.m[k]
	delete(b.m, k)
	b.mu.Unlock()
	if st == nil {
		return false
	}
	if st.cancel != nil {
		st.cancel()
	}
	return st.text != ""
}

// suggestTurnEnded is told of every turn that ended (working to done or idle, fleet_sessions.go setSessions): it starts
// the turn's one suggestion when the rules say so, and reports whether it did.
func (s *Service) suggestTurnEnded(machine string, sess proto.Session) bool {
	ctx := context.Background()
	if !hubSuggests(&sess) || !s.nextSuggestionsOn(ctx) {
		return false
	}
	if text, _ := latestTurnError(sess); text != "" { // a failed turn
		return false
	}
	if !s.sessionWatched(machine, sess.SessionID) { // nobody has it open
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, suggestCallTimeout)
	st := &nextSuggest{cancel: cancel}
	k := [2]string{machine, sess.SessionID}
	b := &s.nextSug
	b.mu.Lock()
	if b.m == nil {
		b.m = map[[2]string]*nextSuggest{}
	}
	old := b.m[k]
	b.m[k] = st
	b.mu.Unlock()
	if old != nil && old.cancel != nil {
		old.cancel()
	}
	go func() {
		defer cancel()
		text := s.makeSuggestion(cctx, machine, sess.SessionID)
		b.mu.Lock()
		current := b.m[k] == st && cctx.Err() == nil
		if current {
			st.text, st.cancel = text, nil
			if text == "" {
				delete(b.m, k)
			}
		}
		done := b.done
		b.mu.Unlock()
		if !current {
			text = ""
		}
		if text != "" {
			s.publishSessions(machine) // the apps get it with the list
		}
		if done != nil {
			done(machine, sess.SessionID, text)
		}
	}()
	return true
}

// hubSuggestions are the machine's hub-made suggestions shown now, by session; nil for none.
func (s *Service) hubSuggestions(machine string) map[string]string {
	b := &s.nextSug
	b.mu.Lock()
	defer b.mu.Unlock()
	var out map[string]string
	for k, st := range b.m {
		if k[0] == machine && st.text != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[k[1]] = st.text
		}
	}
	return out
}

// decorateSuggestion puts the hub's suggestion on a session the node has none for, while it is not working, and
// marks a risky one (from either source).
func decorateSuggestion(sess *proto.Session, hub map[string]string) {
	if sess.Suggestion == "" && sess.Status != proto.StatusWorking && hubSuggests(sess) {
		sess.Suggestion = hub[sess.SessionID]
	}
	sess.SuggestionRisky = sess.Suggestion != "" && suggestionRisky(sess.Suggestion)
}

// makeSuggestion reads the chat's last visible messages and asks the model for the user's next one; "" for none.
func (s *Service) makeSuggestion(ctx context.Context, machine, sid string) string {
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, machine, DeviceSessionItems, map[string]any{"session_id": sid, "turns": suggestTurns, "lazy_images": true}, &res); err != nil {
		s.logf("suggest %s: %v", short(sid), err)
		return ""
	}
	turns := suggestionTurns(res.Items)
	if len(turns) == 0 {
		return ""
	}
	system := prompts.RenderNextMessage(prompts.NextMessage{Language: answerLanguage(s.hello(ctx).Locale)})
	r, err := s.purposeModel(purposeSuggestNext).Chat(ctx, ai.Request{System: system,
		Messages: []ai.Message{{Role: ai.RoleUser, Content: prompts.RenderNextMessageInput(turns)}}, MaxTokens: suggestMaxTokens})
	if err != nil {
		if ctx.Err() == nil && !ai.IsPaused(err) {
			s.logf("suggest %s: %v", short(sid), err)
		}
		return ""
	}
	if r.Truncated {
		return ""
	}
	return cleanSuggestion(r.Text)
}

// suggestionTurns are the visible user and assistant messages the model reads: the last suggestMaxMessages, oldest
// first, suggestMaxChars characters in all (the newest kept whole first, an older one cut from its start).
func suggestionTurns(items []proto.Item) []prompts.NextMessageTurn {
	var picked []prompts.NextMessageTurn
	left := suggestMaxChars
	for i := len(items) - 1; i >= 0 && len(picked) < suggestMaxMessages && left > 0; i-- {
		it := &items[i]
		if it.Kind != proto.KindText || (it.Role != "user" && it.Role != "assistant") || it.Image != nil ||
			it.Injected != nil || it.Wake || it.Compact != nil || it.ToolUseID != "" {
			continue
		}
		text := strings.TrimSpace(it.Text)
		if it.Role == "assistant" {
			text = strings.TrimSpace(wire.AssistantCardText(text))
		}
		if text == "" {
			continue
		}
		if n := utf8.RuneCountInString(text); n > left {
			r := []rune(text)
			text = "…" + string(r[n-left+1:])
		}
		left -= utf8.RuneCountInString(text)
		picked = append(picked, prompts.NextMessageTurn{Role: it.Role, Text: text})
	}
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	return picked
}

// cleanSuggestion is the model's answer as a suggestion: one line, unquoted; "" for NONE, nothing, or more than a
// short message.
func cleanSuggestion(out string) string {
	t := strings.TrimSpace(out)
	if strings.ContainsAny(t, "\r\n") { // more than one line is not one message
		return ""
	}
	// i18n-ignore: quote marks the model may wrap its answer in, matched and stripped, never shown
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}, {"「", "」"}, {"'", "'"}, {"`", "`"}} {
		if len(t) >= len(q[0])+len(q[1]) && strings.HasPrefix(t, q[0]) && strings.HasSuffix(t, q[1]) {
			t = strings.TrimSpace(t[len(q[0]) : len(t)-len(q[1])])
		}
	}
	// i18n-ignore: the full stops the model may end its "NONE" with, matched and stripped, never shown
	if t == "" || strings.EqualFold(strings.Trim(t, ".。 "), "none") || utf8.RuneCountInString(t) > suggestMaxRunes {
		return ""
	}
	return t
}

// What makes a suggestion risky beyond riskyWords: a command, a path, a link, a secret-looking string.
var (
	suggestCommand = lazyre.New("(?i)`|(?:^|\\s|\\p{Han})(?:\\$|sudo|rm|git|npm|npx|pnpm|yarn|cargo|curl|wget|ssh|scp|kubectl|docker|helm|terraform|systemctl|bash|python3?|pip|brew|chmod|chown|mkfs|pkill|psql)\\s+\\S")
	suggestPath    = lazyre.New(`(?:^|[\s(])(?:~/|\./|\.\./|/[A-Za-z0-9._-]+/)|[A-Za-z]:\\|\b[\w.-]+/[\w.-]+\.[A-Za-z0-9]{1,6}\b`)
	suggestLink    = lazyre.New(`(?i)\b[a-z][a-z0-9+.-]*://|\bwww\.|\b[\w-]+\.(?:com|net|org|io|dev|app|cn|co)\b`)
	suggestToken   = lazyre.New(`(?i)\b(?:sk-|ghp_|gho_|github_pat_|xox[bpa]-|akia)[A-Za-z0-9_-]{8,}|[A-Za-z0-9+/_=-]{24,}`)
)

// suggestionRisky: the suggestion names a command, a path, a link, a secret-looking string or a risky action
// (riskyWords): the apps fill it in but do not send it with one tap.
func suggestionRisky(t string) bool {
	if risky(t) || suggestCommand.MatchString(t) || suggestPath.MatchString(t) || suggestLink.MatchString(t) {
		return true
	}
	for _, tok := range suggestToken.FindAllString(t, -1) {
		// a key's prefix, or a long run that mixes letters and digits
		if len(tok) < 24 || (strings.ContainsAny(tok, "0123456789") && strings.ContainsAny(strings.ToLower(tok), "abcdefghijklmnopqrstuvwxyz")) {
			return true
		}
	}
	return false
}
