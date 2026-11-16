package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// oneMemoryNode is a node in a temporary home with the assistant's directory, the Health space's, the default Claude
// profile (~/.claude) and Persona's private one (profiles/claude). Nothing outside the temporary home is touched.
type oneMemoryNode struct {
	*Node
	cwd, health      string
	shared, private  string // the assistant's memory in ~/.claude, and the private profile's copy
	privateProfile   string
	healthInProfile  string
	healthInDefaults string
}

func newOneMemoryNode(t *testing.T) oneMemoryNode {
	t.Helper()
	home := t.TempDir()
	n := userSetupNode(t, home)
	f := oneMemoryNode{Node: n, cwd: filepath.Join(n.dir, "assistant"), health: filepath.Join(n.dir, "health")}
	for _, d := range []string{f.cwd, f.health} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.privateProfile = filepath.Join(n.dir, "profiles", "claude")
	f.shared = filepath.Join(home, ".claude", "projects", memoryKey(f.cwd), "memory")
	f.private = filepath.Join(f.privateProfile, "projects", memoryKey(f.cwd), "memory")
	f.healthInDefaults = filepath.Join(home, ".claude", "projects", memoryKey(f.health), "memory")
	f.healthInProfile = filepath.Join(f.privateProfile, "projects", memoryKey(f.health), "memory")
	return f
}

