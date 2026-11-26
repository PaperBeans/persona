package node

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A discovered home can be on an offline mount or waiting for a folder permission.
// The OS may leave its open pending indefinitely. Keep one scan per home and bound
// the number of blocked filesystem workers across connections.
var codexHomeScanSlots = make(chan struct{}, 16)

const (
	codexHomeScanWait = 100 * time.Millisecond
	codexHomeListWait = 300 * time.Millisecond
)

type codexHomeRun struct {
	done    chan struct{}
	started time.Time
	ok      bool
}

type codexHomeScan struct {
	run   *codexHomeRun
	fresh time.Time
	ok    bool
}

type codexHomeIndex struct {
	mu       sync.Mutex
	homes    map[string]*codexHomeScan
	scanWait time.Duration // 0 uses codexHomeScanWait
	listWait time.Duration // 0 uses codexHomeListWait
}

func (i *codexHomeIndex) listBudget() time.Duration {
	if i == nil {
		return codexHomeListWait
	}
	i.mu.Lock()
	d := i.listWait
	i.mu.Unlock()
	if d > 0 {
		return d
	}
	return codexHomeListWait
}

func (n *Node) codexHomeListBudget() time.Duration {
	if n == nil || n.codices == nil {
		return codexHomeListWait
	}
	return n.codices.index.listBudget()
}

// indexCodexHome registers both the rollouts and the App's sidebar threads (including
// threads without a rollout). Neither file source can hold up a different home.
func (n *Node) indexCodexHome(home string, deadline time.Time) bool {
	return n.codices.index.available(home, deadline, func() (time.Duration, bool) {
		st, err := os.Stat(home)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Not created yet (e.g. before first Codex use): recheck quickly so new history appears promptly.
				return time.Second, true
			}
			// Propagate permission, mount, or I/O failures as unavailable without caching a fresh failure.
			return 0, false
		}
		if !st.IsDir() {
			return 0, false
		}
		n.registerRollouts(home)
		n.registerCodexAppThreads(home, nil)
		// Warm session_index.jsonl inside the bounded worker so a stalled title read cannot stall session lists.
		threadTitle(home, "")
		return time.Second, true
	})
}

// codexHomeOf returns the registered home for id, falling back to the node's default Codex home.
func (n *Node) codexHomeOf(id string) string {
	if src, ok := n.codexSource(id); ok && src.home != "" {
		return src.home
	}
	return n.codexHome()
}

// A live pane is decorated before the history rows, sometimes between host refreshes.
// Do not re-enter a filesystem scan the index has already timed out or another reader holds.
// Downstream reads in the list pass still use nonblocking TryLock so a mutex acquired after
// this check can never stall the list across homes.
func (n *Node) liveCodexHomeReady(id string, deadline time.Time, pass *codexListPass) bool {
	src, ok := n.codexSource(id)
	if !ok {
		// A pane can already have its own transcript before its home is in
		// the Codex registry. The normal transcript lookup revalidates that
		// path; it needs no inferred home or new public identity here.
		n.mu.Lock()
		path := n.paths[id]
		n.mu.Unlock()
		return path != ""
	}
	if pass.homeUnavailable(src.home) {
		return false
	}
	c := rolloutCacheFor(src.home)
	if !c.mu.TryLock() {
		pass.markHomeUnavailable(src.home)
		return false
	}
	fresh := src.path != "" && time.Since(c.at) < rolloutScanInterval
	c.mu.Unlock()
	if fresh {
		return true // the pane lookup just read this home; unrelated history work cannot take its slot
	}
	if !n.indexCodexHome(src.home, deadline) || !c.mu.TryLock() {
		pass.markHomeUnavailable(src.home)
		return false
	}
	c.mu.Unlock()
	return true
}

