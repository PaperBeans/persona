package diskmap

import (
	"container/list"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// cache keeps measured folders in the node's memory: the most recently used first, at most most of them, each for
// ttl. Nothing is written to disk. A folder in it is never changed in place: an update replaces it with a copy, so a
// caller may keep reading one it got.
type cache struct {
	mu   sync.Mutex
	most int
	ttl  time.Duration
	now  func() time.Time
	ll   *list.List
	byP  map[string]*list.Element
}

type cached struct {
	f  *folder
	at time.Time
}

func newCache(most int, ttl time.Duration, now func() time.Time) *cache {
	return &cache{most: most, ttl: ttl, now: now, ll: list.New(), byP: map[string]*list.Element{}}
}

// get is the folder at path, while it has not expired.
func (c *cache) get(path string) *folder {
	c.mu.Lock()
	defer c.mu.Unlock()
	el := c.byP[path]
	if el == nil {
		return nil
	}
	e := el.Value.(*cached)
	if c.now().Sub(e.at) > c.ttl {
		c.ll.Remove(el)
		delete(c.byP, path)
		return nil
	}
	c.ll.MoveToFront(el)
	return e.f
}

// put keeps f (replacing what was there), dropping the least recently used past most.
func (c *cache) put(f *folder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(f, c.now())
}

func (c *cache) putLocked(f *folder, at time.Time) {
	if el := c.byP[f.path]; el != nil {
		el.Value = &cached{f: f, at: at}
		c.ll.MoveToFront(el)
		return
	}
	c.byP[f.path] = c.ll.PushFront(&cached{f: f, at: at})
	for c.ll.Len() > c.most {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.byP, last.Value.(*cached).f.path)
	}
}

// drop forgets path and every folder under it ("" forgets everything).
func (c *cache) drop(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p, el := range c.byP {
		if path == "" || under(p, path) {
			c.ll.Remove(el)
			delete(c.byP, p)
		}
	}
}

// len is how many folders it holds.
func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// under says whether p is root or inside it.
func under(p, root string) bool {
	if p == root {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(p, strings.TrimSuffix(root, sep)+sep)
}

// removed updates the cached folders after path (bytes on disk) went: it leaves its parent's children and its bytes
// leave every cached folder above it. Its own folder and those under it are forgotten.
func (c *cache) removed(path string, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p, el := range c.byP {
		if under(p, path) {
			c.ll.Remove(el)
			delete(c.byP, p)
		}
	}
	parent := filepath.Dir(path)
	if el := c.byP[parent]; el != nil {
		e := el.Value.(*cached)
		f := e.f.clone()
		if i := slices.IndexFunc(f.children, func(it item) bool { return it.path == path }); i >= 0 {
			if f.children[i].kind != kindMount {
				bytes = f.children[i].bytes
			}
			f.children = slices.Delete(f.children, i, i+1)
		} else if f.otherItems > 0 {
			f.otherBytes = max(f.otherBytes-bytes, 0)
			f.otherItems--
		}
		f.bytes = max(f.bytes-bytes, 0)
		el.Value = &cached{f: f, at: e.at}
	}
	c.shiftLocked(parent, -bytes)
}

// shift adds delta to the folder at path as every cached folder above it counts it (its parent's child, their
// totals), after it was measured again.
func (c *cache) shift(path string, delta int64) {
	if delta == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shiftLocked(path, delta)
}

func (c *cache) shiftLocked(path string, delta int64) {
	for child, dir := path, filepath.Dir(path); dir != child; child, dir = dir, filepath.Dir(dir) {
		el := c.byP[dir]
		if el == nil {
			continue
		}
		e := el.Value.(*cached)
		f := e.f.clone()
		if i := slices.IndexFunc(f.children, func(it item) bool { return it.path == child }); i >= 0 {
			f.children[i].bytes = max(f.children[i].bytes+delta, 0)
			sortItems(f.children)
		} else if f.otherItems > 0 {
			f.otherBytes = max(f.otherBytes+delta, 0)
		}
		f.bytes = max(f.bytes+delta, 0)
		el.Value = &cached{f: f, at: e.at}
	}
}

// clone is a copy of f a change can be made to.
func (f *folder) clone() *folder {
	g := *f
	g.children = slices.Clone(f.children)
	return &g
}

// restored updates the cached folders after path (bytes) came back: its parent is forgotten (it is listed again when
// next opened) and every cached folder above that counts the bytes again.
func (c *cache) restored(path string, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	parent := filepath.Dir(path)
	if el := c.byP[parent]; el != nil {
		c.ll.Remove(el)
		delete(c.byP, parent)
	}
	c.shiftLocked(parent, bytes)
}
