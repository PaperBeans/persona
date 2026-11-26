package assistant

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// Server-owned board projection tree for todos_v2 (#15774): computes status groups (with top-level
// goal folding and blocked/needs-you child surfacing), direction groups (preferring explicit
// direction_id), the By-to-do sidebar folded tree, and unified summary counts in one deterministic
// pass so clients stay thin renderers.
//
// All types here are intentionally unexported and all constants are untyped strings to respect
// internal/archguard domain layer baselines.

const (
	boardSectionNeedsYou      = "needs_you"
	boardSectionInProgress    = "in_progress"
	boardSectionPendingReview = "pending_review"
	boardSectionWaiting       = "waiting"
	boardSectionSomeday       = "someday"
	boardSectionDone          = "done"
)

var boardSectionOrder = [...]string{
	boardSectionNeedsYou,
	boardSectionInProgress,
	boardSectionPendingReview,
	boardSectionWaiting,
	boardSectionSomeday,
	boardSectionDone,
}

type todoBoardProjection struct {
	StatusGroups    []todoBoardStatusGroup    `json:"status_groups"`
	DirectionGroups []todoBoardDirectionGroup `json:"direction_groups"`
	SidebarTree     []todoBoardSidebarNode    `json:"sidebar_tree"`
	Counts          todoBoardCounts           `json:"counts"`
}

type todoBoardStatusGroup struct {
	Key          string           `json:"key"`
	TotalCount   int              `json:"total_count"`
	VisibleCount int              `json:"visible_count"`
	Entries      []todoBoardEntry `json:"entries"`
}

type todoBoardEntry struct {
	TodoID           string   `json:"todo_id"`
	FoldedChildIDs   []string `json:"folded_child_ids,omitempty"`
	SurfacedChildIDs []string `json:"surfaced_child_ids,omitempty"`
	FoldedCount      int      `json:"folded_count,omitempty"`
}

type todoBoardDirectionGroup struct {
	DirectionID string                   `json:"direction_id"`
	Label       string                   `json:"label,omitempty"`
	Counts      todoBoardDirectionCounts `json:"counts"`
	TodoIDs     []string                 `json:"todo_ids"`
}

type todoBoardDirectionCounts struct {
	NeedsYou      int `json:"needs_you"`
	InProgress    int `json:"in_progress"`
	PendingReview int `json:"pending_review"`
	Waiting       int `json:"waiting"`
	Done          int `json:"done"`
	Total         int `json:"total"`
}

type todoBoardSidebarNode struct {
	TodoID           string   `json:"todo_id"`
	Section          string   `json:"section"`
	ItemType         string   `json:"item_type"`
	DirectionID      string   `json:"direction_id,omitempty"`
	ChildIDs         []string `json:"child_ids,omitempty"`
	SurfacedChildIDs []string `json:"surfaced_child_ids,omitempty"`
	FoldedCount      int      `json:"folded_count"`
	NeedsYouCount    int      `json:"needs_you_count"`
	ActiveCount      int      `json:"active_count"`
}

type todoBoardCounts struct {
	NeedsYou      int `json:"needs_you"`
	InProgress    int `json:"in_progress"`
	PendingReview int `json:"pending_review"`
	Waiting       int `json:"waiting"`
	Someday       int `json:"someday"`
	Done          int `json:"done"`
	OpenTotal     int `json:"open_total"`
}

type todoBoardItemMeta struct {
	item        listedTodo
	section     string
	subOrder    int
	viewRank    int
	rootGoalID  string
	surfaced    bool
	directionID string
	itemType    string
}

// todoBoardDirectionLabels resolves display names for all known directions once per GET /api/v1/todos read.
func (s *Service) todoBoardDirectionLabels(ctx context.Context) map[string]string {
	cfg := s.settings(ctx)
	titles, _ := s.conversationTitles()
	names := s.directionSessionNames(ctx, cfg, titles)
	if names == nil {
		names = map[string]string{}
	}
	b := &s.directions
	b.mu.Lock()
	if s.directionsLocked(ctx) {
		for id, d := range b.m {
			if id == "" || id == cfg.Main || d == nil {
				continue
			}
			if _, exists := names[id]; exists {
				continue
			}
			if label := strings.TrimSpace(d.Name); label != "" {
				names[id] = label
			}
		}
	}
	b.mu.Unlock()
	return names
}

