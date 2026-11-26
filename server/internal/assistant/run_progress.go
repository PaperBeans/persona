package assistant

// What a Feishu run's progress card shows of the run, read from the channels' live state (chanLive), which is the
// core's: the lark module draws the card from it (larkHost.RunProgress).

import (
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
)

// botTargetOf is what a run's conversation is to the chat: replies to the bot's messages about it go there.
func botTargetOf(r *channel.Run) botTarget {
	kind := r.TargetKind
	if kind == "" || kind == targetMain {
		kind = targetPersona
	}
	return botTarget{Kind: kind, MachineID: r.MachineID, SessionID: r.SessionID}
}

// runProgress is what run r's progress card shows besides its steps, now.
func (s *Service) runProgress(r *channel.Run) assistantwire.RunProgress {
	waiting := s.runWaiting(r)
	v := s.runLiveView(r.ID)
	v.Waiting, v.Target = waiting, botTargetOf(r)
	return v
}

// runLiveView is run id's notes and the time now.
func (s *Service) runLiveView(id string) assistantwire.RunProgress {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.live[id].progress(c.clock())
}

// progress is p's notes as the progress card shows them (a copy) at time now; p may be nil: a run with no live state.
func (p *chanLive) progress(now time.Time) assistantwire.RunProgress {
	v := assistantwire.RunProgress{Now: now}
	if p == nil {
		return v
	}
	for _, o := range p.open {
		v.Open = append(v.Open, assistantwire.RunOpenCall{Step: o.step, Since: o.since})
	}
	for _, st := range p.subs {
		v.Subs = append(v.Subs, assistantwire.RunSubtask{Desc: st.desc, Kind: st.kind, Done: st.done})
	}
	v.Calls, v.Read, v.Edited, v.SubsMore = p.calls, len(p.read), len(p.edited), p.subsMore
	return v
}

// runWaiting says why a run's message is not found in its conversation yet: queued behind a turn that works
// ("queued"), or not in yet ("preparing"); "" once it is.
func (s *Service) runWaiting(r *channel.Run) string {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	switch p := c.live[r.ID]; {
	case p == nil || p.from != 0:
		return ""
	case r.Seen:
		return "queued"
	}
	return "preparing"
}
