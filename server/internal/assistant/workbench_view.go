package assistant

import (
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// What the Lark workbench (the lark module's workbench.go) reads of the assistant: the agents at work and the
// computers, counted for the home and Today cards, and the open Inbox in its order.

// wbCounts is what the home card sums up of the agents and the computers (the module counts the Inbox).
type wbCounts struct {
	working, unread, idle int // agents by state
	pending               int // questions and approvals waiting
	online, machines      int
}

// wbAgent is an agent at work, as the home card lists it.
type wbAgent struct {
	d    Device
	sess proto.Session
}

// wbIdleFor is how recently an idle agent must have moved to count as one of the owner's.
const wbIdleFor = 48 * time.Hour

func (s *Service) workbenchCount(ctx context.Context, now time.Time) (wbCounts, []wbAgent) {
	var c wbCounts
	var busy []wbAgent
	for _, d := range s.devices(ctx) {
		c.machines++
		if d.Online {
			c.online++
		}
		list, _ := s.sessions(ctx, d.ID)
		for _, sess := range list {
			if sess.Assistant || sess.HiddenByDirectory() {
				continue
			}
			switch sess.State {
			case proto.StateNeedsYou, proto.StateWorking:
				c.working++
				busy = append(busy, wbAgent{d, sess})
			case proto.StateUnread:
				c.unread++
				busy = append(busy, wbAgent{d, sess})
			case proto.StateIdle:
				if now.Sub(sess.UpdatedAt) < wbIdleFor {
					c.idle++
				}
			}
		}
	}
	order := map[string]int{proto.StateNeedsYou: 0, proto.StateWorking: 1, proto.StateUnread: 2}
	slices.SortStableFunc(busy, func(a, b wbAgent) int { return order[a.sess.State] - order[b.sess.State] })
	c.pending = len(s.visiblePendingInteractions())
	return c, busy
}

func wbNeedsYou(it InboxItem) bool { return it.Tier == TierAction || it.Urgent }

// workbenchInbox is the open Inbox without its noise, what needs the owner first, newest first within.
func (s *Service) workbenchInbox() []InboxItem {
	var open []InboxItem
	for _, it := range s.inboxList(true) {
		if it.DoneAt == nil && it.Tier != TierNoise {
			open = append(open, it)
		}
	}
	slices.SortStableFunc(open, func(a, b InboxItem) int {
		if na, nb := wbNeedsYou(a), wbNeedsYou(b); na != nb {
			if na {
				return -1
			}
			return 1
		}
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return open
}
