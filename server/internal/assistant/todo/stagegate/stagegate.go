// Package stagegate evaluates task dependency unlock readiness for part_of / after edges
// under two modes: default completion/acceptance unlock and todos_v2 contract/stage unlock.
//
// It is pure: no IO, no clock, no network, and no dependencies outside the Go standard library.
//
// Unlock modes:
//   - UnlockOnDone (default): unlocks when the prerequisite is not cancelled and Done == true.
//   - UnlockOnAccepted: unlocks when the prerequisite is not cancelled and Accepted == true
//     (aligned with existing RequireAcceptedAfter semantics).
//   - UnlockOnStage: active only when todosV2 == true; unlocks early when the prerequisite
//     reaches or passes RequiredStage in the ordered built-in stage lattice
//     (plan < contract_ready < develop < pr_open < merged < done) and/or declares all
//     RequiredContracts (e.g. api_contract, schema_frozen). When todosV2 == false, stage
//     edges losslessly fall back to UnlockOnDone (or UnlockOnAccepted when RequireAccepted
//     or FallbackMode requires acceptance) with zero behavioral drift.
//
// Safety and determinism guarantees:
//   - Cancelled prerequisites always block downstream tasks (StatusBlockedByCancelled) and
//     are never unlocked even if Done, Accepted, Stage, or Contracts are set.
//   - Self-loops and multi-hop dependency cycles are detected deterministically without
//     infinite recursion (StatusCycleDetected with canonical CyclePath).
//   - Missing prerequisites, unknown stages, and invalid unlock modes fail closed with
//     explicit structured reasons.
//   - All output slices are deterministically ordered regardless of input slice order.
package stagegate

import (
	"cmp"
	"slices"
	"strings"
)

// UnlockMode defines how a single dependency edge determines whether its prerequisite is satisfied.
type UnlockMode string

const (
	// UnlockOnDone unlocks when the prerequisite is not cancelled and Done == true.
	UnlockOnDone UnlockMode = "done"
	// UnlockOnAccepted unlocks when the prerequisite is not cancelled and Accepted == true.
	UnlockOnAccepted UnlockMode = "accepted"
	// UnlockOnStage unlocks early under todos_v2=true when the prerequisite reaches or passes
	// RequiredStage and/or declares all RequiredContracts.
	UnlockOnStage UnlockMode = "stage"
)

// Stage represents a built-in ordered lifecycle milestone for a task.
type Stage string

const (
	StagePlan          Stage = "plan"
	StageContractReady Stage = "contract_ready"
	StageDevelop       Stage = "develop"
	StagePROpen        Stage = "pr_open"
	StageMerged        Stage = "merged"
	StageDone          Stage = "done"
)

// Status summarizes the dependency gate outcome for a task node.
type Status string

const (
	// StatusReady indicates all prerequisites are satisfied and the task may start in parallel.
	StatusReady Status = "ready"
	// StatusWaiting indicates at least one prerequisite is not yet satisfied.
	StatusWaiting Status = "waiting"
	// StatusBlockedByCancelled indicates at least one required prerequisite was cancelled
	// and requires human or supervisor intervention.
	StatusBlockedByCancelled Status = "blocked_by_cancelled"
	// StatusCycleDetected indicates a self-loop or multi-hop dependency cycle is reachable
	// from this task.
	StatusCycleDetected Status = "cycle_detected"
)

// Structured reasons reported on UnsatisfiedDep.
const (
	ReasonMissingPrerequisite  = "missing_prerequisite"
	ReasonPrereqCancelled      = "prerequisite_cancelled"
	ReasonNotDone              = "not_done"
	ReasonNotAccepted          = "not_accepted"
	ReasonStageNotReached      = "stage_not_reached"
	ReasonMissingContracts     = "missing_contracts"
	ReasonUnknownRequiredStage = "unknown_required_stage"
	ReasonUnknownCurrentStage  = "unknown_current_stage"
	ReasonInvalidUnlockMode    = "invalid_unlock_mode"
	ReasonTargetNotFound       = "target_not_found"
)

// Edge describes a single prerequisite dependency (after edge) of a task node.
type Edge struct {
	PrereqID          string     `json:"prereq_id"`
	Mode              UnlockMode `json:"mode,omitempty"`
	FallbackMode      UnlockMode `json:"fallback_mode,omitempty"`
	RequiredStage     Stage      `json:"required_stage,omitempty"`
	RequiredContracts []string   `json:"required_contracts,omitempty"`
	RequireAccepted   bool       `json:"require_accepted,omitempty"`
}

