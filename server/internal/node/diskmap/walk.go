package diskmap

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// fileID tells files apart across one filesystem.
type fileID struct{ dev, ino uint64 }

// fileStat is what the walker needs of an entry (statOf, per OS).
type fileStat struct {
	id      fileID
	dev     uint64
	links   uint64
	dir     bool
	link    bool // a symbolic link or (Windows) a reparse point: never followed
	regular bool
	bytes   int64 // allocated on disk (Windows: logical), which is what frees when it goes
	mtime   int64 // Unix seconds
}

// volume is one filesystem's numbers.
type volume struct{ total, free, used int64 }

// mostTracked is the most children one tracked folder keeps apart while walking; the rest are summed into one
// overflow box, so a folder of a million files does not hold a million records.
const mostTracked = 20000

// acc is one box being measured: a folder or file whose bytes and newest change the walk adds up as it goes. The walk
// keeps a box for each root, each root's children and each child's children (the prefetch), and one level more under
// the folders a job asks for (deep).
type acc struct {
	name, path string
	parent     *acc
	depth      int
	dir        bool
	mount      *volume // another filesystem's root: its statfs numbers, never walked
	track      bool    // its children get boxes of their own
	keepAll    bool    // its folder keeps every child, not only the largest

	bytes   atomic.Int64
	usedAt  atomic.Int64
	pending atomic.Int64 // folders under it still to be listed
	cut     atomic.Bool  // the walk under it stopped before it was done
	listed  atomic.Bool  // its own listing is done (its children are known)

	mu       sync.Mutex
	kids     []*acc
	overflow *acc // the children past mostTracked
	extra    int  // how many children overflow sums
}

// add counts bytes and a change time into a and every box above it.
func (a *acc) add(bytes, mtime int64) {
	for x := a; x != nil; x = x.parent {
		if bytes != 0 {
			x.bytes.Add(bytes)
		}
		for {
			old := x.usedAt.Load()
			if mtime <= old || x.usedAt.CompareAndSwap(old, mtime) {
				break
			}
		}
	}
}

// open counts a folder to list under a (n = 1) or one listed (n = -1), in a and every box above it.
func (a *acc) open(n int64) {
	for x := a; x != nil; x = x.parent {
		x.pending.Add(n)
	}
}

// markCut says the walk under a stopped early, in a and every box above it.
func (a *acc) markCut() {
	for x := a; x != nil; x = x.parent {
		x.cut.Store(true)
	}
}

// child makes a's box for one of its entries, or the overflow box once a has mostTracked.
func (a *acc) child(name, path string, dir bool, j *job) *acc {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.kids) >= mostTracked {
		if a.overflow == nil {
			a.overflow = &acc{parent: a, depth: a.depth + 1}
		}
		a.extra++
		return a.overflow
	}
	k := &acc{name: name, path: path, parent: a, depth: a.depth + 1, dir: dir}
	if dir {
		k.track = k.depth < 2 || j.deep[path]
		k.keepAll = j.deep[path]
	}
	a.kids = append(a.kids, k)
	return k
}

// job is one scan: a set of roots walked by one pool of walkers within one deadline and one entry budget. Callers for
// the same key join it; it keeps going when they stop waiting.
type job struct {
	key      string // "" for the overview, else the folder
	roots    []*acc
	deep     map[string]bool
	deadline time.Duration
	budget   int64
	walkers  int

	started atomic.Int64 // Unix nanoseconds, 0 while it waits for a scan slot
	ended   atomic.Int64 // Unix nanoseconds, 0 while it runs
	done    chan struct{}
	entries atomic.Int64

	seenMu sync.Mutex
	seen   map[fileID]bool // files with more than one link, already counted

	qmu    sync.Mutex
	qcond  *sync.Cond
	queue  []work // deeper folders, walked depth first
	front  []work // the folders whose children get boxes, listed first and in order, so every box shows early
	active int
	stop   bool
}

// work is one folder to list: dir, inside the box at (whose own folder it is when own), on device dev.
type work struct {
	dir string
	at  *acc
	own bool
	dev uint64
}

