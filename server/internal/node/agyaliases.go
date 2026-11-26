package node

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// An agy conversation is listed under one of two ids, and which one changes over its life (#14395). While a live pane
// runs it and the pane's conversation is known (agyConversation), the row is the pane's, agy-<workspace>-<pane>
// (otherSession), and the Antigravity companion's own row for it, agy-<conversation>, is left out (sessionListIn). With
// no such pane (it closed, agy quit in it, the engine gave the conversation another pane, the pane's report is in
// doubt) the companion's row is listed instead. An Agent keeps the id it took from a quote or from a2a_sessions, so the
// row listing a conversation names the conversation's other ids in its aliases (proto.Session.Aliases), and the manager
// finds an A2A target by them (assistant/a2a_resolve.go): a pane's row names agy-<conversation>, and whichever row lists
// a conversation names the pane rows that showed it before and are not listed now, the most recent first. Which
// conversation those panes showed is kept in memory, for the agyShownMost panes listed last: after a restart the node
// knows only the panes it has listed since. A pane that runs another conversation now names that one: its id is its
// row's, never an alias.

const (
	agyShownMost   = 256 // pane rows remembered with the conversation they showed (Node.agyShown)
	agyAliasesMost = 8   // ids one row names
)

// agyShown is the conversation an agy pane's row showed when it was last listed, and when that was.
type agyShown struct {
	conversation string
	at           time.Time
}

// nameAgyAliases names on list's rows the other ids their agy conversations go by (above). panes is the conversation
// each agy pane's row showed in this pass, by row id, whether or not the row is in list.
func (n *Node) nameAgyAliases(list []proto.Session, panes map[string]string) {
	earlier := n.rememberAgyPanes(panes, time.Now())
	if len(panes) == 0 && len(earlier) == 0 {
		return
	}
	listed := map[string]int{}
	for i := range list {
		if id := list[i].SessionID; strings.HasPrefix(id, agyAgent+"-") {
			listed[id] = i
		}
	}
	paneOf := make(map[string]string, len(panes)) // conversation -> the pane row showing it ("" when two do)
	for id, conversation := range panes {
		if _, two := paneOf[conversation]; two {
			id = ""
		}
		paneOf[conversation] = id
		if _, ok := earlier[conversation]; !ok {
			earlier[conversation] = nil
		}
	}
	for conversation, before := range earlier {
		own := agyAgent + "-" + conversation
		row, ownListed := listed[own] // the companion's row when it is listed, else the pane's
		if !ownListed {
			var ok bool
			if row, ok = listed[paneOf[conversation]]; !ok {
				continue // the conversation is not listed
			}
		}
		var names []string
		if !ownListed {
			names = append(names, own)
		}
		for _, id := range before {
			if _, ok := listed[id]; !ok && len(names) < agyAliasesMost {
				names = append(names, id)
			}
		}
		if len(names) == 0 {
			continue
		}
		s := &list[row]
		have := s.Aliases()
		all := slices.Clip(have) // shared with the row's other copies: appended to, never written
		for _, id := range names {
			if !slices.Contains(have, id) {
				all = append(all, id)
			}
		}
		s.SetAliases(all)
	}
}

// rememberAgyPanes keeps, as of now, the conversation each pane row in panes shows, and answers the rows remembered that
// are not among them, by the conversation they showed last, the most recently listed first (by id when listed together).
// Not nil when panes has a row, so the caller can add to it.
func (n *Node) rememberAgyPanes(panes map[string]string, now time.Time) map[string][]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(panes) == 0 && len(n.agyShown) == 0 {
		return nil
	}
	if n.agyShown == nil {
		n.agyShown = make(map[string]agyShown, len(panes))
	}
	for id, conversation := range panes {
		n.agyShown[id] = agyShown{conversation: conversation, at: now}
	}
	newest := func(a, b string) int {
		return cmp.Or(n.agyShown[b].at.Compare(n.agyShown[a].at), strings.Compare(a, b))
	}
	if over := len(n.agyShown) - agyShownMost; over > 0 {
		ids := slices.SortedFunc(maps.Keys(n.agyShown), newest)
		for _, id := range ids[len(ids)-over:] {
			delete(n.agyShown, id)
		}
	}
	earlier := map[string][]string{}
	for id, e := range n.agyShown {
		if _, shown := panes[id]; !shown {
			earlier[e.conversation] = append(earlier[e.conversation], id)
		}
	}
	for _, ids := range earlier {
		slices.SortFunc(ids, newest)
	}
	return earlier
}
