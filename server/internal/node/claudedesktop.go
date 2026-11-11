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
// unarchived session once (a side session nested under its parent), however long it has been quiet. The offline list
// follows it (#3304): it lists the transcript each unarchived session runs whatever its age, on a count of its own (with
// the entry's title and directory where the transcript has none yet), and leaves out the transcripts of archived
// sessions, and a session's earlier transcripts once the one it runs now is listed. Only what an entry says is acted on:
// a transcript no entry names, or an entry that cannot be read, hides nothing; at worst a session shows twice, never not
// at all. Only these files are read, never the app's config file (it holds credentials).

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
	title, cwd string   // as the sidebar shows it, and where it runs
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
	about   map[string]claudeDesktopAbout // an unarchived session's current transcript → its title and directory
}

type claudeDesktopAbout struct{ title, cwd string }

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
	x := claudeDesktopIndex{visible: map[string]bool{}, earlier: map[string][]claudeDesktopNow{}, about: map[string]claudeDesktopAbout{}}
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
		if !e.archived {
			x.about[e.transcript] = claudeDesktopAbout{e.title, e.cwd}
		}
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
		Title      string   `json:"title"`
		Cwd        string   `json:"cwd"`
	}
	if json.Unmarshal(b, &raw) != nil || !validID(raw.Transcript) {
		return claudeDesktopEntry{}
	}
	cwd := raw.Cwd
	if !filepath.IsAbs(cwd) {
		cwd = ""
	}
	return claudeDesktopEntry{transcript: raw.Transcript, archived: raw.Archived, prior: raw.Prior, title: raw.Title, cwd: cwd}
}

// listed: transcript id is the one an unarchived session of the desktop app runs: the sidebar lists it, so the offline
// list does whatever its age (up to maxDesktopOffline, the newest first), on a count of its own (#3304).
func (x claudeDesktopIndex) listed(id string) bool { return x.visible[id] }

// maxDesktopOffline bounds the desktop app's sessions the offline list shows (the newest, whatever their age); they are
// counted on their own, not toward the 80 of the others.
const maxDesktopOffline = 100

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