// buildTodoBoardProjection deterministically computes the complete server-owned board projection tree
// for GET /api/v1/todos under todos_v2=true with default top-level goal folding enabled.
func buildTodoBoardProjection(items []listedTodo, directions map[string]string) *todoBoardProjection {
	return buildTodoBoardProjectionWithOptions(items, directions, true)
}

// buildTodoBoardProjectionWithOptions deterministically computes the complete server-owned board projection tree
// for GET /api/v1/todos under todos_v2=true. When foldChildren is false, child tasks are not folded under top-level
// parent goals and every open to-do appears flat in status_groups and sidebar_tree.
func buildTodoBoardProjectionWithOptions(items []listedTodo, directions map[string]string, foldChildren bool) *todoBoardProjection {
	metaByID := make(map[string]*todoBoardItemMeta, len(items))
	orderedIDs := make([]string, 0, len(items))

	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if _, dup := metaByID[item.ID]; dup {
			continue
		}
		sec, sub := classifyBoardSection(item)
		rank := math.MaxInt
		if item.View != nil {
			rank = item.View.Rank
		}
		itemType := strings.TrimSpace(item.ItemType)
		if itemType == "" {
			itemType = effectiveTodoItemType(item.Todo, false)
		}
		metaByID[item.ID] = &todoBoardItemMeta{
			item:        item,
			section:     sec,
			subOrder:    sub,
			viewRank:    rank,
			directionID: resolveBoardDirectionID(item, directions),
			itemType:    itemType,
		}
		orderedIDs = append(orderedIDs, item.ID)
	}

	// Sort all items deterministically before resolving folding so output never depends on input order.
	slices.SortFunc(orderedIDs, func(a, b string) int {
		return compareBoardMeta(metaByID[a], metaByID[b])
	})

	// Resolve cycle-safe top-level ancestor goal and whether each child surfaces as a blocker.
	foldedChildren := make(map[string][]string)
	surfacedChildren := make(map[string][]string)
	if foldChildren {
		for _, id := range orderedIDs {
			m := metaByID[id]
			rootID := resolveBoardRootGoalID(id, metaByID)
			if rootID == "" {
				continue
			}
			// Never fold an open child under a closed parent; open work must stay visible.
			if metaByID[rootID].section == boardSectionDone && m.section != boardSectionDone {
				continue
			}
			m.rootGoalID = rootID
			if isBoardChildSurfaced(m) {
				m.surfaced = true
				// A surfaced child with an active ask/needs_user always surfaces into needs_you.
				if m.section != boardSectionNeedsYou {
					m.section = boardSectionNeedsYou
					m.subOrder = 0
				}
				surfacedChildren[rootID] = append(surfacedChildren[rootID], id)
			} else {
				foldedChildren[rootID] = append(foldedChildren[rootID], id)
			}
		}
	}

	// Re-sort after any surfaced child promotion so intra-section ranks remain strictly deterministic.
	slices.SortFunc(orderedIDs, func(a, b string) int {
		return compareBoardMeta(metaByID[a], metaByID[b])
	})

	// Re-sort per-parent child lists deterministically.
	for rootID := range foldedChildren {
		slices.SortFunc(foldedChildren[rootID], func(a, b string) int {
			return compareBoardMeta(metaByID[a], metaByID[b])
		})
	}
	for rootID := range surfacedChildren {
		slices.SortFunc(surfacedChildren[rootID], func(a, b string) int {
			return compareBoardMeta(metaByID[a], metaByID[b])
		})
	}

	// 1. Build status_groups and global counts.
	rawBySection := make(map[string]int, len(boardSectionOrder))
	entriesBySection := make(map[string][]todoBoardEntry, len(boardSectionOrder))
	for _, key := range boardSectionOrder {
		entriesBySection[key] = []todoBoardEntry{}
	}

	for _, id := range orderedIDs {
		m := metaByID[id]
		rawBySection[m.section]++
		// Folded non-surfaced children live inside their top-level parent entry.
		if m.rootGoalID != "" && !m.surfaced {
			continue
		}
		entry := todoBoardEntry{
			TodoID: id,
		}
		if fc := foldedChildren[id]; len(fc) > 0 {
			entry.FoldedChildIDs = fc
			entry.FoldedCount = len(fc)
		}
		if sc := surfacedChildren[id]; len(sc) > 0 {
			entry.SurfacedChildIDs = sc
		}
		entriesBySection[m.section] = append(entriesBySection[m.section], entry)
	}

	statusGroups := make([]todoBoardStatusGroup, 0, len(boardSectionOrder))
	for _, key := range boardSectionOrder {
		entries := entriesBySection[key]
		statusGroups = append(statusGroups, todoBoardStatusGroup{
			Key:          key,
			TotalCount:   rawBySection[key],
			VisibleCount: len(entries),
			Entries:      entries,
		})
	}

	counts := todoBoardCounts{
		NeedsYou:      rawBySection[boardSectionNeedsYou],
		InProgress:    rawBySection[boardSectionInProgress],
		PendingReview: rawBySection[boardSectionPendingReview],
		Waiting:       rawBySection[boardSectionWaiting],
		Someday:       rawBySection[boardSectionSomeday],
		Done:          rawBySection[boardSectionDone],
	}
	counts.OpenTotal = counts.NeedsYou + counts.InProgress + counts.PendingReview + counts.Waiting

	// 2. Build direction_groups.
	dirBuckets := make(map[string][]*todoBoardItemMeta)
	for _, id := range orderedIDs {
		m := metaByID[id]
		dirBuckets[m.directionID] = append(dirBuckets[m.directionID], m)
	}

	directionGroups := make([]todoBoardDirectionGroup, 0, len(dirBuckets))
	for dirID, bucket := range dirBuckets {
		slices.SortFunc(bucket, compareBoardDirectionMember)
		var dc todoBoardDirectionCounts
		todoIDs := make([]string, 0, len(bucket))
		for _, m := range bucket {
			todoIDs = append(todoIDs, m.item.ID)
			dc.Total++
			switch m.section {
			case boardSectionNeedsYou:
				dc.NeedsYou++
			case boardSectionInProgress:
				dc.InProgress++
			case boardSectionPendingReview:
				dc.PendingReview++
			case boardSectionWaiting:
				dc.Waiting++
			case boardSectionDone:
				dc.Done++
			}
		}
		label := ""
		if dirID != "" && directions != nil {
			label = strings.TrimSpace(directions[dirID])
		}
		directionGroups = append(directionGroups, todoBoardDirectionGroup{
			DirectionID: dirID,
			Label:       label,
			Counts:      dc,
			TodoIDs:     todoIDs,
		})
	}

	slices.SortFunc(directionGroups, compareBoardDirectionGroup)

	// 3. Build sidebar_tree (open top-level items + surfaced blockers, ordered by urgency).
	sidebarTree := make([]todoBoardSidebarNode, 0)
	for _, id := range orderedIDs {
		m := metaByID[id]
		if !isBoardOpenSection(m.section) {
			continue
		}
		if m.rootGoalID != "" && !m.surfaced {
			continue
		}
		fc := foldedChildren[id]
		sc := surfacedChildren[id]
		needsYouCount := 0
		activeCount := 0
		if m.section == boardSectionNeedsYou {
			needsYouCount++
		}
		if m.section == boardSectionInProgress || m.section == boardSectionPendingReview {
			activeCount++
		}
		for _, cid := range fc {
			cm := metaByID[cid]
			if cm.section == boardSectionNeedsYou {
				needsYouCount++
			}
			if cm.section == boardSectionInProgress || cm.section == boardSectionPendingReview {
				activeCount++
			}
		}
		for _, cid := range sc {
			cm := metaByID[cid]
			if cm.section == boardSectionNeedsYou {
				needsYouCount++
			}
			if cm.section == boardSectionInProgress || cm.section == boardSectionPendingReview {
				activeCount++
			}
		}
		node := todoBoardSidebarNode{
			TodoID:        id,
			Section:       m.section,
			ItemType:      m.itemType,
			DirectionID:   m.directionID,
			FoldedCount:   len(fc),
			NeedsYouCount: needsYouCount,
			ActiveCount:   activeCount,
		}
		if len(fc) > 0 {
			node.ChildIDs = fc
		}
		if len(sc) > 0 {
			node.SurfacedChildIDs = sc
		}
		sidebarTree = append(sidebarTree, node)
	}

	return &todoBoardProjection{
		StatusGroups:    statusGroups,
		DirectionGroups: directionGroups,
		SidebarTree:     sidebarTree,
		Counts:          counts,
	}
}

