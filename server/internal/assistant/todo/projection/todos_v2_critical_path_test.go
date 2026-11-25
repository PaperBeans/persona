package projection

import (
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func needsYouDecisionFact(id string, askedAt time.Time, todosV2 bool) Facts {
	return Facts{
		ID:        id,
		Title:     id,
		CreatedAt: askedAt.Add(-time.Hour),
		UpdatedAt: askedAt,
		Origin:    "conv_main",
		TodosV2:   todosV2,
		Ask: &Ask{
			Outcome:  outcomeDecide,
			Verdict:  reviewNeedsUser,
			AskedAt:  askedAt,
			Question: "Decide for " + id,
			Options: []assistantwire.OwnerAskOption{
				{Label: "Proceed", LeadsTo: "Continue"},
			},
		},
	}
}

func openTodoFact(id string, createdAt time.Time, todosV2 bool, after ...string) Facts {
	return Facts{
		ID:        id,
		Title:     id,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Origin:    "conv_main",
		After:     after,
		TodosV2:   todosV2,
	}
}

func viewByID(list []Facts, views []assistantwire.TodoSummary) map[string]assistantwire.TodoSummary {
	out := make(map[string]assistantwire.TodoSummary, len(list))
	for i := range list {
		out[list[i].ID] = views[i]
	}
	return out
}

func TestTodosV2CriticalPathChainFanOutAndRollback(t *testing.T) {
	// Older isolated ask vs newer chain blocker (2 downstream) vs newest fan-out blocker (3 downstream).
	isolated := needsYouDecisionFact("todo_isolated", hours(-5), false)
	chainRoot := needsYouDecisionFact("todo_chain_a", hours(-3), false)
	chainMid := openTodoFact("todo_chain_b", hours(-3), false, "todo_chain_a")
	chainLeaf := openTodoFact("todo_chain_c", hours(-3), false, "todo_chain_b")

	fanRoot := needsYouDecisionFact("todo_fan_a", hours(-1), false)
	fanB := openTodoFact("todo_fan_b", hours(-1), false, "todo_fan_a")
	fanC := openTodoFact("todo_fan_c", hours(-1), false, "todo_fan_a")
	fanD := openTodoFact("todo_fan_d", hours(-1), false, "todo_fan_a")

	legacyList := []Facts{isolated, chainRoot, chainMid, chainLeaf, fanRoot, fanB, fanC, fanD}
	legacyViews := Views(legacyList, testNow)
	legacyMap := viewByID(legacyList, legacyViews)

	// todos_v2 == false: legacy oldest-ask-first ordering is untouched.
	if !(legacyMap["todo_isolated"].Rank < legacyMap["todo_chain_a"].Rank && legacyMap["todo_chain_a"].Rank < legacyMap["todo_fan_a"].Rank) {
		t.Fatalf("legacy ranks changed: isolated=%d chain=%d fan=%d",
			legacyMap["todo_isolated"].Rank, legacyMap["todo_chain_a"].Rank, legacyMap["todo_fan_a"].Rank)
	}

	// todos_v2 == true: fan-out (3) > chain (2) > isolated (0).
	v2List := make([]Facts, len(legacyList))
	copy(v2List, legacyList)
	for i := range v2List {
		v2List[i].TodosV2 = true
	}
	v2Views := Views(v2List, testNow)
	v2Map := viewByID(v2List, v2Views)
	if !(v2Map["todo_fan_a"].Rank < v2Map["todo_chain_a"].Rank && v2Map["todo_chain_a"].Rank < v2Map["todo_isolated"].Rank) {
		t.Fatalf("todos_v2 critical-path ranks mismatch: fan=%d chain=%d isolated=%d",
			v2Map["todo_fan_a"].Rank, v2Map["todo_chain_a"].Rank, v2Map["todo_isolated"].Rank)
	}

	// Turning todos_v2 off restores exact legacy views byte-for-byte.
	rollbackList := make([]Facts, len(v2List))
	copy(rollbackList, v2List)
	for i := range rollbackList {
		rollbackList[i].TodosV2 = false
	}
	rollbackViews := Views(rollbackList, testNow)
	if !reflect.DeepEqual(rollbackViews, legacyViews) {
		t.Fatalf("rollback mismatch:\ngot:  %+v\nwant: %+v", rollbackViews, legacyViews)
	}
}

func TestTodosV2CriticalPathPartOfAndDiamondDeduplication(t *testing.T) {
	// Diamond: A -> B, A -> C, B -> D, C -> D => A blocks 3 distinct open tasks (not 4).
	diamondA := needsYouDecisionFact("todo_diamond_a", hours(-2), true)
	diamondB := openTodoFact("todo_diamond_b", hours(-2), true, "todo_diamond_a")
	diamondC := openTodoFact("todo_diamond_c", hours(-2), true, "todo_diamond_a")
	diamondD := openTodoFact("todo_diamond_d", hours(-2), true, "todo_diamond_b", "todo_diamond_c")

	// PartOf + After: step B part_of P, C after P, B after X => X blocks {B, P, C} = 3;
	// P blocks {C} = 1; isolated blocks 0.
	partX := needsYouDecisionFact("todo_part_x", hours(-1), true)
	partP := openTodoFact("todo_part_p", hours(-4), true)
	partB := openTodoFact("todo_part_b", hours(-4), true, "todo_part_x")
	partB.PartOf = "todo_part_p"
	partC := openTodoFact("todo_part_c", hours(-4), true, "todo_part_p")

	// Parent P itself needs user via a separate chain: Q blocks {R} = 1.
	parentQ := needsYouDecisionFact("todo_parent_q", hours(-6), true)
	afterQ := openTodoFact("todo_after_q", hours(-6), true, "todo_parent_q")

	list := []Facts{diamondA, diamondB, diamondC, diamondD, partX, partP, partB, partC, parentQ, afterQ}
	related := relate(list)
	placed := make([]placement, len(related))
	for i := range related {
		placed[i] = place(&related[i], testNow)
	}
	counts := downstreamBlockedCounts(related, placed)
	if counts["todo_diamond_a"] != 3 {
		t.Fatalf("diamond count=%d, want 3", counts["todo_diamond_a"])
	}
	if counts["todo_part_x"] != 3 || counts["todo_part_b"] != 2 || counts["todo_part_p"] != 1 {
		t.Fatalf("part_of counts mismatch: x=%d b=%d p=%d, want 3/2/1",
			counts["todo_part_x"], counts["todo_part_b"], counts["todo_part_p"])
	}
	if counts["todo_parent_q"] != 1 {
		t.Fatalf("parent_q count=%d, want 1", counts["todo_parent_q"])
	}

	views := Views(list, testNow)
	byID := viewByID(list, views)
	// diamondA (3, asked -2h) < partX (3, asked -1h) < parentQ (1, asked -6h).
	if !(byID["todo_diamond_a"].Rank < byID["todo_part_x"].Rank && byID["todo_part_x"].Rank < byID["todo_parent_q"].Rank) {
		t.Fatalf("unexpected ranks: diamondA=%d partX=%d parentQ=%d",
			byID["todo_diamond_a"].Rank, byID["todo_part_x"].Rank, byID["todo_parent_q"].Rank)
	}
}

func TestTodosV2CriticalPathIgnoresClosedAndCancelledDownstream(t *testing.T) {
	doneAt := hours(-1)
	cancelledAt := hours(-1)

	older := needsYouDecisionFact("todo_older", hours(-5), true)
	newer := needsYouDecisionFact("todo_newer", hours(-1), true)

	// Completed downstream and cancelled downstream must not inflate newer's count.
	doneChild := openTodoFact("todo_done_child", hours(-2), true, "todo_newer")
	doneChild.DoneAt = &doneAt
	cancelledChild := openTodoFact("todo_cancelled_child", hours(-2), true, "todo_newer")
	cancelledChild.DoneAt = &cancelledAt
	cancelledChild.CancelledAt = &cancelledAt
	// Downstream of a closed task should not be reachable through the closed task.
	afterDone := openTodoFact("todo_after_done", hours(-2), true, "todo_done_child")

	list := []Facts{older, newer, doneChild, cancelledChild, afterDone}
	views := Views(list, testNow)
	byID := viewByID(list, views)

	// Both have 0 open downstream tasks, so older ask still ranks first.
	if !(byID["todo_older"].Rank < byID["todo_newer"].Rank) {
		t.Fatalf("closed/cancelled downstream inflated rank: older=%d newer=%d",
			byID["todo_older"].Rank, byID["todo_newer"].Rank)
	}
}

func TestTodosV2CriticalPathCycleSafety(t *testing.T) {
	// Self-loop + 2-cycle + 3-cycle with tail must terminate and rank deterministically.
	selfLoop := needsYouDecisionFact("todo_self", hours(-5), true)
	selfLoop.After = []string{"todo_self"}
	selfLoop.PartOf = "todo_self"

	cycA := needsYouDecisionFact("todo_cyc_a", hours(-2), true)
	cycA.After = []string{"todo_cyc_c"}
	cycB := openTodoFact("todo_cyc_b", hours(-2), true, "todo_cyc_a")
	cycC := openTodoFact("todo_cyc_c", hours(-2), true, "todo_cyc_b")
	cycTail := openTodoFact("todo_cyc_tail", hours(-2), true, "todo_cyc_c")

	list := []Facts{selfLoop, cycA, cycB, cycC, cycTail}
	views := Views(list, testNow)
	byID := viewByID(list, views)

	// cycA transitively reaches {cycB, cycC, cycTail} = 3; selfLoop reaches 0.
	if !(byID["todo_cyc_a"].Rank < byID["todo_self"].Rank) {
		t.Fatalf("cycle-safe rank mismatch: cycA=%d selfLoop=%d", byID["todo_cyc_a"].Rank, byID["todo_self"].Rank)
	}
}

func TestTodosV2CriticalPathCanWaitBoundaryAndTieBreak(t *testing.T) {
	// Active owner ask with 0 downstream stays ahead of a can_wait decision with 3 downstream.
	active := needsYouDecisionFact("todo_active", hours(-1), true)

	canWait := needsYouDecisionFact("todo_can_wait", hours(-10), true)
	canWait.Ask.ForBriefing = true
	cw1 := openTodoFact("todo_cw_1", hours(-10), true, "todo_can_wait")
	cw2 := openTodoFact("todo_cw_2", hours(-10), true, "todo_can_wait")
	cw3 := openTodoFact("todo_cw_3", hours(-10), true, "todo_can_wait")

	// Equal downstream count tie-breaking: earlier deadline beats later deadline; then earlier since; then ID.
	tieA := needsYouDecisionFact("todo_tie_a", hours(-2), true)
	tieA.Ask.Deadline = at(hours(2))
	tieADep := openTodoFact("todo_tie_a_dep", hours(-2), true, "todo_tie_a")

	tieB := needsYouDecisionFact("todo_tie_b", hours(-4), true)
	tieB.Ask.Deadline = at(hours(6))
	tieBDep := openTodoFact("todo_tie_b_dep", hours(-4), true, "todo_tie_b")

	list := []Facts{active, canWait, cw1, cw2, cw3, tieA, tieADep, tieB, tieBDep}
	views := Views(list, testNow)
	byID := viewByID(list, views)

	if byID["todo_can_wait"].Section != assistantwire.TodoListSectionCanWait || !byID["todo_can_wait"].CanWait {
		t.Fatalf("can_wait item left can_wait section: %+v", byID["todo_can_wait"])
	}
	if byID["todo_active"].Section != assistantwire.TodoListSectionNeedsYou {
		t.Fatalf("active item left needs_you section: %+v", byID["todo_active"])
	}
	// Within needs_you: tieA (1 downstream, +2h deadline) < tieB (1 downstream, +6h deadline) < active (0 downstream).
	if !(byID["todo_tie_a"].Rank < byID["todo_tie_b"].Rank && byID["todo_tie_b"].Rank < byID["todo_active"].Rank) {
		t.Fatalf("tie-break rank mismatch: tieA=%d tieB=%d active=%d",
			byID["todo_tie_a"].Rank, byID["todo_tie_b"].Rank, byID["todo_active"].Rank)
	}

	// Also pin direct compareRank invariant: active (!canWait) with 0 blocked beats canWait with 10 blocked.
	activeKey := rankKey{criticalPath: true, canWait: false, downstreamBlocked: 0, since: hours(-1)}
	canWaitKey := rankKey{criticalPath: true, canWait: true, downstreamBlocked: 10, since: hours(-10)}
	if got := compareRank(assistantwire.TodoListSectionNeedsYou, activeKey, canWaitKey); got >= 0 {
		t.Fatalf("compareRank(active, canWait) = %d, want < 0", got)
	}
	if got := compareRank(assistantwire.TodoListSectionNeedsYou, canWaitKey, activeKey); got <= 0 {
		t.Fatalf("compareRank(canWait, active) = %d, want > 0", got)
	}
}
