package node

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	indexIDs    = "- 开放待办索引（仅列 ID）："
	indexTitles = "- 开放待办索引（列出 ID 和标题）："
	indexCount  = "- 开放待办：共 %d 件（完整列表用 todo_list）"
)

// openTodoIndex renders an open to-do index of n to-dos (todo_1 … todo_n, the newest first) with or without titles, and the
// count section above it, as prompts/assistant/snapshot.md does.
func openTodoIndex(n int, titles bool, extra ...string) string {
	head := indexIDs
	if titles {
		head = indexTitles
	}
	items := append([]string(nil), extra...)
	for i := n; i >= 1; i-- {
		item := fmt.Sprintf("todo_%d", i)
		if titles {
			item += fmt.Sprintf("「巡检任务 %d」", i)
		}
		items = append(items, item)
	}
	return snapshotOf("- 现在：2026-11-23 周一 10:00（UTC）", fmt.Sprintf(indexCount, n+len(extra)), head+strings.Join(items, "；"))
}

// The open to-do index is told again only by the to-dos that came or went: before, every message that changed any of
// it carried all 60 ids again.
func TestContextDeltaOpenTodoIndexTellsOnlyAddedAndRemoved(t *testing.T) {
	first := openTodoIndex(60, false)
	block, seen := contextDelta(nil, first)
	if strings.Contains(block, "开放待办索引") || strings.Contains(block, "todo_60；todo_59") ||
		!strings.Contains(block, fmt.Sprintf(indexCount, 60)) {
		t.Fatalf("a full block repeats the index instead of its count and todo_list guidance:\n%s", block)
	}

	// Nothing changed: the index is not told.
	block, seen = contextDelta(seen, first)
	if strings.Contains(block, "开放待办索引") {
		t.Fatalf("unchanged index told:\n%s", block)
	}

	// One came, one went: only those, and the count line.
	next := strings.Replace(openTodoIndex(60, false, "todo_61"), "；todo_7；", "；", 1)
	next = strings.Replace(next, fmt.Sprintf(indexCount, 61), fmt.Sprintf(indexCount, 60), 1)
	block, seen = contextDelta(seen, next)
	if !strings.Contains(block, "- 开放待办索引"+rowsMark+"：\n  - todo_61\n"+rowsRemoved+"todo_7）") ||
		strings.Contains(block, "todo_59") || strings.Contains(block, rowsOrder) {
		t.Fatalf("one in, one out:\n%s", block)
	}
	if n := utf8.RuneCountInString(block); n > 250 {
		t.Fatalf("the block is %d characters:\n%s", n, block)
	}

	// The snapshot switches to titles (more room this time): the same to-dos, nothing to tell.
	titled := strings.Replace(openTodoIndex(60, true, "todo_61"), "；todo_7「巡检任务 7」；", "；", 1)
	titled = strings.Replace(titled, fmt.Sprintf(indexCount, 61), fmt.Sprintf(indexCount, 60), 1)
	block, seen = contextDelta(seen, titled)
	if strings.Contains(block, "开放待办索引") {
		t.Fatalf("a change of rendering alone was told:\n%s", block)
	}

	// With titles on both sides, a flag that changed is told by its item.
	flagged := strings.Replace(titled, "todo_5「巡检任务 5」", "todo_5「巡检任务 5」（没开起来）", 1)
	block, _ = contextDelta(seen, flagged)
	if !strings.Contains(block, "\n  - todo_5「巡检任务 5」（没开起来）") || strings.Contains(block, "todo_6") {
		t.Fatalf("changed flag:\n%s", block)
	}
}

// An index a title with "；" splits cannot be read by its items: it is told whole, as before.
func TestContextDeltaOpenTodoIndexUnreadableIsWhole(t *testing.T) {
	old := snapshotOf("- 现在：2026-11-23 周一 10:00（UTC）", indexTitles+"todo_2「甲；乙」；todo_1「丙」")
	_, seen := contextDelta(nil, old)
	next := snapshotOf("- 现在：2026-11-23 周一 10:01（UTC）", indexTitles+"todo_3「丁」；todo_2「甲；乙」；todo_1「丙」")
	block, _ := contextDelta(seen, next)
	if !strings.Contains(block, "\n"+indexTitles+"todo_3「丁」；todo_2「甲；乙」；todo_1「丙」\n") {
		t.Fatalf("not whole:\n%s", block)
	}
}