func boardSectionPriority(section string) int {
	switch section {
	case boardSectionNeedsYou:
		return 0
	case boardSectionInProgress:
		return 1
	case boardSectionPendingReview:
		return 2
	case boardSectionWaiting:
		return 3
	case boardSectionSomeday:
		return 4
	case boardSectionDone:
		return 5
	default:
		return 6
	}
}

func isBoardOpenSection(section string) bool {
	switch section {
	case boardSectionNeedsYou, boardSectionInProgress, boardSectionPendingReview, boardSectionWaiting:
		return true
	default:
		return false
	}
}

// classifyBoardSection maps a listedTodo to one of the 6 board sections and a deterministic
// sub-section priority, respecting the server-computed todo_view when present.
func classifyBoardSection(t listedTodo) (section string, subOrder int) {
	if v := t.View; v != nil {
		switch {
		case v.Section == assistantwire.TodoListSectionNeedsYou || v.Phase == assistantwire.TodoPhaseNeedsYou:
			return boardSectionNeedsYou, 0
		case v.Section == assistantwire.TodoListSectionCanWait:
			return boardSectionNeedsYou, 1
		case v.Phase == assistantwire.TodoPhaseDone || v.Section == assistantwire.TodoListSectionDone:
			if v.Section == assistantwire.TodoListSectionJustFinished {
				return boardSectionDone, 0
			}
			return boardSectionDone, 1
		case v.Section == assistantwire.TodoListSectionSomeday || v.Phase == assistantwire.TodoPhaseSomeday:
			return boardSectionSomeday, 0
		case t.Supervision != nil && t.Supervision.State == reviewPending:
			return boardSectionPendingReview, 0
		case v.Section == assistantwire.TodoListSectionJustFinished:
			return boardSectionPendingReview, 1
		case v.Section == assistantwire.TodoListSectionWorking || v.Phase == assistantwire.TodoPhaseWorking:
			return boardSectionInProgress, 0
		case v.Section == assistantwire.TodoListSectionWaiting || v.Phase == assistantwire.TodoPhaseWaiting:
			return boardSectionWaiting, 0
		default:
			return boardSectionWaiting, 1
		}
	}

	// Fallback for direct unit-test inputs without precomputed View.
	switch {
	case t.DoneAt != nil && t.StartError == "":
		return boardSectionDone, 1
	case t.SomedayUntil != nil:
		return boardSectionSomeday, 0
	case t.StartError != "" || t.Ask != nil || (t.Supervision != nil && t.Supervision.State == reviewNeedsUser):
		return boardSectionNeedsYou, 0
	case t.Supervision != nil && t.Supervision.State == reviewPending:
		return boardSectionPendingReview, 0
	case t.Supervision != nil && t.Supervision.State == reviewParked,
		t.Wait != nil,
		repoGateOpenNonDraftPR(t.RepoGate):
		return boardSectionWaiting, 0
	default:
		for _, a := range t.Agents {
			if a.ReleasedAt == nil && a.DismissedAt == nil {
				return boardSectionInProgress, 0
			}
		}
		return boardSectionWaiting, 1
	}
}