func writeNote(t *testing.T, dir, name, content string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func readNote(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noteMissing(dir, name string) bool {
	_, err := os.Lstat(filepath.Join(dir, name))
	return os.IsNotExist(err)
}

// dirState is every file below dir with its bytes and modification time, to show a directory was left alone.
func dirState(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[path] = info.ModTime().UTC().Format(time.RFC3339Nano) + "\n" + string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameState(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mergeRunDirs(t *testing.T, n oneMemoryNode) []string {
	t.Helper()
	entries, err := os.ReadDir(n.memoryMergeRoot(memoryKey(n.cwd)))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(n.memoryMergeRoot(memoryKey(n.cwd)), e.Name()))
	}
	return out
}

// Every way the assistant reaches its memory names one directory, whichever Claude profile a conversation runs in
// and whichever directories exist: the Claude start's settings (CLI and Agent SDK share the file), Codex and the
// other runtimes, the Memory page and dreaming.
func TestAssistantMemoryIsOneDirectoryInEveryProfile(t *testing.T) {
	n := newOneMemoryNode(t)
	// Only the private profile has the project so far: the old rule ("the first directory that has it") would pick it.
	writeNote(t, n.private, "side.md", "from a side conversation\n", time.Time{})
	if got := n.memoryDir(n.cwd); got != n.shared {
		t.Fatalf("memory directory %s, want the default profile's %s", got, n.shared)
	}

	for _, profile := range []string{"", "default", n.privateProfile, filepath.Join(n.home, "elsewhere")} {
		s := writtenSettings(t, n.Node, n.cwd, "", profile)
		if got, _ := s["autoMemoryDirectory"].(string); got != n.shared {
			t.Errorf("a conversation in profile %q is told %q, want %s", profile, got, n.shared)
		}
	}
	// The Agent SDK start reads the same settings file.
	path, err := n.assistantCompactionSettings("sdk-id", n.cwd, "", assistantCompaction{profile: n.privateProfile})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := (sdkStart{settings: path, mcp: map[string]any{}}).options("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := opts["settings"].(map[string]any)["autoMemoryDirectory"].(string); got != n.shared {
		t.Errorf("the Agent SDK start is told %q, want %s", got, n.shared)
	}

	real, err := filepath.EvalSymlinks(n.shared)
	if err != nil {
		t.Fatal(err)
	}
	if _, mem := n.codexDirs(n.cwd); mem != real {
		t.Errorf("Codex gets %s, want %s", mem, real)
	}
	if got := n.memoryForUse(n.cwd); got != n.shared { // what a runtime's turn carries as memory_dir
		t.Errorf("a runtime gets %s, want %s", got, n.shared)
	}
	res, err := n.assistantMemory(memoryOp{Op: "list", Cwd: n.cwd})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(map[string]any)["dir"]; got != n.shared {
		t.Errorf("the Memory page lists %v, want %s", got, n.shared)
	}
	if files := res.(map[string]any)["files"].([]MemoryFile); len(files) != 2 || files[1].Name != "side.md" {
		t.Errorf("the Memory page does not show the side conversation's note: %+v", files)
	}
	snapshot, err := n.assistantMemory(memoryOp{Op: "dream-read", Cwd: n.cwd})
	if err != nil {
		t.Fatal(err)
	}
	index, _ := snapshot.(map[string]any)["index"].(string)
	if !strings.Contains(index, "(side.md)") {
		t.Errorf("dreaming reads an index without the side conversation's note: %q", index)
	}
	if _, err := n.assistantMemory(memoryOp{Op: "dream-write", Cwd: n.cwd, PreviousIndex: index, Content: "Prefers tea."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readNote(t, n.shared, dreamNoteName), "Prefers tea.") || !noteMissing(n.private, dreamNoteName) {
		t.Error("dreaming did not write the one memory")
	}
}

// The boundaries stay: a guest and a meeting's room get no memory directory and keep auto memory off, and the Health
// space keeps its own memory where Claude Code puts it, with no new setting and no merge.
func TestOneMemoryLeavesGuestsAndHealthAlone(t *testing.T) {
	n := newOneMemoryNode(t)
	writeNote(t, n.private, "side.md", "from a side conversation\n", time.Time{})

	n.state.put("guest-1", sessionState{Launcher: "claude", Guest: []string{"lark_agent_reply"}})
	n.state.put("room-1", sessionState{Launcher: "claude", Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})
	for _, id := range []string{"guest-1", "room-1"} {
		start, err := n.assistantCompactionSettings(id, n.cwd, "", assistantCompaction{profile: n.privateProfile})
		if err != nil {
			t.Fatal(err)
		}
		if s, err := readLauncherSettings(start, n.cwd, n.home); err != nil || s["autoMemoryDirectory"] != nil {
			t.Errorf("%s: the start's settings name a memory directory: %v %v", id, s["autoMemoryDirectory"], err)
		}
		path, err := n.guestSettingsFile(id, n.cwd, n.privateProfile, start, false)
		if err != nil {
			t.Fatal(err)
		}
		s, err := readLauncherSettings(path, n.cwd, n.home)
		if err != nil {
			t.Fatal(err)
		}
		env, _ := s["env"].(map[string]any)
		if s["autoMemoryDirectory"] != nil || env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "1" {
			t.Errorf("%s: settings %v", id, s)
		}
	}
	if runs := mergeRunDirs(t, n); len(runs) != 0 {
		t.Errorf("a guest's start merged the memory: %v", runs)
	}

	// Health: only the private profile has its project, so that is its memory, as before. Nothing is merged for it.
	writeNote(t, n.healthInProfile, "sleep.md", "health note\n", time.Time{})
	if got := n.memoryDir(n.health); got != n.healthInProfile {
		t.Errorf("health memory %s, want %s", got, n.healthInProfile)
	}
	writeNote(t, n.healthInDefaults, "weight.md", "another health note\n", time.Time{})
	if got := n.memoryDir(n.health); got != n.healthInDefaults { // the default directory first, as before
		t.Errorf("health memory %s, want %s", got, n.healthInDefaults)
	}
	before := dirState(t, filepath.Join(n.home, ".claude", "projects", memoryKey(n.health)))
	if s := writtenSettings(t, n.Node, n.health, "", n.privateProfile); s["autoMemoryDirectory"] != nil {
		t.Errorf("a Health conversation is told a memory directory: %v", s["autoMemoryDirectory"])
	}
	res, err := n.assistantMemory(memoryOp{Op: "list", Cwd: n.health})
	if err != nil {
		t.Fatal(err)
	}
	if files := res.(map[string]any)["files"].([]MemoryFile); len(files) != 1 || files[0].Name != "weight.md" {
		t.Errorf("health memory lists %+v", files)
	}
	if _, mem := n.codexDirs(n.health); !strings.HasSuffix(mem, filepath.Join(memoryKey(n.health), "memory")) || strings.Contains(mem, memoryKey(n.cwd)+string(filepath.Separator)) {
		t.Errorf("Codex in the Health space gets %s", mem)
	}
	if !sameState(before, dirState(t, filepath.Join(n.home, ".claude", "projects", memoryKey(n.health)))) || !noteMissing(n.healthInDefaults, "sleep.md") {
		t.Error("the Health space's memory was merged")
	}
	if _, err := os.Stat(n.memoryMergeRoot(memoryKey(n.health))); !os.IsNotExist(err) {
		t.Errorf("a merge was recorded for the Health space: %v", err)
	}
	// And the main memory holds no health note, nor the other way round.
	n.memoryForUse(n.cwd)
	if !noteMissing(n.shared, "sleep.md") || !noteMissing(n.shared, "weight.md") || !noteMissing(n.healthInDefaults, "side.md") {
		t.Error("notes crossed between the main memory and the Health space's")
	}
}

// The copies other profiles hold are merged once: a note only a copy has comes in with its index line, the same
// name with different content keeps the one modified later, everything is saved first, the copies are left as they
// are, and running again changes nothing.
func TestMemoryCopiesAreMergedOnce(t *testing.T) {
	n := newOneMemoryNode(t)
	old, mid, recent := time.Now().Add(-72*time.Hour).Truncate(time.Second), time.Now().Add(-48*time.Hour).Truncate(time.Second), time.Now().Add(-24*time.Hour).Truncate(time.Second)

	writeNote(t, n.shared, "main-only.md", "main only\n", old)
	writeNote(t, n.shared, "same.md", "identical\n", old)
	writeNote(t, n.shared, "copy-newer.md", "main says no\n", old)
	writeNote(t, n.shared, "main-newer.md", "main says yes\n", recent)
	writeNote(t, n.shared, memoryIndexName, "- [Main only](main-only.md) — a\n- [Copy newer](copy-newer.md) — b\n- [Main newer](main-newer.md) — c", old)

	writeNote(t, n.private, "same.md", "identical\n", mid)
	writeNote(t, n.private, "copy-newer.md", "copy says yes\n", mid)
	writeNote(t, n.private, "main-newer.md", "copy says no\n", mid)
	writeNote(t, n.private, "indexed.md", "indexed in the copy\n", mid)
	writeNote(t, n.private, "unindexed.md", "---\nname: tunnel\ndescription: where the tunnel ends\n---\nbody\n", mid)
	writeNote(t, n.private, "notes.txt", "not a note\n", mid)
	writeNote(t, n.private, memoryIndexName, "- [Indexed](indexed.md) — from the copy (with a note in brackets)\n- [Copy newer](copy-newer.md) — copy's line\n- [Same](same.md) — in both, indexed only in the copy\n", mid)
	copyBefore := dirState(t, n.private)
	mainBefore := dirState(t, n.shared)

	if got := n.memoryForUse(n.cwd); got != n.shared {
		t.Fatalf("memory directory %s", got)
	}
	for name, want := range map[string]string{
		"main-only.md": "main only\n", "same.md": "identical\n", "copy-newer.md": "copy says yes\n", "main-newer.md": "main says yes\n",
		"indexed.md": "indexed in the copy\n", "unindexed.md": "---\nname: tunnel\ndescription: where the tunnel ends\n---\nbody\n",
	} {
		if got := readNote(t, n.shared, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !noteMissing(n.shared, "notes.txt") {
		t.Error("a file that is not a note was copied in")
	}
	if info, err := os.Stat(filepath.Join(n.shared, "indexed.md")); err != nil || !info.ModTime().Equal(mid) {
		t.Errorf("the note's modification time was not kept: %v %v", info, err)
	}
	wantIndex := "- [Main only](main-only.md) — a\n- [Copy newer](copy-newer.md) — b\n- [Main newer](main-newer.md) — c\n" +
		"- [Indexed](indexed.md) — from the copy (with a note in brackets)\n- [Same](same.md) — in both, indexed only in the copy\n- [tunnel](unindexed.md) — where the tunnel ends\n"
	if got := readNote(t, n.shared, memoryIndexName); got != wantIndex {
		t.Errorf("index:\n%s\nwant:\n%s", got, wantIndex)
	}
	if !sameState(copyBefore, dirState(t, n.private)) {
		t.Error("the other profile's copy was changed")
	}

	// The backup: the memory as it was, the notes read from the copy, and what was done.
	runs := mergeRunDirs(t, n)
	if len(runs) != 1 {
		t.Fatalf("runs %v", runs)
	}
	for path, want := range mainBefore {
		rel, _ := filepath.Rel(n.shared, path)
		info, err := os.Stat(filepath.Join(runs[0], "before", rel))
		if err != nil {
			t.Fatalf("backup of %s: %v", rel, err)
		}
		if got := info.ModTime().UTC().Format(time.RFC3339Nano) + "\n" + readNote(t, filepath.Join(runs[0], "before"), rel); got != want {
			t.Errorf("backup of %s = %q, want %q", rel, got, want)
		}
	}
	if got := readNote(t, filepath.Join(runs[0], "copies", "0"), "main-newer.md"); got != "copy says no\n" {
		t.Errorf("the version that lost is not in the backup: %q", got)
	}
	if got := readNote(t, filepath.Join(runs[0], "replaced", "0"), "copy-newer.md"); got != "main says no\n" {
		t.Errorf("the version that was replaced is not in the backup: %q", got)
	}
	var manifest memoryMergeRun
	if err := json.Unmarshal([]byte(readNote(t, runs[0], "manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, f := range manifest.Copies[0].Files {
		actions[f.Name] = f.Action + ":" + f.Kept
	}
	want := map[string]string{"same.md": "same:", "copy-newer.md": "conflict:copy", "main-newer.md": "conflict:memory", "indexed.md": "added:", "unindexed.md": "added:"}
	if len(actions) != len(want) || manifest.MemoryDir != n.shared || manifest.Copies[0].Dir != n.private {
		t.Fatalf("manifest %+v", manifest)
	}
	for name, a := range want {
		if actions[name] != a {
			t.Errorf("%s: %q, want %q", name, actions[name], a)
		}
	}

	// What the manager's report is built from: the notes brought in, and each conflict with the other version.
	res, err := n.assistantMemory(memoryOp{Op: "merges", Cwd: n.cwd})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)["runs"].([]memoryMergeRunOut)
	if len(out) != 1 || strings.Join(out[0].Added, ",") != "indexed.md,unindexed.md" || len(out[0].Conflicts) != 2 || out[0].Backup != runs[0] {
		t.Fatalf("merges %+v", out)
	}
	for _, c := range out[0].Conflicts {
		switch c.Name {
		case "copy-newer.md":
			if c.Kept != memoryKeptCopy || c.Other != "main says no\n" || !c.MemoryModified.Equal(old) || !c.CopyModified.Equal(mid) {
				t.Errorf("conflict %+v", c)
			}
		case "main-newer.md":
			if c.Kept != memoryKeptMemory || c.Other != "copy says no\n" || !c.MemoryModified.Equal(recent) || !c.CopyModified.Equal(mid) {
				t.Errorf("conflict %+v", c)
			}
		default:
			t.Errorf("unexpected conflict %+v", c)
		}
	}
	if res, _ := n.assistantMemory(memoryOp{Op: "merges", Cwd: n.cwd, After: out[0].ID}); len(res.(map[string]any)["runs"].([]memoryMergeRunOut)) != 0 {
		t.Error("a run already told is listed again")
	}

	// Again, in this process and in a node that has just started: nothing is done twice.
	after := dirState(t, n.shared)
	n.memoryForUse(n.cwd)
	n.memoryMerged = nil
	n.memoryForUse(n.cwd)
	if _, err := n.assistantMemory(memoryOp{Op: "list", Cwd: n.cwd}); err != nil {
		t.Fatal(err)
	}
	if !sameState(after, dirState(t, n.shared)) || len(mergeRunDirs(t, n)) != 1 {
		t.Errorf("a second run changed the memory or recorded a run: %v", mergeRunDirs(t, n))
	}

	// A note the user deletes on the Memory page stays deleted, and an edit made there is not undone.
	if _, err := n.assistantMemory(memoryOp{Op: "delete", Cwd: n.cwd, Name: "indexed.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.assistantMemory(memoryOp{Op: "write", Cwd: n.cwd, Name: "copy-newer.md", Content: "the user's own wording\n"}); err != nil {
		t.Fatal(err)
	}
	n.memoryMerged = nil
	n.memoryForUse(n.cwd)
	if !noteMissing(n.shared, "indexed.md") || strings.Contains(readNote(t, n.shared, memoryIndexName), "(indexed.md)") ||
		readNote(t, n.shared, "copy-newer.md") != "the user's own wording\n" || len(mergeRunDirs(t, n)) != 1 {
		t.Error("a deleted or edited note came back from the copy")
	}

	// A conversation still running with its old settings writes to its profile's copy afterwards: that is merged too.
	writeNote(t, n.private, "late.md", "written after the merge\n", time.Now())
	n.memoryForUse(n.cwd)
	if got := readNote(t, n.shared, "late.md"); got != "written after the merge\n" || len(mergeRunDirs(t, n)) != 2 {
		t.Errorf("late.md = %q, runs %v", got, mergeRunDirs(t, n))
	}
	if !strings.Contains(readNote(t, n.shared, memoryIndexName), "- [late](late.md)\n") {
		t.Errorf("index after the late note:\n%s", readNote(t, n.shared, memoryIndexName))
	}
}

// Two copies with a newer version of the same note: the newest stays, and the backup keeps the memory's original and
// every version that was replaced, each shown with the conflict it lost.
func TestMemoryMergeKeepsEveryReplacedVersion(t *testing.T) {
	n := newOneMemoryNode(t)
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	other := filepath.Join(n.dir, "profiles", "other", "projects", memoryKey(n.cwd), "memory")
	writeNote(t, n.shared, "x.md", "the memory's original\n", old)
	writeNote(t, n.private, "x.md", "the first copy's\n", old.Add(24*time.Hour))
	writeNote(t, other, "x.md", "the second copy's\n", old.Add(48*time.Hour))

	n.memoryForUse(n.cwd)
	runs := mergeRunDirs(t, n)
	if got := readNote(t, n.shared, "x.md"); got != "the second copy's\n" || len(runs) != 1 {
		t.Fatalf("x.md = %q, runs %v", got, runs)
	}
	if got := readNote(t, filepath.Join(runs[0], "before"), "x.md"); got != "the memory's original\n" {
		t.Errorf("the backup of the memory as it was holds %q", got)
	}
	res, err := n.assistantMemory(memoryOp{Op: "merges", Cwd: n.cwd})
	if err != nil {
		t.Fatal(err)
	}
	out := res.(map[string]any)["runs"].([]memoryMergeRunOut)
	if len(out) != 1 || len(out[0].Conflicts) != 2 || out[0].Conflicts[0].Other != "the memory's original\n" || out[0].Conflicts[1].Other != "the first copy's\n" ||
		out[0].Conflicts[0].Kept != memoryKeptCopy || out[0].Conflicts[1].Kept != memoryKeptCopy {
		t.Fatalf("merges %+v", out)
	}
}

// Profiles that share one projects directory through a link hold one memory, not a copy of it.
func TestMemoryCopyThroughALinkIsNotMerged(t *testing.T) {
	n := newOneMemoryNode(t)
	writeNote(t, n.shared, "a.md", "a\n", time.Time{})
	if err := os.MkdirAll(n.privateProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(n.home, ".claude", "projects"), filepath.Join(n.privateProfile, "projects")); err != nil {
		t.Skip("no symlinks here")
	}
	before := dirState(t, n.shared)
	n.memoryForUse(n.cwd)
	if !sameState(before, dirState(t, n.shared)) || len(mergeRunDirs(t, n)) != 0 {
		t.Error("the memory was merged with itself")
	}
}

// A merge that cannot be done leaves no backup of a run that did not happen, and is not tried again on every start.
func TestMemoryMergeThatFailsIsRetriedLater(t *testing.T) {
	n := newOneMemoryNode(t)
	writeNote(t, n.private, "side.md", "from a side conversation\n", time.Time{})
	if err := os.MkdirAll(filepath.Dir(n.shared), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(n.shared, []byte("not a directory"), 0o600); err != nil { // the memory cannot be written
		t.Fatal(err)
	}
	if got := n.memoryForUse(n.cwd); got != n.shared {
		t.Fatalf("memory directory %s", got)
	}
	if runs := mergeRunDirs(t, n); len(runs) != 0 {
		t.Fatalf("a failed merge left %v", runs)
	}
	if err := os.Remove(n.shared); err != nil {
		t.Fatal(err)
	}
	n.memoryForUse(n.cwd) // too soon after the failure
	if !noteMissing(n.shared, "side.md") {
		t.Fatal("the merge was tried again at once")
	}
	n.memoryMergeRetry[memoryKey(n.cwd)] = time.Now().Add(-time.Second)
	n.memoryForUse(n.cwd)
	if got := readNote(t, n.shared, "side.md"); got != "from a side conversation\n" || len(mergeRunDirs(t, n)) != 1 {
		t.Fatalf("after the wait: %q, runs %v", got, mergeRunDirs(t, n))
	}
}

// The model writes the memory with its own tools while a merge runs: a new note never lands on a name that was taken
// meanwhile, a version that changed since it was compared is not replaced, and the index is appended to as it is at
// that moment.
func TestMemoryMergeYieldsToConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	mod := time.Now().Add(-time.Hour).Truncate(time.Second)
	note := memoryNote{name: "a.md", data: []byte("from the copy\n"), mod: mod}

	writeNote(t, dir, "a.md", "just written by the model\n", time.Time{})
	if made, err := createMemoryNote(filepath.Join(dir, "a.md"), note); err != nil || made {
		t.Fatalf("created over an existing note: %v %v", made, err)
	}
	if got := readNote(t, dir, "a.md"); got != "just written by the model\n" {
		t.Fatalf("a.md = %q", got)
	}

	writeNote(t, dir, "b.md", "old\n", mod.Add(-time.Hour))
	was, err := os.Lstat(filepath.Join(dir, "b.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeNote(t, dir, "b.md", "rewritten by the model since\n", time.Time{})
	note.name = "b.md"
	if swapped, err := replaceMemoryNote(filepath.Join(dir, "b.md"), note, was, []byte("old\n")); err != nil || swapped {
		t.Fatalf("replaced a note that had changed: %v %v", swapped, err)
	}
	// The same size and time as before, other bytes (a write within the file system's clock tick): still not replaced.
	writeNote(t, dir, "s.md", "aaaa\n", mod.Add(-time.Hour))
	was, err = os.Lstat(filepath.Join(dir, "s.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeNote(t, dir, "s.md", "bbbb\n", mod.Add(-time.Hour))
	if swapped, err := replaceMemoryNote(filepath.Join(dir, "s.md"), memoryNote{name: "s.md", data: []byte("from the copy\n"), mod: mod}, was, []byte("aaaa\n")); err != nil || swapped {
		t.Fatalf("replaced a note rewritten with the same size and time: %v %v", swapped, err)
	}
	if got := readNote(t, dir, "b.md"); got != "rewritten by the model since\n" {
		t.Fatalf("b.md = %q", got)
	}
	// The memory's version, written now, is the newer one: the copy's is recorded as the one that lost.
	if action, kept, _, replaced, err := mergeMemoryNote(dir, note); err != nil || action != memoryMergeConflict || kept != memoryKeptMemory || replaced != nil {
		t.Fatalf("merge of a note the model just rewrote: %s %s %v", action, kept, err)
	}

	writeNote(t, dir, memoryIndexName, "- [A](a.md) — a", time.Time{})
	if err := addMemoryIndexLines(dir, []memoryIndexEntry{{"a.md", "- [A again](a.md) — duplicate"}, {"c.md", "- [C](c.md) — c (see b)"}}); err != nil {
		t.Fatal(err)
	}
	if err := addMemoryIndexLines(dir, []memoryIndexEntry{{"c.md", "- [C](c.md) — c (see b)"}}); err != nil {
		t.Fatal(err)
	}
	if got := readNote(t, dir, memoryIndexName); got != "- [A](a.md) — a\n- [C](c.md) — c (see b)\n" {
		t.Fatalf("index %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".merge-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

// The check before every start costs a directory listing when there is nothing new to merge.
func BenchmarkMemoryMergeCheck(b *testing.B) {
	home := b.TempDir()
	b.Setenv("CLAUDE_CONFIG_DIR", "")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	n := &Node{home: home, dir: filepath.Join(home, ".persona"), state: &stateFile{Sessions: map[string]sessionState{}}}
	cwd := filepath.Join(n.dir, "assistant")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		b.Fatal(err)
	}
	shared := filepath.Join(home, ".claude", "projects", memoryKey(cwd), "memory")
	private := filepath.Join(n.dir, "profiles", "claude", "projects", memoryKey(cwd), "memory")
	for _, d := range []string{shared, private} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			b.Fatal(err)
		}
	}
	body := []byte(strings.Repeat("a line of a memory note\n", 120)) // about 3 KB, a typical note
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(private, "note-"+string(rune('a'+i))+".md"), body, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	n.memoryForUse(cwd) // the merge itself
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n.memoryForUse(cwd)
	}
}