// The trailer after the listed items ("其余用 todo_list 查") is not an item; the items before it still are.
func TestInlineItemsTrailer(t *testing.T) {
	head, items, ids, ok := inlineItems(indexIDs+"todo_2；todo_1（没开起来）；其余用 todo_list 查", inlineTodo)
	if !ok || head != "- 开放待办索引（仅列 ID）" || len(items) != 2 || strings.Join(ids, ",") != "todo_2,todo_1" || items["todo_1"] != "todo_1（没开起来）" {
		t.Fatalf("head %q items %v ids %v ok %v", head, items, ids, ok)
	}
	if _, _, _, ok := inlineItems(indexIDs+"todo_2；todo_2", inlineTodo); ok {
		t.Fatal("a repeated id read as items")
	}
}

func TestContextDeltaFullOpenTodoIndexMeasurement(t *testing.T) {
	snapshot := openTodoIndex(60, false)
	block, seen := contextDelta(nil, snapshot)
	index := ""
	for _, s := range snapshotSections(block) {
		if inlineIDs[s.key] != nil {
			index = s.text
		}
	}
	countAndIndex := fmt.Sprintf(indexCount, 60)
	if index != "" {
		countAndIndex += "\n" + index
	}
	t.Logf("snapshot_sha256=%x index_chars=%d count_and_index_chars=%d\n%s", sha256.Sum256([]byte(snapshot)), utf8.RuneCountInString(index), utf8.RuneCountInString(countAndIndex), block)
	if index != "" || !strings.Contains(block, countAndIndex) {
		t.Fatalf("a full block without a baseline keeps only the count and todo_list guidance:\n%s", block)
	}

	// A full block that still has a comparison baseline uses the same one-in, one-out delta as an ordinary block.
	next := strings.Replace(openTodoIndex(60, false, "todo_61"), "；todo_7；", "；", 1)
	next = strings.Replace(next, fmt.Sprintf(indexCount, 61), fmt.Sprintf(indexCount, 60), 1)
	block, seen = contextDeltaMode(seen, true, next)
	if !strings.Contains(block, "- 开放待办索引"+rowsMark+"：\n  - todo_61\n"+rowsRemoved+"todo_7）") ||
		strings.Contains(block, "todo_59") || !strings.Contains(block, fmt.Sprintf(indexCount, 60)) {
		t.Fatalf("a full block with one in and one out:\n%s", block)
	}
	block, _ = contextDeltaMode(seen, true, next)
	if strings.Contains(block, "开放待办索引") || !strings.Contains(block, fmt.Sprintf(indexCount, 60)) {
		t.Fatalf("an unchanged full index was told again:\n%s", block)
	}
}

// Compaction retains only a comparison baseline, never the old list as context. A restart has no baseline, so its
// full block also leaves the complete index to todo_list. Other conversation-specific task sections stay whole.
func TestTurnContextFullOpenTodoIndexAfterCompactionAndRestart(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	own := "- 这个对话派出或督办的任务：\n" + rowLogin + "\n- 这个对话派出或督办的其他开放任务（仅列 ID）：todo_own"
	first := snapshotOf(openTodoIndex(3, false), own)
	next := strings.Replace(openTodoIndex(3, false, "todo_4"), "；todo_2；", "；", 1)
	next = snapshotOf(strings.Replace(next, fmt.Sprintf(indexCount, 4), fmt.Sprintf(indexCount, 3), 1), own)
	send := func(n *Node, snapshot string) string {
		block, commit := n.turnContext(id, contextAnswer{Text: snapshot})
		commit()
		return block
	}
	assertOwnTasks := func(block string) {
		t.Helper()
		if !strings.Contains(block, own) || !strings.Contains(block, fullIntro) {
			t.Fatalf("a full block lost the conversation's own tasks:\n%s", block)
		}
	}

	n := assistantNode(t)
	block := send(n, first)
	assertOwnTasks(block)
	if strings.Contains(block, "开放待办索引") || !strings.Contains(block, fmt.Sprintf(indexCount, 3)) {
		t.Fatalf("the first full block lists every id:\n%s", block)
	}

	n.contextCompacted(id)
	block = send(n, next)
	assertOwnTasks(block)
	if !strings.Contains(block, "  - todo_4\n"+rowsRemoved+"todo_2）") || strings.Contains(block, "todo_3") {
		t.Fatalf("the full block after compaction did not list only changes:\n%s", block)
	}

	n.contextCompacted(id)
	block = send(n, next)
	assertOwnTasks(block)
	if strings.Contains(block, "开放待办索引") {
		t.Fatalf("an unchanged index returned after compaction:\n%s", block)
	}

	block = send(assistantNode(t), next)
	assertOwnTasks(block)
	if strings.Contains(block, "开放待办索引") || !strings.Contains(block, fmt.Sprintf(indexCount, 3)) {
		t.Fatalf("a restart without a baseline lists every id:\n%s", block)
	}
}
