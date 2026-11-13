package assistant

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/proto"
)

// What a conversation writes, for the channels that follow its turn (docs/extensions/channels.md): the manager follows it
// itself (the node sends it the items and the text as they are made, devices.Follow) and hands them to the channels in
// the order the node's events came, with the turns' starts and ends among them — what the hub did when the nodes'
// events were its (assistant_channels.go there). The assistant's conversations' answers that carry a view
// (```persona-view) are handed over too, followed or not.

// fleetQueue runs what the nodes' events start for the assistant one at a time, in the order they came.
type fleetQueue struct {
	once sync.Once
	ch   chan func()
}

// inOrder runs fn after what came before it (on the queue's own goroutine).
func (s *Service) inOrder(fn func()) {
	q := &s.fleet.q
	q.once.Do(func() {
		q.ch = make(chan func(), 4096)
		go func() {
			for f := range q.ch {
				f()
			}
		}()
	})
	q.ch <- fn
}

// follow has the manager follow machine's session (or stop): device.call session.follow / session.unfollow. The mark
// is what the manager wants whatever the devices answer (the items a reconnect's watch brings are for the channels
// then too); one the devices did not take is unsettled, and the next follow of the session asks them again.
func (s *Service) follow(ctx context.Context, machine, session string, on bool) error {
	f := &s.fleet
	f.followMu.Lock()
	k := [2]string{machine, session}
	had, unsettled := f.follows[k], f.unsettled[k]
	if on {
		if f.follows == nil {
			f.follows = map[[2]string]bool{}
		}
		f.follows[k] = true
	} else {
		delete(f.follows, k)
	}
	f.followMu.Unlock()
	if had == on && !unsettled {
		return nil
	}
	err := s.devs.Follow(ctx, machine, session, on)
	f.followMu.Lock()
	if f.follows[k] == on { // a later follow that changed the mark settles it itself
		if err == nil {
			delete(f.unsettled, k)
		} else {
			if f.unsettled == nil {
				f.unsettled = map[[2]string]bool{}
			}
			f.unsettled[k] = true
		}
	}
	f.followMu.Unlock()
	return err
}

func (s *Service) followed(machine, session string) bool {
	f := &s.fleet
	f.followMu.Lock()
	defer f.followMu.Unlock()
	return f.follows[[2]string{machine, session}]
}

// followedItems: new items of a session; to the channels when it is followed (or an assistant conversation's answers
// with a view).
func (s *Service) followedItems(machine string, params json.RawMessage) {
	var p struct {
		SessionID string       `json:"session_id"`
		Items     []proto.Item `json:"items"`
	}
	if json.Unmarshal(params, &p) != nil || p.SessionID == "" || len(p.Items) == 0 {
		return
	}
	items := p.Items
	if !s.followed(machine, p.SessionID) {
		m, ok := s.fleetMachine(machine)
		if !ok {
			return
		}
		conversation, known := s.nodeSession(machine, p.SessionID)
		if !known || !m.isAssistant(conversation) {
			return
		}
		var cards []proto.Item
		for _, it := range items {
			if it.Role == "assistant" && it.Kind == proto.KindText && (it.FullSize > 0 || wire.HasAssistantView(it.Text)) {
				it.Image = nil
				cards = append(cards, it)
			}
		}
		items = cards
		if len(items) == 0 {
			return
		}
	}
	s.inOrder(func() {
		s.SessionItems(context.Background(), SessionItems{MachineID: machine, SessionID: p.SessionID, Items: items})
	})
}

// followedDelta: the text a followed conversation is writing now.
func (s *Service) followedDelta(machine string, params json.RawMessage) {
	var p struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if json.Unmarshal(params, &p) != nil || !s.followed(machine, p.SessionID) {
		return
	}
	s.inOrder(func() {
		s.SessionDelta(context.Background(), SessionDelta{MachineID: machine, SessionID: p.SessionID, Text: p.Text})
	})
}
