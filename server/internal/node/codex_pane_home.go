package node

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/host"
)

// Herdr names the raw thread. A running process identifies its home; without one
// use a unique indexed rollout, or verify a legacy rollout before claiming it.
func (n *Node) codexPaneOwner(a host.Agent, reported, bound string) (home string, verified bool) {
	r := n.codices
	if r == nil {
		return n.codexHome(), true
	}
	if kind := host.ParsePane(a.PaneID).Kind; kind != host.Herdr && kind != host.HerdrCore {
		if reported == "" && bound != "" {
			if s, ok := n.codexSource(bound); ok && s.home != "" {
				return s.home, true
			}
		}
		return n.codexPaneHome(a.PaneID), true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if home = r.byPane[a.PaneID]; home != "" {
		return home, true
	}
	if reported == "" && bound != "" {
		if s, ok := r.threads[bound]; ok && (s.path != "" || bound != s.thread) {
			return s.home, true
		}
	}
	if reported != "" {
		for _, s := range r.threads {
			if s.thread != reported || s.path == "" {
				continue
			}
			if home != "" && home != s.home {
				return "", false
			}
			home = s.home
		}
		if home != "" {
			return home, false // revalidate the indexed file before binding this live pane
		}
	}
	if reported == "" && len(r.config.Homes) > 1 {
		if _, _, named := codexTitleName(a.Title); named {
			return "", false // the same title may be present in more than one home
		}
	}
	return n.codexHome(), false
}

type codexPaneQuery struct {
	home, reported, bound, title, taken string
	waiting, verified, moved            bool
}

type codexPaneMatch struct {
	id, named, titled string
}

type codexPaneRead struct {
	query   codexPaneQuery
	started time.Time
	done    chan struct{}
	result  codexPaneMatch
}

type codexPaneReads struct {
	sync.Mutex
	pending map[string]*codexPaneRead
}

// Reserve work for live panes even when every history-index worker is already in a slow filesystem.
var codexPaneReadSlots = make(chan struct{}, 8)

// A timed-out observation is consumed by the next refresh, or retired when
// the pane disappears. An unfinished syscall continues to count against the global limit.
func (p *codexPaneReads) retain(panes []host.Agent) {
	p.Lock()
	defer p.Unlock()
	for id := range p.pending {
		if !slices.ContainsFunc(panes, func(a host.Agent) bool { return a.PaneID == id }) {
			select {
			case <-p.pending[id].done:
				delete(p.pending, id)
			default:
			}
		}
	}
}

// Even an old/archived thread can be the current pane. Its history and title lookups
// can block in the filesystem. Share one outstanding lookup per pane, limit workers
// across nodes, and never reuse another pane identity's result after a timeout.
func (n *Node) codexPaneIdentity(a host.Agent, bound string, waiting bool, taken map[string]host.Agent, deadline time.Time) (codexPaneMatch, bool) {
	q := codexPaneQuery{reported: a.SessionID(), bound: bound, title: a.Title, waiting: waiting}
	q.home, q.verified = n.codexPaneOwner(a, q.reported, bound)
	if q.home == "" {
		return codexPaneMatch{}, false
	}
	if q.reported == "" && bound != "" {
		if n.state != nil {
			if st, ok := n.state.get(bound); ok {
				q.moved = st.Pane != "" && st.Pane != a.PaneID
			}
		}
		if source, ok := n.codexSource(bound); ok && source.home == q.home {
			q.bound = source.thread // a pane binding is already a public ID
		} else if n.codices != nil && strings.HasPrefix(bound, "c0de-") {
			return codexPaneMatch{}, false
		}
	}
	if _, _, ok := codexTitleName(a.Title); ok {
		q.taken = strings.Join(slices.Sorted(maps.Keys(taken)), "\x00")
	}
	if n.codices == nil {
		return n.readCodexPane(q, a, taken), true
	}
	p := &n.codices.paneReads
	p.Lock()
	if p.pending == nil {
		p.pending = make(map[string]*codexPaneRead)
	}
	read := p.pending[a.PaneID]
	if read != nil {
		select {
		case <-read.done:
			delete(p.pending, a.PaneID)
			if read.query == q {
				p.Unlock()
				return read.result, true
			}
			read = nil
		default:
			if read.query != q {
				p.Unlock()
				return codexPaneMatch{}, false
			}
		}
	}
	if read == nil {
		select {
		case codexPaneReadSlots <- struct{}{}:
		default:
			p.Unlock()
			return codexPaneMatch{}, false
		}
		read = &codexPaneRead{query: q, started: time.Now(), done: make(chan struct{})}
		p.pending[a.PaneID] = read
		var used map[string]host.Agent
		if q.taken != "" {
			used = maps.Clone(taken) // the host merge continues after its wait expires
		}
		go func() {
			defer func() { <-codexPaneReadSlots }()
			read.result = n.readCodexPane(q, a, used)
			close(read.done)
		}()
	}
	p.Unlock()
	if !waitCodexScan(read.done, read.started, deadline) {
		return codexPaneMatch{}, false
	}
	p.Lock()
	if p.pending[a.PaneID] == read {
		delete(p.pending, a.PaneID)
	}
	p.Unlock()
	return read.result, true
}

func (n *Node) readCodexPane(q codexPaneQuery, a host.Agent, taken map[string]host.Agent) codexPaneMatch {
	id := q.reported
	var path string
	if id != "" {
		id, path = mainThreadRollout(q.home, id)
		if !q.verified && path == "" {
			return codexPaneMatch{}
		}
	}
	if q.waiting {
		m := codexPaneMatch{titled: n.codexTitleThread(q.home, a)}
		if id != "" {
			m.named = n.codexPublicID(q.home, id)
		}
		return m
	}
	sub := id != q.reported
	if id == "" {
		id = q.bound
	}
	if named, claimed := n.codexByTitle(q.home, a, id, taken); named != "" {
		if id != named {
			path = ""
		}
		id = named
	} else if claimed {
		return codexPaneMatch{}
	} else if _, match, ok := codexTitleName(a.Title); ok {
		if q.moved || (!sub && id != "" && threadTitle(q.home, id) != "" && !threadFormerlyNamed(q.home, id, match)) {
			return codexPaneMatch{}
		}
	}
	if id == "" {
		return codexPaneMatch{}
	}
	if path != "" && n.codices != nil {
		return codexPaneMatch{id: n.registerCodex(q.home, id, path)}
	}
	return codexPaneMatch{id: n.codexPublicID(q.home, id)}
}
