package lark

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// A message from a group chat's topic (#5279, docs/assistant/todos.md "Chat thread", docs/extensions/bots.md): the bot's chat with the
// owner is the private one; a topic is a place the owner talks about one to-do. The hub does not hand group messages over yet
// (its own gate comes with a later step), so this is also the second line when it does.
//
// A topic message is not the private chat's: no commands (a text starting with "/" is a word like any other), no
// answering the waiting card or a "Please verify" card, and never the chat's current target. It goes by the topic
// alone: the to-do bound to {feishu, chat_id, thread_id} (Todo.Thread) takes it as a decision and its agent gets it,
// and the answer goes back into the same topic. A topic bound to nothing open gets one short hint.

// threadHintEvery is how often a topic is told again that it is tied to no open to-do.
const threadHintEvery = 10 * time.Minute

// threadHintsKeep bounds the topics remembered for it.
const threadHintsKeep = 200

// threadMessage takes a message from a group chat: the to-do bound to its topic records it and its agent gets it.
func (s core) threadMessage(ctx context.Context, ev botcard.MessageEvent, st FeishuStatus, content botcard.Content) {
	msg := ev.Message
	// Only the owner's own words count here, whatever reaches this far: the hub checks it too (a later step), and the
	// words go to an agent that acts on them.
	if ev.Sender.SenderType != "user" || st.Owner == "" || ev.Sender.SenderID.OpenID != st.Owner {
		s.logf("bots: a group message not from the owner was dropped")
		return
	}
	if msg.ChatID == "" || msg.ThreadID == "" {
		s.logf("bots: a message outside a topic of a group chat was left alone")
		return // nowhere to answer in, and not a topic of a to-do
	}
	// Where the bot's own lines into this topic reply to (a todo's posts, the lark module's thread_posts.go): any message of it will do.
	s.rememberTopicMessage(ctx, msg.ChatID, msg.ThreadID, msg.MessageID)
	l := s.botLang(ctx)
	topic := &feishuTopic{Chat: msg.ChatID, Thread: msg.ThreadID}
	to := topic.target()
	if err := s.h.EnsureTodosLoaded(ctx); err != nil {
		s.logf("bots: topic message: todos not loaded: %v", err)
		s.botNoteTo(ctx, to, msg.MessageID, i18n.T(l, "assistant-feishu.not_sent", i18n.Args{"error": clip(oneLine(err.Error()), 200)}))
		return
	}
	t, bound := s.h.TodoOfThread(msg.ChatID, msg.ThreadID)
	if !bound && s.openTopicIfAsked(ctx, ev, st, content, topic) {
		return // a topic bound to nothing that starts one, or "/ai …" in one: a to-do is opened (thread_open.go)
	}
	if bound && !content.Forward && s.h.NoReplyCommand(openingCommandWords(ev, content)) { // after the bot's @, which a group that only sends what @s the bot starts every message with
		// "Don't report back": a command of its own, also for a todo that is done and not yet reported, and never passed on
		// as words (the core's todo_source.go).
		s.h.ThreadNoReply(ctx, ev, t.ID, topic.Chat, topic.Thread)
		return
	}
	if !bound || t.Done {
		key := "assistant-feishu.thread_unbound"
		if bound {
			key = "assistant-feishu.thread_done"
		}
		// "/ai" for a to-do that is done is asked for in so many words: it is answered each time, not once in ten minutes.
		if _, asked := aiCommand(openingCommandWords(ev, content)); bound && asked || s.threadHintDue(msg.ChatID+"/"+msg.ThreadID) {
			s.botNoteTo(ctx, to, msg.MessageID, i18n.T(l, key))
		}
		return
	}
	text := strings.TrimSpace(content.Text)
	if content.Forward || text == "" && len(content.Media) == 0 {
		// Merged forwards are not opened here (others' words), and a message with nothing in it has nothing to pass on.
		s.botNoteTo(ctx, to, msg.MessageID, i18n.T(l, "assistant-feishu.cannot_read"))
		return
	}
	// Who gets it is settled before anything is written; with no one, the words are still recorded and the user is told so.
	routes, routeErr := s.h.ThreadRoutes(ctx, t.ID, st.Features)
	recorded := false
	if text != "" {
		// Noted as the to-do's decision (the core's ledger; a failure there is only logged: the words still go to the
		// agent).
		switch s.h.TopicDecision(ctx, t.ID, i18n.T(l, "assistant-feishu.thread_question"), text, msg.MessageID) {
		case assistantwire.DecisionTodoGone: // deleted since it was looked up
			if s.threadHintDue(msg.ChatID + "/" + msg.ThreadID) {
				s.botNoteTo(ctx, to, msg.MessageID, i18n.T(l, "assistant-feishu.thread_unbound"))
			}
			return
		case assistantwire.DecisionSeen:
			return // this message was handed over before: once is all
		case assistantwire.DecisionNoted:
			recorded = true
		}
	}
	notDelivered := func(err error) {
		key := "assistant-feishu.not_sent"
		if recorded {
			key = "assistant-feishu.thread_recorded_not_sent"
		}
		s.botNoteTo(ctx, to, msg.MessageID, i18n.T(l, key, i18n.Args{"error": clip(oneLine(err.Error()), 200)}))
	}
	if text != "" {
		label := msg.ThreadID
		if strings.TrimSpace(t.ThreadTitle) != "" {
			label = t.ThreadTitle
		}
		s.h.TopicWake(ctx, t.ID, "feishu thread "+label, text, "feishu_thread:"+msg.ChatID+"/"+msg.ThreadID+"@"+msg.MessageID)
	}
	if len(routes) == 0 {
		s.logf("bots: topic message of todo %s: no one to give it to: %v", t.ID, routeErr)
		notDelivered(routeErr)
		return
	}
	content.Text = prompts.RenderTodoThreadReply(prompts.TodoThreadReply{Language: s.h.AnswerLanguage(s.hello(ctx).Locale), Title: contextText(clip(oneLine(t.Title), 80)), Text: text, Recorded: recorded})
	// A to-do that came from a question nobody answered (the core's ask_todo.go): the reply is the owner's call, and goes to the
	// conversation that asked (not to an Agent) as the answer; the to-do then closes.
	asked := false
	if convs := slices.DeleteFunc(slices.Clone(routes), func(r botTarget) bool { return r.Kind == targetAgent }); text != "" && len(convs) > 0 {
		if answer, ok := s.h.AskTodoTopicAnswer(ctx, t.ID, text); ok {
			content.Text, routes, asked = answer, convs, true
		}
	}
	var err error
	for _, target := range routes { // one that cannot take it (offline, gone, refusing) leaves it to the next
		if err = s.botSendIn(ctx, &ev, content, target, topic); err == nil {
			if asked {
				s.h.SettleAskClaim(ctx, t.ID, true)
				s.h.CloseAskTodo(ctx, t.ID)
			}
			return
		}
		s.logf("bots: topic message of todo %s to %s: %v", t.ID, target.Kind, err)
	}
	if asked {
		s.h.SettleAskClaim(ctx, t.ID, false)
	}
	notDelivered(err)
}

// threadHintDue says whether the topic key is to be told it has no open to-do now: once in threadHintEvery.
func (s core) threadHintDue(key string) bool {
	b := s.fbot()
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	if at, ok := b.hinted[key]; ok && now.Sub(at) < threadHintEvery {
		return false
	}
	if b.hinted == nil {
		b.hinted = map[string]time.Time{}
	}
	for k, at := range b.hinted {
		if now.Sub(at) >= threadHintEvery {
			delete(b.hinted, k)
		}
	}
	if len(b.hinted) >= threadHintsKeep { // a flood of topics: the hints stop rather than the table grow
		return false
	}
	b.hinted[key] = now
	return true
}
