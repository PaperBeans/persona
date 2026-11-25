package assistant

import (
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/larkhost"
	"github.com/nwverse/persona/server/internal/proto"
)

// A to-do's chat thread as the core keeps it for the Feishu topic lines (the lark module's thread_posts.go): the topic
// messages the bot can reply under (kept with the chat's state), which to-do an agent works for, the decisions written
// (each told to the to-do's topic through the outbound port), and the agents' finished turns and asks a topic is told of.

const (
	// threadPostSettle is how long the lark module lets a quiet to-do's first topic line wait (its thread_posts.go), so
	// what comes right behind it is one line; a source report planned at a start that failed waits as long.
	threadPostSettle = 3 * time.Second
)

// threadPoster is the clock and the timer a to-do's topic lines and its source report wait on; tests replace them.
type threadPoster struct {
	now   func() time.Time
	after func(d time.Duration, f func())
	// asked, when set, is called when threadAgentAsks has decided (tests: the decision runs on its own goroutine).
	asked func()
}

// threadOf is the thread todo id is bound to, from memory; nil when it has none (or is gone).
func (s *Service) threadOf(id string) *TodoThread {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	if t, ok := s.todos.todos[id]; ok && t.Thread != nil {
		th := *t.Thread
		return &th
	}
	return nil
}

// insertDecision is InsertTodoDecision, and tells the todo's topic of a decision that was written (postable says
// which). Every writer of decisions goes through it, so a new source cannot forget the topic.
func (s *Service) insertDecision(ctx context.Context, st todoStore, row db.TodoDecisionRow) (bool, error) {
	ok, err := st.InsertTodoDecision(ctx, row)
	if err == nil && ok {
		s.todoDecisionEvent(row)
		if row.Source == db.DecisionManual {
			s.askTodoDecided(context.WithoutCancel(ctx), row) // the owner's call on a question's to-do (ask_todo.go)
		}
		s.parkDecided(context.WithoutCancel(ctx), row) // what a parked task waited for may have come (parked.go)
	}
	if err == nil && ok && postable(row) {
		s.chat().ThreadPost(row.TodoID, larkhost.ThreadEvent{Kind: larkhost.ThreadDecision, Text: decisionLine(row)})
	}
	return ok, err
}

// threadAgentDone: an agent's finished turn (the one the phone is pushed, notifyFinished) is told to the topic of
// the todo it works on.
func (s *Service) threadAgentDone(machine string, sess proto.Session) {
	if id := s.activeTodoOf(machine, sess.SessionID); id != "" {
		s.chat().ThreadPost(id, larkhost.ThreadEvent{Kind: larkhost.ThreadAgentFinished, Agent: threadName(agentLabel(sess))})
	}
}

// threadAgentAsks: a card an agent put to the user (the one the phone is pushed, tell) is told to the topic of the
// todo it works on, unless that topic knows already: the card went into it (its run was asked there, same chat and
// thread), or a line there says the card went to the DM (a private run of the conversation wins). Where the card is
// decides what the line says: in the DM, the line only says so (the summary of an approval is not for a group); no card
// at all (the cards switch is off), the line is the notice and carries the summary, but never a permission's.
func (s *Service) threadAgentAsks(ix proto.Interaction) {
	if f := s.tposts.asked; f != nil {
		defer f()
	}
	id := s.activeTodoOf(ix.MachineID, ix.SessionID)
	th := s.threadOf(id)
	if th == nil {
		return
	}
	private, topic := s.interactionRuns(ix)
	if topic != nil {
		ext := s.runExts(topic, "chat", "thread")
		if ext["chat"] == th.ChatID && ext["thread"] == th.ThreadID {
			return // the card is in this topic, or this topic was told it went to the DM
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ev := larkhost.ThreadEvent{Kind: larkhost.ThreadAgentNeeds, Agent: threadName(firstNonEmpty(ix.SessionTitle, ix.TaskTitle))}
	switch {
	case !s.larkModule().AskWanted(ctx, ix):
		// No card anywhere: the line is the notice, and says what is asked, except for a permission: what a tool is about
		// to do (a command, a path) is not for a group chat, only that the Agent needs the user.
		if ix.Kind != proto.IxPermission {
			ev.Text = clip(oneLine(ix.Summary), 80)
		}
	case private != nil || topic == nil:
		ev.DM = true
	}
	s.chat().ThreadPost(id, ev)
}

// threadName is an agent's name as a line quotes it: one short line, "" when there is none (a bare directory mark is none).
func threadName(s string) string {
	s = strings.TrimSpace(clip(oneLine(s), 40))
	if s == "." || s == "/" {
		return ""
	}
	return s
}

// activeTodoOf is the open todo the agent machine/session works for, "" when it works for none.
func (s *Service) activeTodoOf(machine, session string) string {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	return s.todos.activeBySession[[2]string{machine, session}]
}

func (p *threadPoster) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *threadPoster) later(d time.Duration, f func()) {
	if p.after != nil {
		p.after(d, f)
		return
	}
	time.AfterFunc(d, f)
}
