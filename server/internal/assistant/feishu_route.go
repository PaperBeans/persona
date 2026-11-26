package assistant

import (
	"context"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Where the Feishu chat's messages go, as the core resolves it (the lark module reaches it through its host): a
// target's conversation or agent as it is now, whether the bot's switches allow it, the run that answers a message
// started in it, a topic's to-do and who takes what is said there, and where a run's messages go.

// startBotRun sends text (with its pictures) for the owner's message messageID into the target's conversation, or
// starts one, and starts the run that answers it. ext are the run's channel values besides the reaction (where the
// answer goes: runTo).
func (s *Service) startBotRun(ctx context.Context, rt resolvedTarget, t botTarget, messageID, text string, images []proto.Image, probe, reaction string, ext map[string]string) error {
	run := &channel.Run{ID: "fb_" + messageID, Channel: channel.Feishu, ReplyTo: messageID, Started: time.Now().UTC(), Label: rt.label,
		Probe: probe, Ext: ext}
	if run.Ext == nil {
		run.Ext = map[string]string{}
	}
	if reaction != "" {
		run.Ext["reaction"] = reaction
	}
	if rt.isNew {
		sess, err := s.Start(ctx, StartParams{Text: text, Images: images, Main: rt.main, Origin: OriginChannel, OriginRef: channel.Feishu})
		if err != nil {
			return err
		}
		run.MachineID, run.SessionID, run.TargetKind, run.Probe = sess.MachineID, sess.SessionID, targetPersona, ""
		if t.Kind == targetPersona { // /new: the chat goes on in it
			s.larkModule().SetTarget(botTarget{Kind: targetPersona, MachineID: sess.MachineID, SessionID: sess.SessionID})
		}
	} else {
		sid := rt.session.SessionID
		run.MachineID, run.SessionID, run.TargetKind = rt.machine, sid, t.Kind
		if run.TargetKind == targetMain {
			run.TargetKind = targetPersona
		}
		run.Base = s.lastSeq(ctx, rt.machine, sid)
		id := channel.ClientMsgID(channel.Feishu, messageID)
		run.MsgIDs = []string{id}
		p := map[string]any{"session_id": sid, "text": text, "client_msg_id": id}
		if len(images) > 0 {
			p["images"] = images
		}
		if !t.Agent() {
			s.UserSpoke(ctx, SessionRef{MachineID: rt.machine, SessionID: sid})
			var err error
			if p, err = s.SendParams(ctx, SendParams{MachineID: rt.machine, SessionID: sid, Params: p}); err != nil {
				return err
			}
		}
		if err := s.sendOnce(ctx, rt.machine, p); err != nil {
			return err
		}
	}
	s.startChannelRun(run)
	return nil
}

// feishuTopic is the chat and thread a message was said in.
type feishuTopic struct{ Chat, Thread string }

// target is where what is sent for that topic goes: its chat, as a reply into the thread. The zero topic (nil) is the
// private chat, where nothing changes.
func (t *feishuTopic) target() feishuTarget {
	if t == nil {
		return feishuTarget{}
	}
	return feishuTarget{ChatID: t.Chat, InThread: true}
}

// runTo is where a run's messages go, from its channel values (Ext): under the owner's "/read" in its group
// (the lark module's read.go), the topic it was asked in, else the private chat.
func runTo(ext map[string]string) feishuTarget {
	if readRun(ext) {
		return feishuTarget{ChatID: ext[readChatKey], InThread: ext[readThreadKey] != "", NoDetour: true, Read: true}
	}
	if ext["chat"] == "" || ext["thread"] == "" {
		return feishuTarget{}
	}
	return (&feishuTopic{Chat: ext["chat"], Thread: ext["thread"]}).target()
}

// todoOfThread is the todo th is bound to (open or done), from memory (the todos are loaded).
func (s *Service) todoOfThread(th TodoThread) (Todo, bool) {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	for _, t := range s.todos.todos {
		if t.Thread != nil && sameThread(*t.Thread, th) {
			return copyTodoLocked(t), true
		}
	}
	return Todo{}, false
}

// threadRoutes are who can get what was said in the topic of open todo t, best first: the agent it has that is running
// (the one that most needs it, pickTaskAgent; a closed one, offline or interrupted, is not counted), then the
// conversation that opened it (for a question's to-do, which has none, the conversation that asked: its record,
// ask_todo.go), then the main conversation. One the bot's switches do not allow, or that is not there,
// is left out; the error is why none is left (nil when there are routes).
func (s *Service) threadRoutes(ctx context.Context, t Todo, f FeishuFeatures) ([]botTarget, error) {
	var tried []botTarget
	var running []proto.Session
	for _, a := range s.taskAgents(ctx, t) {
		if a.Status != proto.StatusOffline && a.Status != proto.StatusInterrupted {
			running = append(running, a)
		}
	}
	if len(running) > 0 {
		c := pickTaskAgent(running)
		tried = append(tried, botTarget{Kind: targetAgent, MachineID: c.MachineID, SessionID: c.SessionID})
	}
	if t.Origin != "" {
		tried = append(tried, botTarget{Kind: targetPersona, SessionID: t.Origin})
	} else if a, ok := s.askTodoOf(ctx, t.ID); ok {
		tried = append(tried, botTarget{Kind: targetPersona, SessionID: a.SessionID})
	}
	tried = append(tried, botTarget{Kind: targetMain})
	l := s.botLang(ctx)
	var out []botTarget
	var last error
	for _, c := range tried {
		if err := botAllowed(l, f, c); err != nil {
			last = err
			continue
		}
		if c.Kind == targetPersona {
			// The conversation is looked for: a todo outlives the conversation that opened it.
			if _, ok := s.session(ctx, s.settings(ctx).MachineID, c.SessionID); !ok {
				continue
			}
		}
		if _, err := s.resolveBotTarget(ctx, c); err != nil {
			last = err
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 && last == nil {
		last = errors.New(i18n.T(l, "assistant-feishu.no_assistant_machine"))
	}
	return out, last
}

const readChatKey = "read_chat"

const readThreadKey = "read_thread"

// readRun says a run answers a /read: its answer goes under the message in that group, quietly.
func readRun(ext map[string]string) bool { return ext[readChatKey] != "" }

// openTarget is who takes a to-do opened from a topic or from elsewhere: the main conversation, which origin is the id
// of. fail says why not, in the bot's language, "" when it can.
func (s *Service) openTarget(ctx context.Context, st FeishuStatus) (target botTarget, origin, fail string) {
	l := s.botLang(ctx)
	failed := func(err error) string {
		return i18n.T(l, "assistant-feishu.thread_open_failed", i18n.Args{"error": clip(oneLine(err.Error()), 200)})
	}
	target = botTarget{Kind: targetMain}
	if err := botAllowed(l, st.Features, target); err != nil {
		return target, "", failed(err)
	}
	rt, err := s.resolveBotTarget(ctx, target)
	if err != nil {
		return target, "", failed(err)
	}
	origin = s.settings(ctx).Main
	if _, ok := s.session(ctx, rt.machine, origin); origin == "" || rt.isNew || !ok {
		return target, "", i18n.T(l, "assistant-feishu.thread_open_no_main")
	}
	return target, origin, ""
}
