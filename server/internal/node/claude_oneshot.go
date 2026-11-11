package node

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/transcript"
)

// A program running Claude Code's command line once (`claude -p`: entrypoint "sdk-cli") writes a transcript like any
// session's; a script doing it all day filled the offline list with them and pushed the user's own sessions out of its 80
// (#3828). The offline list leaves out a transcript that is such a run from its first entrypoint to its last (each read
// within oneShotReach of the file's start and end): a session the user had in a terminal and then went on with through
// `claude -p -c`, or the other way round, is listed. So is everything Persona has a hand in: whatever was started under
// its state directory (the assistant's, any manager's, a health space's, a guest's; its path and its resolved one), a
// session the node keeps state for (none is left out while that state still loads), a fork, a kept session. The Agent
// SDKs' runs ("sdk-ts", "sdk-py") are listed: interactive hosts run the user's sessions through them. A host that runs
// the command line itself writes "sdk-cli" like a script (Paseo does): its sessions are listed by the host while it
// lists them, and the ones it no longer lists (archived there) are the transcripts it names as its own
// (runtimeunlisted.go, #4521), which the offline list keeps.
// Codex's one-shot runs are left out the same way (codex_hierarchy.go programRun).

// Entrypoints are read a tail's worth at a time, up to oneShotReach from either end. The first is looked for from the
// start: a program's first line is its prompt, often longer than a tail (up to 19 KB on one Mac). The last from the end:
// Claude Code writes one on every message line, but after a run's last message it may write more than a tail of lines
// that carry none (api-request-blob, up to 10 KB a line; last-prompt, mode…; 14 KB on one Mac, #4551). Lines without an
// entrypoint are Claude Code's own records: someone going on with the session writes one ("cli") on their first message.
const (
	oneShotTail  = 8 << 10
	oneShotReach = 64 << 10
)

type entrypointMark struct {
	size        int64
	mtime       time.Time
	first, last string
}

var entrypoints = struct {
	sync.Mutex
	byPath map[string]entrypointMark
}{byPath: map[string]entrypointMark{}}

// transcriptEntrypoints are the first and last entrypoints of transcript p ("" where none is within reach; the first is
// read only when the last is a one-shot run's); read again only when the file changed.
func transcriptEntrypoints(p string, st os.FileInfo) (first, last string) {
	entrypoints.Lock()
	m, ok := entrypoints.byPath[p]
	entrypoints.Unlock()
	if ok && m.size == st.Size() && m.mtime.Equal(st.ModTime()) {
		return m.first, m.last
	}
	first, last = readEntrypoints(p, st.Size())
	entrypoints.Lock()
	if len(entrypoints.byPath) > 20000 { // gone transcripts' marks: start over rather than keep them forever
		clear(entrypoints.byPath)
	}
	entrypoints.byPath[p] = entrypointMark{st.Size(), st.ModTime(), first, last}
	entrypoints.Unlock()
	return first, last
}

func readEntrypoints(p string, size int64) (first, last string) {
	f, err := os.Open(p)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	read := func(off, n int64) []byte {
		buf := make([]byte, n)
		got, _ := f.ReadAt(buf, off)
		return buf[:got]
	}
	key := []byte(`"entrypoint":"`)
	value := func(buf []byte, i int) string {
		if i < 0 {
			return ""
		}
		rest := buf[i+len(key):]
		if j := bytes.IndexByte(rest, '"'); j >= 0 {
			return string(rest[:j])
		}
		return ""
	}
	var tail []byte
	i := -1
	for back := int64(0); i < 0 && back < min(size, oneShotReach); back += oneShotTail {
		off := max(size-back-oneShotTail, 0)
		tail = append(read(off, size-back-off), tail...) // a key cut at the chunk's start is whole once the next is read
		i = bytes.LastIndex(tail, key)
	}
	last = value(tail, i)
	if !transcript.ClaudeOneShot(last) {
		return "", last // only a one-shot run's start matters
	}
	var head []byte
	for off := int64(0); off < min(size, oneShotReach); off += oneShotTail {
		head = append(head, read(off, min(oneShotTail, size-off))...)
		if i := bytes.Index(head, key); i >= 0 && bytes.IndexByte(head[i+len(key):], '"') >= 0 {
			return value(head, i), last
		}
	}
	return "", last
}

// personaProjectDirs are the names Claude Code gives the projects of sessions started under Persona's state directory —
// its path, and its resolved path when links lead there, with every character but letters and digits as "-" — each
// followed by "-": a prefix of any directory under it.
func (n *Node) personaProjectDirs() []string {
	var out []string
	for _, dir := range []string{identity.DataDir(n.home), filepath.Join(n.home, ".persona"), filepath.Join(n.home, compat.StateDir)} {
		for _, p := range []string{dir, realPath(dir)} {
			if name := nonAlnum.ReplaceAllString(p, "-") + "-"; !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// oneShotRun: the transcript at p (in project directory base) is a program's one-shot run of Claude Code that the offline
// list leaves out (see above).
func (n *Node) oneShotRun(id, base, p string, st os.FileInfo, persona []string) bool {
	for _, prefix := range persona {
		if strings.HasPrefix(base, prefix) {
			return false // started under Persona's state directory
		}
	}
	if n.state != nil {
		if n.state.isLoading() {
			return false // which sessions the node keeps state for is not known yet
		}
		if _, ok := n.state.get(id); ok {
			return false // the node started or drove it
		}
	}
	first, last := transcriptEntrypoints(p, st)
	return transcript.ClaudeOneShot(first) && transcript.ClaudeOneShot(last)
}
