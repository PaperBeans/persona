package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Claude's desktop app keeps an index of its Claude Code sessions, one JSON per session:
// ~/Library/Application Support/Claude/claude-code-sessions/<…>/<…>/local_<id>.json (Claude-3p for the build on a
// third-party provider). Each names the transcript the session runs (cliSessionId), whether the session is archived
// (isArchived), and the transcripts it ran before (priorCliSessionIds: it went on in a new one). Its sidebar lists each
// unarchived session once (a side session nested under its parent). The offline list follows it (#3304): it leaves out
// the transcripts of archived sessions, and a session's earlier transcripts once the one it runs now is listed. Only what
// an entry says is acted on: a transcript no entry names, or an entry that cannot be read, hides nothing; at worst a
// session shows twice, never not at all. Only these files are read, never the app's config file (it holds credentials).

// claudeDesktopDirs are the desktop apps' data directories under home (a variable: tests use their own). Only macOS's
// layout is known.
var claudeDesktopDirs = func(home string) []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	base := filepath.Join(home, "Library", "Application Support")
	return []string{filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p")}
}

type claudeDesktopEntry struct {
	transcript string   // cliSessionId
	archived   bool     // isArchived
	prior      []string // priorCliSessionIds
}

type claudeDesktopFile struct {
	mtime time.Time
	size  int64
	entry claudeDesktopEntry // transcript "": unreadable, names nothing
}

var claudeDesktopFiles = struct {
	sync.Mutex
	byPath map[string]claudeDesktopFile
}{byPath: map[string]claudeDesktopFile{}}

// claudeDesktopIndex is what the desktop apps' session index says about transcripts.
type claudeDesktopIndex struct {
	visible map[string]bool               // a session's current transcript → some entry naming it is unarchived
	earlier map[string][]claudeDesktopNow // an earlier transcript → the sessions that ran it before
}

// claudeDesktopNow is a session that went on from an earlier transcript: the one it runs now, and whether it is archived.
type claudeDesktopNow struct {
	transcript string
	archived   bool
}

// readClaudeDesktopIndex reads the index under home; a file is parsed again only when it changed.
func readClaudeDesktopIndex(home string) claudeDesktopIndex {
	var paths []string
	for _, dir := range claudeDesktopDirs(home) {
		found, _ := filepath.Glob(filepath.Join(dir, "claude-code-sessions", "*", "*", "local_*.json"))
		paths = append(paths, found...)
	}
	x := claudeDesktopIndex{visible: map[string]bool{}, earlier: map[string][]claudeDesktopNow{}}
	claudeDesktopFiles.Lock()
	defer claudeDesktopFiles.Unlock()
	current := make(map[string]bool, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.Size() > 4<<20 {
			continue
		}
		current[p] = true
		f, ok := claudeDesktopFiles.byPath[p]
		if !ok || !f.mtime.Equal(st.ModTime()) || f.size != st.Size() {
			f = claudeDesktopFile{mtime: st.ModTime(), size: st.Size(), entry: readClaudeDesktopEntry(p)}
			claudeDesktopFiles.byPath[p] = f
		}
		e := f.entry
		if e.transcript == "" {
			continue
		}
		x.visible[e.transcript] = x.visible[e.transcript] || !e.archived
		for _, old := range e.prior {
			x.earlier[old] = append(x.earlier[old], claudeDesktopNow{e.transcript, e.archived})
		}
	}
	for p := range claudeDesktopFiles.byPath {
		if !current[p] {
			delete(claudeDesktopFiles.byPath, p)
		}
	}
	return x
}

func readClaudeDesktopEntry(path string) claudeDesktopEntry {
	b, err := os.ReadFile(path)
	if err != nil {
		return claudeDesktopEntry{}
	}
	var raw struct {
		Transcript string   `json:"cliSessionId"`
		Archived   bool     `json:"isArchived"`
		Prior      []string `json:"priorCliSessionIds"`
	}
	if json.Unmarshal(b, &raw) != nil || !validID(raw.Transcript) {
		return claudeDesktopEntry{}
	}
	return claudeDesktopEntry{transcript: raw.Transcript, archived: raw.Archived, prior: raw.Prior}
}

// hidden: the offline list leaves transcript id out as the desktop app's sidebar does. shown are the transcripts the
// list has shown so far (newest first, so a session's current one comes before its earlier ones).
func (x claudeDesktopIndex) hidden(id string, shown map[string]bool) bool {
	if visible, named := x.visible[id]; named {
		return !visible // a session's current transcript: hidden while every entry naming it is archived
	}
	sessions := x.earlier[id]
	if len(sessions) == 0 {
		return false
	}
	open := false
	for _, s := range sessions {
		if !s.archived {
			open = true
			if shown[s.transcript] {
				return true // its session is listed through the transcript it runs now
			}
		}
	}
	return !open // only archived sessions ran it
}
