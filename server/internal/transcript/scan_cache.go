package transcript

import (
	"bytes"
	"hash/crc32"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// A File read cold scans its whole file (scanLines) before it parses the window at the end. A node reads a big
// transcript cold again whenever it no longer holds the File it had read (evicted, forgotten, read again from the start
// after a branch revocation, or opened by another reader): every page read of a ~550 MB transcript scanned all of it.
// What the scan found is kept per path, and a later scan of the same file reads only its last block and what was
// appended since: blocks fall at the same offsets as in a cold scan, so the result is the same as one.

const (
	scanBlock       = 4 << 20
	scanCacheFiles  = 32
	scanCacheIdle   = 10 * time.Minute
	scanCheckWindow = 64 << 10
	scanSample      = 4 << 10
)

// scanKeep is how far past its block a block reads, so a marker split across two blocks is found.
var scanKeep = int64(max(len(seedNotification), len(seedStdout), len(seedHandback)) - 1)

// scanRead counts the bytes scans read (tests).
var scanRead atomic.Int64

type scanHit struct {
	off        int64
	id         string // a notification's task id
	bash       bool
	ultracode  bool
	background bool
}

// scanState is a scan's raw findings up to some block: the line starts (the last one is where the line after the last
// newline starts) and the marker hits, in file order.
type scanState struct {
	starts    []int64
	hits      []scanHit
	first     []byte
	firstDone bool
}

// scanIndex is a scan kept for its file: what blocks before resume found, valid while the file keeps its inode, its
// first line and the bytes just before resume, and grows (or, at the same size, keeps its modification time).
type scanIndex struct {
	ino    uint64
	size   int64
	mtime  time.Time
	resume int64 // blocks before resume read the same bytes at any later size
	check  uint32
	state  scanState // findings of the blocks before resume
}

type scanEntry struct {
	mu   sync.Mutex
	idx  *scanIndex
	used time.Time // guarded by scanCache.mu
}

var scanCache struct {
	mu      sync.Mutex
	entries map[string]*scanEntry
}

// scanEntryFor is the cache entry of path, made if missing; idle entries go, and the least recently used past
// scanCacheFiles.
func scanEntryFor(path string, now time.Time) *scanEntry {
	scanCache.mu.Lock()
	defer scanCache.mu.Unlock()
	if scanCache.entries == nil {
		scanCache.entries = map[string]*scanEntry{}
	}
	for p, e := range scanCache.entries {
		if now.Sub(e.used) > scanCacheIdle {
			delete(scanCache.entries, p)
		}
	}
	e := scanCache.entries[path]
	if e == nil {
		for len(scanCache.entries) >= scanCacheFiles {
			oldest := ""
			for p, c := range scanCache.entries {
				if oldest == "" || c.used.Before(scanCache.entries[oldest].used) {
					oldest = p
				}
			}
			delete(scanCache.entries, oldest)
		}
		e = &scanEntry{}
		scanCache.entries[path] = e
	}
	e.used = now
	return e
}

// scanResume is where a scan of a file of size bytes stops being final: a block before it read block+keep bytes, as
// it does at any larger size.
func scanResume(size int64) int64 {
	if size <= scanKeep {
		return 0
	}
	return (size - scanKeep) / scanBlock * scanBlock
}

// scanLinesCached is scanLines for the file at path (stat'ed as st), from what an earlier scan of it kept.
func scanLinesCached(path string, fh *os.File, st os.FileInfo) (starts []int64, notif []notifSeed, bash, ultracode, background []int, first []byte, lastNL int64, err error) {
	size, ino := st.Size(), inode(st)
	e := scanEntryFor(path, time.Now())
	e.mu.Lock()
	defer e.mu.Unlock()
	var s scanState
	from := int64(0)
	if idx := e.idx; idx != nil && idx.reusable(fh, ino, size, st.ModTime()) {
		s = idx.state.clone()
		from = idx.resume
	}
	if err := scanBlocks(fh, &s, from, size); err != nil {
		e.idx = nil
		return nil, nil, nil, nil, nil, nil, 0, err
	}
	e.idx = nil
	if resume := scanResume(size); s.firstDone && int64(len(s.first)) < resume {
		if check, ok := checksumAt(fh, resume); ok {
			e.idx = &scanIndex{ino: ino, size: size, mtime: st.ModTime(), resume: resume, check: check, state: s.upTo(resume)}
		}
	}
	return scanFinish(fh, s)
}

// reusable: the file is the one scanned, grown or as it was. A same-size file modified since, a smaller one, another
// inode, another first line or other sampled bytes before resume are scanned again from the start.
func (idx *scanIndex) reusable(fh *os.File, ino uint64, size int64, mtime time.Time) bool {
	if ino != idx.ino || size < idx.size || size == idx.size && !mtime.Equal(idx.mtime) {
		return false
	}
	head := make([]byte, len(idx.state.first)+1)
	if _, err := fh.ReadAt(head, 0); err != nil || !bytes.Equal(head[:len(head)-1], idx.state.first) || head[len(head)-1] != '\n' {
		return false
	}
	check, ok := checksumAt(fh, idx.resume)
	return ok && check == idx.check
}

// checksumAt is a checksum of samples of the bytes before off: the end of each block before it, and more just before
// it. A rewrite in place shifts what follows the change, so one that kept the inode and grew is told from an append.
func checksumAt(fh *os.File, off int64) (uint32, bool) {
	h := crc32.NewIEEE()
	buf := make([]byte, scanCheckWindow)
	sample := func(end, n int64) bool {
		from := max(end-n, 0)
		b := buf[:end-from]
		if _, err := fh.ReadAt(b, from); err != nil && err != io.EOF {
			return false
		}
		h.Write(b)
		return true
	}
	for at := int64(scanBlock); at < off; at += scanBlock {
		if !sample(at, scanSample) {
			return 0, false
		}
	}
	if !sample(off, scanCheckWindow) {
		return 0, false
	}
	return h.Sum32(), true
}

func (s scanState) clone() scanState {
	return scanState{starts: append([]int64(nil), s.starts...), hits: append([]scanHit(nil), s.hits...),
		first: append([]byte(nil), s.first...), firstDone: s.firstDone}
}

// upTo is what the blocks before off found: line starts up to off and hits before it (a copy).
func (s scanState) upTo(off int64) scanState {
	n := sort.Search(len(s.starts), func(i int) bool { return s.starts[i] > off })
	h := sort.Search(len(s.hits), func(i int) bool { return s.hits[i].off >= off })
	return scanState{starts: append([]int64(nil), s.starts[:n]...), hits: append([]scanHit(nil), s.hits[:h]...),
		first: append([]byte(nil), s.first...), firstDone: s.firstDone}
}

// scanBlocks scans the blocks from offset from (a block boundary; 0 with an empty state) to size into s.
func scanBlocks(fh *os.File, s *scanState, from, size int64) error {
	buf := make([]byte, scanBlock+scanKeep)
	if len(s.starts) == 0 {
		s.starts = append(s.starts, 0)
	}
	claude := s.firstDone && !isCodexLine(s.first) // the first line is read and is no Codex rollout's
	for off := from; off < size; off += scanBlock {
		n := min(scanBlock+scanKeep, size-off)
		if _, err := fh.ReadAt(buf[:n], off); err != nil && err != io.EOF {
			return err
		}
		scanRead.Add(n)
		data := buf[:n]
		own := min(int64(scanBlock), n) // bytes this block owns; the rest is the next block's start, read for markers
		if !s.firstDone {
			if j := bytes.IndexByte(data[:own], '\n'); j >= 0 {
				s.first, s.firstDone = append(s.first, data[:j]...), true
				claude = !isCodexLine(s.first)
			} else {
				s.first = append(s.first, data[:own]...)
			}
		}
		markers := seedMarkers
		if claude {
			markers = markers[:len(markers)-1] // only a Codex rollout backgrounds commands: a Claude transcript pays nothing
		}
		var hits []scanHit
		for _, m := range markers {
			for i := 0; ; {
				j := bytes.Index(data[i:], m)
				if j < 0 || int64(i+j) >= own {
					break
				}
				h := scanHit{off: off + int64(i+j), bash: bytes.Equal(m, seedBash), ultracode: bytes.Equal(m, seedUltracode) || bytes.Equal(m, seedStdout),
					background: bytes.Equal(m, seedBackgrounded)}
				// A backgrounded command's id, and whether the line is one, are read with the line (codexPendingAt).
				if bytes.Equal(m, seedHandback) {
					near := data[i+j+len(m) : min(len(data), i+j+len(m)+200)]
					if z := bytes.IndexByte(near, '"'); z > 0 {
						h.id = string(near[:z])
					}
				} else if !h.bash && !h.ultracode && !h.background { // the task id follows the marker: "<task-notification>\n<task-id>…</task-id>"
					near := data[i+j : min(len(data), i+j+300)]
					if a := bytes.Index(near, []byte("<task-id>")); a >= 0 {
						if z := bytes.Index(near[a:], []byte("</task-id>")); z > 0 {
							h.id = string(near[a+len("<task-id>") : a+z])
						}
					}
				}
				hits = append(hits, h)
				i += j + 1
			}
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].off < hits[j].off })
		s.hits = append(s.hits, hits...)
		for i := int64(0); i < own; {
			j := bytes.IndexByte(data[i:own], '\n')
			if j < 0 {
				break
			}
			i += int64(j) + 1
			s.starts = append(s.starts, off+i)
		}
	}
	return nil
}