// resolveBoardRootGoalID walks part_of to the highest ancestor present in metaByID.
// Returns "" for top-level items, orphan children, self-references, or cycles.
func resolveBoardRootGoalID(id string, metaByID map[string]*todoBoardItemMeta) string {
	start := metaByID[id]
	if start == nil {
		return ""
	}
	parent := strings.TrimSpace(start.item.PartOf)
	if parent == "" || parent == id || metaByID[parent] == nil {
		return ""
	}
	visited := map[string]bool{id: true}
	cur := parent
	for cur != "" {
		if visited[cur] {
			return ""
		}
		visited[cur] = true
		node := metaByID[cur]
		if node == nil {
			return ""
		}
		next := strings.TrimSpace(node.item.PartOf)
		if next == "" || next == cur || metaByID[next] == nil {
			return cur
		}
		cur = next
	}
	return ""
}

// isBoardChildSurfaced reports whether a child task needs user decision or human intervention
// and must surface out of its parent goal fold.
func isBoardChildSurfaced(m *todoBoardItemMeta) bool {
	if m.section == boardSectionNeedsYou {
		return true
	}
	if m.section == boardSectionDone {
		return false
	}
	if m.item.Ask != nil {
		return true
	}
	if m.item.Supervision != nil && m.item.Supervision.State == reviewNeedsUser {
		return true
	}
	if m.item.StartError != "" {
		return true
	}
	return false
}