func newJob(key string, roots []string, deep map[string]bool, deadline time.Duration, budget int64, walkers int) *job {
	j := &job{key: key, deep: deep, deadline: deadline, budget: budget, walkers: walkers, done: make(chan struct{}),
		seen: map[fileID]bool{}}
	j.qcond = sync.NewCond(&j.qmu)
	for _, r := range roots {
		j.roots = append(j.roots, &acc{name: filepath.Base(r), path: r, dir: true, track: true, keepAll: deep[r]})
	}
	return j
}

// run walks every root until done, the deadline or the budget; gate holds one of the node's scan slots meanwhile.
func (j *job) run(gate chan struct{}) {
	defer func() {
		j.ended.Store(time.Now().UnixNano())
		close(j.done)
	}()
	gate <- struct{}{}
	defer func() { <-gate }()
	j.started.Store(time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), j.deadline)
	defer cancel()
	for _, r := range j.roots {
		info, err := os.Lstat(r.path)
		if err != nil || !info.IsDir() {
			r.listed.Store(true)
			continue
		}
		st, ok := statOf(info)
		if !ok || st.link {
			r.listed.Store(true)
			continue
		}
		r.add(j.count(st), st.mtime)
		r.open(1)
		j.front = append(j.front, work{dir: r.path, at: r, own: true, dev: st.dev})
	}
	var wg sync.WaitGroup
	for range j.walkers {
		wg.Go(func() { j.walk(ctx) })
	}
	stopped := context.AfterFunc(ctx, func() {
		j.qmu.Lock()
		j.stop = true
		j.qcond.Broadcast()
		j.qmu.Unlock()
	})
	wg.Wait()
	stopped()
	// What the deadline or the budget left unlisted makes its boxes partial.
	for _, w := range append(j.front, j.queue...) {
		w.at.markCut()
		w.at.open(-1)
	}
	j.front, j.queue = nil, nil
}

// walk lists folders from the queue until it is empty and no walker can add more, or the job stops.
func (j *job) walk(ctx context.Context) {
	for {
		j.qmu.Lock()
		for len(j.front)+len(j.queue) == 0 && j.active > 0 && !j.stop {
			j.qcond.Wait()
		}
		if j.stop || len(j.front)+len(j.queue) == 0 {
			j.qmu.Unlock()
			j.qcond.Broadcast()
			return
		}
		var w work
		if len(j.front) > 0 {
			w, j.front = j.front[0], j.front[1:]
		} else {
			w = j.queue[len(j.queue)-1]
			j.queue = j.queue[:len(j.queue)-1]
		}
		j.active++
		j.qmu.Unlock()

		more := j.list(ctx, w)

		j.qmu.Lock()
		w.at.open(-1)
		if w.own {
			w.at.listed.Store(true)
		}
		j.active--
		for _, m := range more {
			if m.own && m.at.track {
				j.front = append(j.front, m)
			} else {
				j.queue = append(j.queue, m)
			}
		}
		j.qcond.Broadcast()
		j.qmu.Unlock()
	}
}

// list reads one folder and counts what is in it; it answers the folders found in it, to list next.
func (j *job) list(ctx context.Context, w work) []work {
	if ctx.Err() != nil || j.entries.Load() >= j.budget {
		w.at.markCut()
		j.halt()
		return nil
	}
	entries, err := readDir(w.dir)
	if err != nil {
		return nil // a folder the user cannot read is not theirs to clean up
	}
	if j.entries.Add(int64(len(entries))) > j.budget {
		w.at.markCut()
		j.halt()
	}
	var more []work
	tracking := w.own && w.at.track
	for _, e := range entries {
		info, err := e.Info() // lstat: a link is never followed
		if err != nil {
			continue
		}
		st, ok := statOf(info)
		if !ok || st.link || !st.dir && !st.regular {
			continue // a link is never followed; a socket, a pipe or a device holds nothing
		}
		path := filepath.Join(w.dir, e.Name())
		if st.dir && st.dev != w.dev {
			// Another filesystem (a disk or share mounted here): named with its own numbers, never walked.
			if tracking {
				k := w.at.child(e.Name(), path, true, j)
				if v, err := volumeAt(path); err == nil {
					k.mount = &v
				} else {
					k.mount = &volume{}
				}
				k.usedAt.Store(st.mtime)
				k.listed.Store(true)
			}
			continue
		}
		at := w.at
		if tracking {
			at = w.at.child(e.Name(), path, st.dir, j)
		}
		at.add(j.count(st), st.mtime)
		if st.dir {
			at.open(1)
			more = append(more, work{dir: path, at: at, own: tracking && at.path == path, dev: w.dev})
		}
	}
	return more
}