// Node describes a task in the dependency graph and its current completion/stage/contract state.
type Node struct {
	TodoID    string   `json:"todo_id"`
	Stage     Stage    `json:"stage,omitempty"`
	Contracts []string `json:"contracts,omitempty"`
	Done      bool     `json:"done,omitempty"`
	Accepted  bool     `json:"accepted,omitempty"`
	Cancelled bool     `json:"cancelled,omitempty"`
	After     []Edge   `json:"after,omitempty"`
}

// UnsatisfiedDep explains why a specific prerequisite edge is currently blocking a task.
type UnsatisfiedDep struct {
	PrereqID         string     `json:"prereq_id"`
	EffectiveMode    UnlockMode `json:"effective_mode"`
	Reason           string     `json:"reason"`
	RequiredStage    Stage      `json:"required_stage,omitempty"`
	CurrentStage     Stage      `json:"current_stage,omitempty"`
	MissingContracts []string   `json:"missing_contracts,omitempty"`
}

// Evaluation is the deterministic dependency gate verdict for a single task.
type Evaluation struct {
	TodoID        string           `json:"todo_id"`
	Status        Status           `json:"status"`
	Ready         bool             `json:"ready"`
	EffectiveMode UnlockMode       `json:"effective_mode,omitempty"`
	Unsatisfied   []UnsatisfiedDep `json:"unsatisfied,omitempty"`
	CyclePath     []string         `json:"cycle_path,omitempty"`
}

// NormalizeStage canonicalizes a stage name (trimmed, lowercased) and reports whether it is
// one of the built-in ordered stages.
func NormalizeStage(raw string) (Stage, bool) {
	s := Stage(strings.ToLower(strings.TrimSpace(raw)))
	if _, ok := StageRank(s); !ok {
		return "", false
	}
	return s, true
}

// StageRank returns the monotonic ordinal (1..6) of a built-in stage, or (0, false) if unknown.
func StageRank(s Stage) (int, bool) {
	switch Stage(strings.ToLower(strings.TrimSpace(string(s)))) {
	case StagePlan:
		return 1, true
	case StageContractReady:
		return 2, true
	case StageDevelop:
		return 3, true
	case StagePROpen:
		return 4, true
	case StageMerged:
		return 5, true
	case StageDone:
		return 6, true
	default:
		return 0, false
	}
}

// StageSatisfies reports whether current has reached or passed required in the built-in stage order.
// Unknown or empty stages never satisfy any requirement.
func StageSatisfies(current, required Stage) bool {
	curRank, okCur := StageRank(current)
	reqRank, okReq := StageRank(required)
	if !okCur || !okReq {
		return false
	}
	return curRank >= reqRank
}

// NormalizeContract canonicalizes a contract tag by trimming whitespace and lowercasing.
func NormalizeContract(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", false
	}
	return s, true
}

// NormalizeContracts canonicalizes, deduplicates, and sorts a slice of contract tags.
func NormalizeContracts(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		norm, ok := NormalizeContract(r)
		if !ok {
			continue
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}

// CompletionEdges builds legacy completion/acceptance Edges from a plain slice of prerequisite
// todo IDs, preserving 100% compatibility with existing after slices.
// Empty/whitespace IDs are ignored and duplicates are removed in deterministic ascending order.
func CompletionEdges(ids []string, requireAccepted bool) []Edge {
	if len(ids) == 0 {
		return nil
	}
	mode := UnlockOnDone
	if requireAccepted {
		mode = UnlockOnAccepted
	}
	seen := make(map[string]struct{}, len(ids))
	clean := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return nil
	}
	slices.Sort(clean)
	edges := make([]Edge, 0, len(clean))
	for _, id := range clean {
		edges = append(edges, Edge{
			PrereqID:        id,
			Mode:            mode,
			RequireAccepted: requireAccepted,
		})
	}
	return edges
}

// FallbackMode returns the completion unlock mode used when todosV2 is false (or when an
// UnlockOnStage edge has no stage/contract criteria). RequireAccepted always enforces
// UnlockOnAccepted.
func FallbackMode(e Edge) UnlockMode {
	if e.RequireAccepted {
		return UnlockOnAccepted
	}
	switch UnlockMode(strings.ToLower(strings.TrimSpace(string(e.FallbackMode)))) {
	case UnlockOnAccepted:
		return UnlockOnAccepted
	case UnlockOnDone, "":
		return UnlockOnDone
	default:
		return UnlockOnDone
	}
}