// firstMessageRollouts reads the completed index. A start polls until its first
// message appears, so it can pass a busy home and check again on the next look.
func (n *Node) firstMessageRollouts(home string, deadline time.Time) []codexRollout {
	if n.codices == nil {
		return rollouts(home)
	}
	c := rolloutCacheFor(home)
	if !c.mu.TryLock() {
		return nil
	}
	if time.Since(c.at) < rolloutScanInterval {
		list := c.rollouts
		c.mu.Unlock()
		return list
	}
	c.mu.Unlock()
	// The rollout cache may have been invalidated since the last catalog check.
	// Reusing an older, incomplete list could turn two candidate threads into one.
	i := &n.codices.index
	i.mu.Lock()
	if s := i.homes[home]; s != nil && s.run == nil {
		s.fresh = time.Time{}
	}
	i.mu.Unlock()
	if !n.indexCodexHome(home, deadline) {
		return nil
	}
	if !c.mu.TryLock() {
		return nil
	}
	defer c.mu.Unlock()
	return c.rollouts
}

// available waits only for the first short look. Once a scan overruns its budget,
// later lists pass it immediately. A completed scan is used by the next list.
func (i *codexHomeIndex) available(home string, deadline time.Time, scan func() (time.Duration, bool)) bool {
	i.mu.Lock()
	if i.homes == nil {
		i.homes = make(map[string]*codexHomeScan)
	}
	s := i.homes[home]
	if s == nil {
		s = &codexHomeScan{}
		i.homes[home] = s
	}
	now := time.Now()
	if now.Before(s.fresh) {
		ok := s.ok
		i.mu.Unlock()
		return ok
	}
	if s.run == nil {
		select {
		case codexHomeScanSlots <- struct{}{}:
		default:
			i.mu.Unlock()
			return false
		}
		run := &codexHomeRun{done: make(chan struct{}), started: now}
		s.run = run
		go func() {
			defer func() { <-codexHomeScanSlots }()
			freshFor, ok := scan()
			run.ok = ok
			i.mu.Lock()
			if ok && freshFor > 0 {
				s.fresh = time.Now().Add(freshFor)
			} else {
				// Never cache failed scans as fresh: a transient error must recover on the next bounded scan.
				s.fresh = time.Time{}
			}
			s.ok = ok
			if s.run == run {
				s.run = nil
			}
			close(run.done)
			i.mu.Unlock()
		}()
	}
	run := s.run
	scanWait := i.scanWait
	i.mu.Unlock()
	if scanWait <= 0 {
		scanWait = codexHomeScanWait
	}
	if !waitCodexScanWithin(run.done, run.started, deadline, scanWait) {
		return false
	}
	return run.ok
}

func waitCodexScan(done <-chan struct{}, started, deadline time.Time) bool {
	return waitCodexScanWithin(done, started, deadline, codexHomeScanWait)
}

func waitCodexScanWithin(done <-chan struct{}, started, deadline time.Time, scanWait time.Duration) bool {
	now := time.Now()
	wait := min(scanWait-now.Sub(started), deadline.Sub(now))
	if wait <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// A home which is slow to scan retains the identity and context the node last confirmed.
// The current process, permissions and actions are unknown until the next successful read.
func unavailableCodexRow(s proto.Session) proto.Session {
	return proto.Session{
		SessionID: s.SessionID, Agent: s.Agent, AgentName: s.AgentName, AgentSurface: s.AgentSurface,
		Title: s.Title, Cwd: s.Cwd, WorkspaceCwd: s.WorkspaceCwd, RepoRoot: s.RepoRoot, Worktree: s.Worktree,
		Branch: s.Branch, Model: s.Model, Effort: s.Effort, UpdatedAt: s.UpdatedAt,
		SourceKind: s.SourceKind, SourceInstanceID: s.SourceInstanceID, SourceThreadID: s.SourceThreadID,
		ParentThreadID: s.ParentThreadID, ParentSessionID: s.ParentSessionID, RootSessionID: s.RootSessionID,
		IsSubagent: s.IsSubagent, Status: proto.StatusUnknown, Capabilities: &proto.SessionCapabilities{},
	}
}