// halt stops every walker (the budget is spent).
func (j *job) halt() {
	j.qmu.Lock()
	j.stop = true
	j.qcond.Broadcast()
	j.qmu.Unlock()
}

// count is a file's bytes, or 0 for a hard link to a file this job already counted.
func (j *job) count(st fileStat) int64 {
	if st.links > 1 && !st.dir {
		j.seenMu.Lock()
		defer j.seenMu.Unlock()
		if j.seen[st.id] {
			return 0
		}
		j.seen[st.id] = true
	}
	return st.bytes
}

// times are when the job started and how long it has run (or ran).
func (j *job) times() (time.Time, time.Duration) {
	s := j.started.Load()
	if s == 0 {
		return time.Now(), 0
	}
	start := time.Unix(0, s)
	if e := j.ended.Load(); e > 0 {
		return start, time.Unix(0, e).Sub(start)
	}
	return start, time.Since(start)
}

// finished says whether the job is done.
func (j *job) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// readDir lists a folder in the order the filesystem gives (os.ReadDir would sort every one).
func readDir(dir string) ([]os.DirEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// item is one child of a folder as the map knows it.
type item struct {
	name, path string
	kind       string // kindDir, kindFile, kindMount
	bytes      int64
	usedAt     int64
	partial    bool
	vol        *volume
}

const (
	kindDir   = "dir"
	kindFile  = "file"
	kindMount = "mount"
)

// folder is what is known of one folder: its bytes and its children's, largest first. Children past what it keeps
// are summed in other.
type folder struct {
	path        string
	bytes       int64
	usedAt      int64
	partial     bool
	listed      bool // its children are all known (false while its listing runs)
	children    []item
	otherBytes  int64
	otherItems  int
	otherUsedAt int64
	scannedAt   time.Time
	duration    time.Duration
}

// keepDefault is how many children a folder record keeps; a deep one keeps them all (the app aggregation reads them).
const keepDefault = 64

// view is a's folder now: the bytes counted so far and the children known so far.
func (a *acc) view() *folder {
	f := &folder{path: a.path, bytes: a.bytes.Load(), usedAt: a.usedAt.Load(), partial: a.cut.Load(), listed: a.listed.Load()}
	a.mu.Lock()
	kids := slices.Clone(a.kids)
	over, extra := a.overflow, a.extra
	a.mu.Unlock()
	for _, k := range kids {
		it := item{name: k.name, path: k.path, kind: kindFile, bytes: k.bytes.Load(), usedAt: k.usedAt.Load(), partial: k.cut.Load()}
		if k.dir {
			it.kind = kindDir
		}
		if k.mount != nil {
			it.kind, it.vol, it.bytes = kindMount, k.mount, k.mount.used
		}
		f.children = append(f.children, it)
	}
	sortItems(f.children)
	if over != nil {
		f.otherBytes, f.otherItems, f.otherUsedAt = over.bytes.Load(), extra, over.usedAt.Load()
	}
	if !a.keepAll {
		f.trim(keepDefault)
	}
	return f
}

// trim keeps the n largest children and sums the rest into other.
func (f *folder) trim(n int) {
	if len(f.children) <= n {
		return
	}
	for _, it := range f.children[n:] {
		f.otherBytes += it.bytes
		f.otherItems++
		f.otherUsedAt = max(f.otherUsedAt, it.usedAt)
	}
	f.children = slices.Clip(f.children[:n])
}

func sortItems(items []item) {
	slices.SortFunc(items, func(a, b item) int { return cmp.Or(cmp.Compare(b.bytes, a.bytes), cmp.Compare(a.name, b.name)) })
}

// views are the folders of every box the job keeps children for, by path, as they are now.
func (j *job) views() map[string]*folder {
	out := map[string]*folder{}
	var visit func(a *acc)
	visit = func(a *acc) {
		if !a.track || a.mount != nil {
			return
		}
		f := a.view()
		f.scannedAt, f.duration = j.times()
		out[a.path] = f
		a.mu.Lock()
		kids := slices.Clone(a.kids)
		a.mu.Unlock()
		for _, k := range kids {
			visit(k)
		}
	}
	for _, r := range j.roots {
		visit(r)
	}
	return out
}