// EffectiveMode resolves the unlock mode that will actually be applied to e given todosV2.
// Returns ("", false) when e.Mode is an unrecognized non-empty mode string.
func EffectiveMode(e Edge, todosV2 bool) (UnlockMode, bool) {
	rawMode := UnlockMode(strings.ToLower(strings.TrimSpace(string(e.Mode))))
	switch rawMode {
	case "":
		if e.RequireAccepted {
			return UnlockOnAccepted, true
		}
		return UnlockOnDone, true
	case UnlockOnDone:
		if e.RequireAccepted {
			return UnlockOnAccepted, true
		}
		return UnlockOnDone, true
	case UnlockOnAccepted:
		return UnlockOnAccepted, true
	case UnlockOnStage:
		if !todosV2 {
			return FallbackMode(e), true
		}
		reqStageRaw := strings.TrimSpace(string(e.RequiredStage))
		reqContracts := NormalizeContracts(e.RequiredContracts)
		if reqStageRaw == "" && len(reqContracts) == 0 {
			return FallbackMode(e), true
		}
		return UnlockOnStage, true
	default:
		return rawMode, false
	}
}

type normalizedEdge struct {
	prereqID          string
	rawMode           UnlockMode
	fallbackMode      UnlockMode
	requiredStageRaw  string
	requiredContracts []string
	requireAccepted   bool
}

type normalizedNode struct {
	todoID       string
	rawStage     string
	contractsSet map[string]struct{}
	done         bool
	accepted     bool
	cancelled    bool
	edges        []normalizedEdge
}