// scanFinish turns a scan's findings into scanLines' results. starts and the line lists are the caller's own.
func scanFinish(fh *os.File, s scanState) (starts []int64, notif []notifSeed, bash, ultracode, background []int, first []byte, lastNL int64, err error) {
	lastNL = s.starts[len(s.starts)-1]
	starts = s.starts[:len(s.starts)-1] // the last entry is where the line still being written (or nothing) starts
	for _, h := range s.hits {
		if h.off >= lastNL {
			break
		}
		line := sort.Search(len(starts), func(i int) bool { return starts[i] > h.off }) // 1-based line holding h
		if h.background {
			if len(background) == 0 || background[len(background)-1] != line {
				background = append(background, line)
			}
		} else if h.ultracode {
			if len(ultracode) == 0 || ultracode[len(ultracode)-1] != line {
				ultracode = append(ultracode, line)
			}
		} else if h.bash {
			if len(bash) == 0 || bash[len(bash)-1] != line {
				bash = append(bash, line)
			}
		} else {
			length := lastNL - starts[line-1]
			if line < len(starts) {
				length = starts[line] - starts[line-1]
			}
			if length > 64<<10 {
				var prefix [1024]byte
				n, _ := fh.ReadAt(prefix[:], starts[line-1])
				if seedRequestTypeRe.Match(prefix[:n]) {
					continue
				}
			}
			notif = append(notif, notifSeed{line, h.id})
		}
	}
	return starts, notif, bash, ultracode, background, s.first, lastNL, nil
}