// resolveBoardDirectionID prefers explicit direction_id, falling back to origin or supervisor
// when it matches a known direction; everything else is ungrouped ("").
func resolveBoardDirectionID(t listedTodo, directions map[string]string) string {
	if dirID := strings.TrimSpace(t.DirectionID); dirID != "" {
		return dirID
	}
	if len(directions) == 0 {
		return ""
	}
	if origin := strings.TrimSpace(t.Origin); origin != "" {
		if _, ok := directions[origin]; ok {
			return origin
		}
	}
	if t.Supervision != nil {
		if sup := strings.TrimSpace(t.Supervision.Supervisor); sup != "" {
			if _, ok := directions[sup]; ok {
				return sup
			}
		}
	}
	return ""
}

func compareBoardMeta(a, b *todoBoardItemMeta) int {
	return cmp.Or(
		cmp.Compare(boardSectionPriority(a.section), boardSectionPriority(b.section)),
		cmp.Compare(a.subOrder, b.subOrder),
		cmp.Compare(a.viewRank, b.viewRank),
		b.item.CreatedAt.Compare(a.item.CreatedAt),
		strings.Compare(a.item.ID, b.item.ID),
	)
}

func compareBoardDirectionMember(a, b *todoBoardItemMeta) int {
	aIsChild := a.rootGoalID != ""
	bIsChild := b.rootGoalID != ""
	childOrder := 0
	if !aIsChild && bIsChild {
		childOrder = -1
	} else if aIsChild && !bIsChild {
		childOrder = 1
	}
	return cmp.Or(
		cmp.Compare(boardSectionPriority(a.section), boardSectionPriority(b.section)),
		childOrder,
		cmp.Compare(a.subOrder, b.subOrder),
		cmp.Compare(a.viewRank, b.viewRank),
		b.item.CreatedAt.Compare(a.item.CreatedAt),
		strings.Compare(a.item.ID, b.item.ID),
	)
}

func compareBoardDirectionGroup(a, b todoBoardDirectionGroup) int {
	// Ungrouped ("") is always sorted last.
	if a.DirectionID == "" && b.DirectionID != "" {
		return 1
	}
	if a.DirectionID != "" && b.DirectionID == "" {
		return -1
	}
	aHasNeeds := a.Counts.NeedsYou > 0
	bHasNeeds := b.Counts.NeedsYou > 0
	if aHasNeeds != bHasNeeds {
		if aHasNeeds {
			return -1
		}
		return 1
	}
	if aHasNeeds && a.Counts.NeedsYou != b.Counts.NeedsYou {
		return cmp.Compare(b.Counts.NeedsYou, a.Counts.NeedsYou)
	}
	aHasActive := a.Counts.InProgress > 0
	bHasActive := b.Counts.InProgress > 0
	if aHasActive != bHasActive {
		if aHasActive {
			return -1
		}
		return 1
	}
	if aHasActive && a.Counts.InProgress != b.Counts.InProgress {
		return cmp.Compare(b.Counts.InProgress, a.Counts.InProgress)
	}
	return cmp.Or(
		cmp.Compare(b.Counts.Total, a.Counts.Total),
		strings.Compare(a.DirectionID, b.DirectionID),
	)
}