func buildGraph(nodes []Node) (map[string]normalizedNode, []string) {
	byID := make(map[string]normalizedNode, len(nodes))
	for _, n := range nodes {
		id := strings.TrimSpace(n.TodoID)
		if id == "" {
			continue
		}
		contracts := NormalizeContracts(n.Contracts)
		var cset map[string]struct{}
		if len(contracts) > 0 {
			cset = make(map[string]struct{}, len(contracts))
			for _, c := range contracts {
				cset[c] = struct{}{}
			}
		}

		edges := make([]normalizedEdge, 0, len(n.After))
		for _, e := range n.After {
			pid := strings.TrimSpace(e.PrereqID)
			if pid == "" {
				continue
			}
			edges = append(edges, normalizedEdge{
				prereqID:          pid,
				rawMode:           e.Mode,
				fallbackMode:      e.FallbackMode,
				requiredStageRaw:  strings.TrimSpace(string(e.RequiredStage)),
				requiredContracts: NormalizeContracts(e.RequiredContracts),
				requireAccepted:   e.RequireAccepted,
			})
		}
		slices.SortFunc(edges, func(a, b normalizedEdge) int {
			return cmp.Or(
				strings.Compare(a.prereqID, b.prereqID),
				strings.Compare(string(a.rawMode), string(b.rawMode)),
				strings.Compare(a.requiredStageRaw, b.requiredStageRaw),
				strings.Compare(strings.Join(a.requiredContracts, ","), strings.Join(b.requiredContracts, ",")),
			)
		})

		byID[id] = normalizedNode{
			todoID:       id,
			rawStage:     strings.TrimSpace(string(n.Stage)),
			contractsSet: cset,
			done:         n.Done,
			accepted:     n.Accepted,
			cancelled:    n.Cancelled,
			edges:        edges,
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return byID, ids
}

// findCycle detects any dependency cycle reachable from startID using deterministic DFS.
// Returned path starts and ends at the repeated node (e.g. ["a", "a"] or ["a", "b", "a"]).
func findCycle(graph map[string]normalizedNode, startID string) []string {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(graph))
	stack := make([]string, 0, 8)
	stackIdx := make(map[string]int, 8)

	var dfs func(id string) []string
	dfs = func(id string) []string {
		node, ok := graph[id]
		if !ok {
			return nil
		}
		state[id] = visiting
		stackIdx[id] = len(stack)
		stack = append(stack, id)

		// Deduplicate outgoing prereq IDs while preserving sorted order.
		prevPID := ""
		for _, e := range node.edges {
			pid := e.prereqID
			if pid == prevPID {
				continue
			}
			prevPID = pid
			if state[pid] == visiting {
				idx := stackIdx[pid]
				cycle := make([]string, 0, len(stack)-idx+1)
				cycle = append(cycle, stack[idx:]...)
				cycle = append(cycle, pid)
				return cycle
			}
			if state[pid] == unvisited {
				if cycle := dfs(pid); len(cycle) > 0 {
					return cycle
				}
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackIdx, id)
		state[id] = visited
		return nil
	}

	return dfs(startID)
}

func summarizeEffectiveMode(edges []normalizedEdge, todosV2 bool) UnlockMode {
	if len(edges) == 0 {
		return UnlockOnDone
	}
	hasStage := false
	hasAccepted := false
	for _, ne := range edges {
		mode, ok := EffectiveMode(Edge{
			PrereqID:          ne.prereqID,
			Mode:              ne.rawMode,
			FallbackMode:      ne.fallbackMode,
			RequiredStage:     Stage(ne.requiredStageRaw),
			RequiredContracts: ne.requiredContracts,
			RequireAccepted:   ne.requireAccepted,
		}, todosV2)
		if !ok {
			continue
		}
		switch mode {
		case UnlockOnStage:
			hasStage = true
		case UnlockOnAccepted:
			hasAccepted = true
		}
	}
	if hasStage {
		return UnlockOnStage
	}
	if hasAccepted {
		return UnlockOnAccepted
	}
	return UnlockOnDone
}

func evaluateNode(graph map[string]normalizedNode, todoID string, todosV2 bool) Evaluation {
	id := strings.TrimSpace(todoID)
	node, exists := graph[id]
	if !exists {
		return Evaluation{
			TodoID: id,
			Status: StatusWaiting,
			Ready:  false,
			Unsatisfied: []UnsatisfiedDep{{
				PrereqID:      id,
				EffectiveMode: UnlockOnDone,
				Reason:        ReasonTargetNotFound,
			}},
		}
	}

	effSummary := summarizeEffectiveMode(node.edges, todosV2)

	if cycle := findCycle(graph, id); len(cycle) > 0 {
		return Evaluation{
			TodoID:        id,
			Status:        StatusCycleDetected,
			Ready:         false,
			EffectiveMode: effSummary,
			CyclePath:     cycle,
		}
	}

	if len(node.edges) == 0 {
		return Evaluation{
			TodoID:        id,
			Status:        StatusReady,
			Ready:         true,
			EffectiveMode: effSummary,
		}
	}

	var unsatisfied []UnsatisfiedDep
	blockedByCancelled := false

	for _, ne := range node.edges {
		edge := Edge{
			PrereqID:          ne.prereqID,
			Mode:              ne.rawMode,
			FallbackMode:      ne.fallbackMode,
			RequiredStage:     Stage(ne.requiredStageRaw),
			RequiredContracts: ne.requiredContracts,
			RequireAccepted:   ne.requireAccepted,
		}
		effMode, modeValid := EffectiveMode(edge, todosV2)
		if !modeValid {
			unsatisfied = append(unsatisfied, UnsatisfiedDep{
				PrereqID:      ne.prereqID,
				EffectiveMode: effMode,
				Reason:        ReasonInvalidUnlockMode,
			})
			continue
		}

		prereq, ok := graph[ne.prereqID]
		if !ok {
			unsatisfied = append(unsatisfied, UnsatisfiedDep{
				PrereqID:      ne.prereqID,
				EffectiveMode: effMode,
				Reason:        ReasonMissingPrerequisite,
			})
			continue
		}

		if prereq.cancelled {
			blockedByCancelled = true
			unsatisfied = append(unsatisfied, UnsatisfiedDep{
				PrereqID:      ne.prereqID,
				EffectiveMode: effMode,
				Reason:        ReasonPrereqCancelled,
			})
			continue
		}

		switch effMode {
		case UnlockOnDone:
			if !prereq.done {
				unsatisfied = append(unsatisfied, UnsatisfiedDep{
					PrereqID:      ne.prereqID,
					EffectiveMode: UnlockOnDone,
					Reason:        ReasonNotDone,
				})
			}

		case UnlockOnAccepted:
			if !prereq.accepted {
				unsatisfied = append(unsatisfied, UnsatisfiedDep{
					PrereqID:      ne.prereqID,
					EffectiveMode: UnlockOnAccepted,
					Reason:        ReasonNotAccepted,
				})
			}

		case UnlockOnStage:
			if dep, blocked := evaluateStageEdge(ne, prereq); blocked {
				unsatisfied = append(unsatisfied, dep)
			}
		}
	}

	if len(unsatisfied) == 0 {
		return Evaluation{
			TodoID:        id,
			Status:        StatusReady,
			Ready:         true,
			EffectiveMode: effSummary,
		}
	}

	slices.SortFunc(unsatisfied, func(a, b UnsatisfiedDep) int {
		return cmp.Or(
			strings.Compare(a.PrereqID, b.PrereqID),
			strings.Compare(a.Reason, b.Reason),
			strings.Compare(string(a.RequiredStage), string(b.RequiredStage)),
			strings.Compare(string(a.EffectiveMode), string(b.EffectiveMode)),
		)
	})

	status := StatusWaiting
	if blockedByCancelled {
		status = StatusBlockedByCancelled
	}
	return Evaluation{
		TodoID:        id,
		Status:        status,
		Ready:         false,
		EffectiveMode: effSummary,
		Unsatisfied:   unsatisfied,
	}
}

func evaluateStageEdge(ne normalizedEdge, prereq normalizedNode) (UnsatisfiedDep, bool) {
	var missingContracts []string
	for _, req := range ne.requiredContracts {
		if _, ok := prereq.contractsSet[req]; !ok {
			missingContracts = append(missingContracts, req)
		}
	}

	var reqStage Stage
	if ne.requiredStageRaw != "" {
		normReq, ok := NormalizeStage(ne.requiredStageRaw)
		if !ok {
			return UnsatisfiedDep{
				PrereqID:         ne.prereqID,
				EffectiveMode:    UnlockOnStage,
				Reason:           ReasonUnknownRequiredStage,
				RequiredStage:    Stage(ne.requiredStageRaw),
				MissingContracts: missingContracts,
			}, true
		}
		reqStage = normReq

		var curStage Stage
		if prereq.rawStage != "" {
			normCur, okCur := NormalizeStage(prereq.rawStage)
			if !okCur {
				return UnsatisfiedDep{
					PrereqID:         ne.prereqID,
					EffectiveMode:    UnlockOnStage,
					Reason:           ReasonUnknownCurrentStage,
					RequiredStage:    reqStage,
					CurrentStage:     Stage(prereq.rawStage),
					MissingContracts: missingContracts,
				}, true
			}
			curStage = normCur
		} else if prereq.done {
			// Completed prerequisites without an explicit stage field satisfy all built-in stages.
			curStage = StageDone
		}

		if !StageSatisfies(curStage, reqStage) {
			return UnsatisfiedDep{
				PrereqID:         ne.prereqID,
				EffectiveMode:    UnlockOnStage,
				Reason:           ReasonStageNotReached,
				RequiredStage:    reqStage,
				CurrentStage:     curStage,
				MissingContracts: missingContracts,
			}, true
		}
	}

	if len(missingContracts) > 0 {
		var curStage Stage
		if normCur, ok := NormalizeStage(prereq.rawStage); ok {
			curStage = normCur
		} else if prereq.rawStage == "" && prereq.done {
			curStage = StageDone
		}
		return UnsatisfiedDep{
			PrereqID:         ne.prereqID,
			EffectiveMode:    UnlockOnStage,
			Reason:           ReasonMissingContracts,
			RequiredStage:    reqStage,
			CurrentStage:     curStage,
			MissingContracts: missingContracts,
		}, true
	}

	return UnsatisfiedDep{}, false
}

// Evaluate computes the deterministic dependency unlock evaluation for todoID within nodes.
// When todosV2 is false, all UnlockOnStage edges losslessly fall back to completion/acceptance unlock.
func Evaluate(nodes []Node, todoID string, todosV2 bool) Evaluation {
	graph, _ := buildGraph(nodes)
	return evaluateNode(graph, todoID, todosV2)
}

// EvaluateAll evaluates every node in nodes and returns Evaluations sorted by TodoID ascending.
// Nodes with empty/whitespace TodoID are ignored.
func EvaluateAll(nodes []Node, todosV2 bool) []Evaluation {
	graph, ids := buildGraph(nodes)
	if len(ids) == 0 {
		return nil
	}
	out := make([]Evaluation, 0, len(ids))
	for _, id := range ids {
		out = append(out, evaluateNode(graph, id, todosV2))
	}
	return out
}

// ReadyIDs returns the sorted, deduplicated TodoIDs of all evaluations with StatusReady.
func ReadyIDs(evals []Evaluation) []string {
	if len(evals) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(evals))
	out := make([]string, 0, len(evals))
	for _, ev := range evals {
		if !ev.Ready || ev.Status != StatusReady {
			continue
		}
		id := strings.TrimSpace(ev.TodoID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}
