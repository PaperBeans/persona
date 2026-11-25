package assistant

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/deeplink"
	"github.com/nwverse/persona/server/internal/proto"
)

const orgModelPagesMax = 20

func (b *organizationBuild) readModelSources() {
	planIDs := map[string]bool{}
	for _, todo := range b.tb.todos {
		if todo.PlanID != "" {
			planIDs[todo.PlanID] = true
		}
	}
	if store := b.s.store(); store != nil && len(planIDs) > 0 {
		ids := make([]string, 0, len(planIDs))
		for id := range planIDs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if rows, err := store.TaskPlansByID(b.ctx, ids); err == nil {
			for _, row := range rows {
				for _, task := range planFromJSON(row.Tasks) {
					if task.Tier == "" {
						continue
					}
					if task.TodoID != "" {
						b.presetByTodo[task.TodoID] = task.Tier
					}
					if task.ReservedTodoID != "" {
						b.presetByTodo[task.ReservedTodoID] = task.Tier
					}
				}
			}
		}
	}
	for _, work := range b.work {
		if !b.activeInOwnerDay(work) {
			b.modelRead[work.node.ID] = orgObserved(orgNotApplicable, "not_active_in_owner_day", "runtime_activity", nil)
			continue
		}
		b.readWorkModels(work)
	}
	for _, node := range b.nodeOrder {
		if node.Kind != "main" && node.Kind != "direction" {
			continue
		}
		raw := b.cfg.Main
		if node.Kind == "direction" {
			raw = strings.TrimPrefix(node.ID, "direction:")
		}
		machine := b.conversationMachine(raw)
		work := &orgWork{key: [2]string{machine, raw}, node: node, session: b.sessionForConversation(raw)}
		if b.activeInOwnerDay(work) {
			b.readWorkModels(work)
		}
	}
}

func (b *organizationBuild) ownerDayBounds() (time.Time, time.Time) {
	location := b.s.location(b.ctx)
	start := dayStart(b.now.In(location))
	return start, dayStart(start.AddDate(0, 0, 1))
}

func orgWithinDay(at, start, end time.Time) bool {
	return !at.IsZero() && !at.Before(start) && at.Before(end)
}

func (b *organizationBuild) activeInOwnerDay(work *orgWork) bool {
	start, end := b.ownerDayBounds()
	if work.session != nil {
		if orgWithinDay(work.session.UpdatedAt, start, end) || work.session.State == proto.StateWorking ||
			work.session.State == proto.StateNeedsYou || work.session.Status == proto.StatusWorking {
			return true
		}
	}
	if work.subagent != nil {
		if orgWithinDay(work.subagent.StartedAt, start, end) || orgWithinDay(work.subagent.UpdatedAt, start, end) || work.subagent.Running {
			return true
		}
	}
	for _, id := range work.todoIDs {
		t, ok := b.tb.todos[id]
		if !ok {
			continue
		}
		if orgWithinDay(t.UpdatedAt, start, end) {
			return true
		}
		for _, agent := range t.Agents {
			if agent.MachineID == work.key[0] && agent.SessionID == work.key[1] && orgWithinDay(agent.LinkedAt, start, end) {
				return true
			}
		}
	}
	return false
}

func (b *organizationBuild) readWorkModels(work *orgWork) {
	node := work.node
	_, _, requested, effort, preset := b.runtimeFields(work)
	items, availability := b.readTranscriptModels(work.key[0], work.key[1])
	b.modelRead[node.ID] = availability
	observations := make([]orgModelObservation, 0, len(items))
	for _, item := range items {
		at := orgTime(item.TS)
		observations = append(observations, orgModelObservation{
			NodeID: node.ID, ItemID: item.ID, Seq: item.Seq, ObservedAt: at,
			Availability: orgObserved(orgAvailable, "", "transcript_item", at),
			Model:        orgValue(item.Model, "transcript_item", at), RequestedModel: requested, Effort: effort, Preset: preset,
		})
	}
	if len(observations) == 0 && work.subagent != nil && work.subagent.Model != "" {
		at := orgTime(work.subagent.UpdatedAt)
		observations = append(observations, orgModelObservation{
			NodeID: node.ID, ObservedAt: at,
			Availability:   orgObserved(orgAvailable, "", "runtime_subagent_transcript_model", at),
			Model:          orgValue(work.subagent.Model, "runtime_subagent_transcript_model", at),
			RequestedModel: requested, Effort: effort, Preset: preset,
		})
	}
	b.modelByNode[node.ID] = observations
}

// readTranscriptModels pages backward only to the owner's day boundary. It
// returns dated actual model observations; Session.Model is not treated as a
// substitute because a model switch can temporarily make it a requested pick.
func (b *organizationBuild) readTranscriptModels(machine, session string) ([]proto.Item, orgAvailability) {
	start, end := b.ownerDayBounds()
	before := 0
	complete := true
	var out []proto.Item
	for pageNumber := 0; pageNumber < orgModelPagesMax; pageNumber++ {
		params := map[string]any{"session_id": session, "limit": 500, "lazy_images": true}
		if before > 0 {
			params["before_seq"] = before
		}
		ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
		var page historyPage
		var err error
		if b.s.devs != nil {
			err = b.s.nodeCall(ctx, machine, DeviceSessionItems, params, &page)
		} else {
			err = b.s.call(ctx, machine, DeviceSessionItems, params, &page)
		}
		cancel()
		if err != nil {
			return out, orgObserved(orgUnknown, "transcript_history_unavailable", "session_items", nil)
		}
		if page.Partial {
			complete = false
		}
		oldestSeq := 0
		sawOlder := false
		for _, item := range page.Items {
			if item.Seq > 0 && (oldestSeq == 0 || item.Seq < oldestSeq) {
				oldestSeq = item.Seq
			}
			if !item.TS.IsZero() && item.TS.Before(start) {
				sawOlder = true
				continue
			}
			if item.Model != "" && orgWithinDay(item.TS, start, end) {
				out = append(out, item)
			}
		}
		if sawOlder {
			break
		}
		more := page.More || page.HasMore != nil && *page.HasMore
		if !more || len(page.Items) == 0 {
			break
		}
		if oldestSeq <= 0 || oldestSeq == before {
			complete = false
			break
		}
		before = oldestSeq
		if pageNumber == orgModelPagesMax-1 {
			complete = false
		}
	}
	slices.SortStableFunc(out, func(a, b proto.Item) int {
		return cmp.Or(a.TS.Compare(b.TS), cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.ID, b.ID))
	})
	out = slices.CompactFunc(out, func(a, b proto.Item) bool {
		return a.ID == b.ID && a.Seq == b.Seq && a.TS.Equal(b.TS) && a.Model == b.Model
	})
	if !complete {
		return out, orgObserved(orgUnknown, "transcript_history_partial", "session_items", nil)
	}
	return out, orgObserved(orgAvailable, "", "session_items", orgTime(b.now))
}

func (b *organizationBuild) applyLatestModel(node *orgNode) {
	observations := b.modelByNode[node.ID]
	for i := len(observations) - 1; i >= 0; i-- {
		observation := observations[i]
		if observation.Model.Value != nil && observation.Model.Status == orgAvailable {
			node.Model = observation.Model
			return
		}
	}
}

func (b *organizationBuild) continuation(id string) (string, string, string, string) {
	links := b.briefs.on[b.briefs.of[id]]
	link := links[id]
	superseded := b.supersededBy[id]
	if link.Continues == "" && superseded == "" {
		return "", "", "none", ""
	}
	status, reason := "linked", ""
	seen := map[string]bool{id: true}
	current := link.Continues
	for current != "" {
		if seen[current] {
			return link.Continues, superseded, "unknown", "continuation_cycle"
		}
		seen[current] = true
		next, ok := links[current]
		if !ok {
			return link.Continues, superseded, "unknown", "continuation_parent_missing"
		}
		if next.Brief != link.Brief {
			return link.Continues, superseded, "unknown", "continuation_cross_brief"
		}
		current = next.Continues
	}
	return link.Continues, superseded, status, reason
}

func (b *organizationBuild) openTodos() []orgOpenTodo {
	for brief, links := range b.briefs.on {
		_ = brief
		ids := make([]string, 0, len(links))
		for id := range links {
			ids = append(ids, id)
		}
		slices.SortFunc(ids, func(a, b string) int {
			return cmp.Or(links[a].At.Compare(links[b].At), cmp.Compare(a, b))
		})
		for _, id := range ids {
			if parent := links[id].Continues; parent != "" && b.supersededBy[parent] == "" {
				b.supersededBy[parent] = id
			}
		}
	}
	out := make([]orgOpenTodo, 0, len(b.tb.open))
	for _, id := range b.tb.open {
		t := b.tb.todos[id]
		continues, superseded, continuationStatus, continuationReason := b.continuation(id)
		scope, runtimeRaw, runtimeStatus := b.todoRuntimeScope(t)
		organizationDirection := b.organizationDirection(t, runtimeRaw, runtimeStatus)
		runtimeDirection := orgValue(runtimeRaw, runtimeStatus.Source, nil)
		if runtimeStatus.Status != orgAvailable {
			runtimeDirection.Status = runtimeStatus.Status
			runtimeDirection.Reason = runtimeStatus.Reason
			if runtimeRaw == "" {
				runtimeDirection.Value = nil
			}
		}
		availability := orgObserved(orgAvailable, "", "direction_tree_open_sop", orgTime(b.now))
		for _, candidate := range []struct{ status, reason string }{
			{runtimeStatus.Status, runtimeStatus.Reason},
			{organizationDirection.Status, organizationDirection.Reason},
			{continuationStatus, continuationReason},
		} {
			if candidate.status == orgUnknown {
				availability = orgObserved(orgUnknown, candidate.reason, "organization_projection", nil)
				break
			}
		}
		nodeIDs := []string{}
		for _, work := range b.work {
			if slices.Contains(work.todoIDs, id) {
				nodeIDs = append(nodeIDs, work.node.ID)
			}
		}
		slices.Sort(nodeIDs)
		out = append(out, orgOpenTodo{
			ID: id, Title: t.Title, State: b.tb.state(t), Section: b.tb.placed[id].Section,
			DirectionID: scope, NodeIDs: nodeIDs, Continues: continues, SupersededBy: superseded,
			ContinuationStatus: continuationStatus, ContinuationReason: continuationReason,
			OrganizationDirection: organizationDirection, RuntimeDirection: runtimeDirection,
			Availability: availability, Link: b.link(deeplink.Todo(id)),
		})
	}
	return out
}

func (b *organizationBuild) machines() []orgMachine {
	keys := map[string]bool{}
	for machine := range b.devices {
		keys[machine] = true
	}
	for machine := range b.machineClaims {
		keys[machine] = true
	}
	for machine := range b.attempted {
		keys[machine] = true
	}
	ids := make([]string, 0, len(keys))
	for machine := range keys {
		ids = append(ids, machine)
	}
	slices.Sort(ids)
	out := make([]orgMachine, 0, len(ids))
	for _, machine := range ids {
		fresh := b.machineFresh[machine]
		if fresh.Status == "" {
			fresh = b.machineAvailability(machine)
		}
		state := fresh.Status
		if fresh.Status == orgAvailable {
			state = "online"
		}
		name := b.devices[machine].Name
		if name == "" {
			name = machine
		}
		running := orgNoValue[int](fresh.Status, fresh.Reason, fresh.Source)
		if fresh.Status == orgAvailable {
			if count, ok := b.s.workingSessions(machine); ok {
				running = orgValue(count, "admission_working_sessions", orgTime(b.now))
			} else if list, hasList := b.lists[machine]; hasList && b.s.devs == nil {
				// A remote sessions() call just returned a complete list. Count
				// the same StatusWorking rows; do not use the OS-process fallback.
				count := 0
				for _, session := range list {
					if session.Status == proto.StatusWorking {
						count++
					}
				}
				running = orgValue(count, "agents_list_status_working", orgTime(b.now))
			} else {
				running = orgNoValue[int](orgUnknown, "no_session_snapshot", "admission_working_sessions")
			}
		}
		claims := b.machineClaims[machine]
		limit := b.s.limitsFor(b.ctx, machine, "", "", b.admission).maxProcs
		out = append(out, orgMachine{
			MachineID: machine, DisplayName: name, State: state, Freshness: fresh, Running: running,
			UnlinkedStartClaims: orgValue(claims, "admission_machine_claims", orgTime(b.now)),
			ConcurrencyLimit:    orgValue(limit, "admission_resolved_machine_limit", orgTime(b.now)),
		})
	}
	return out
}

func (b *organizationBuild) modelsToday() orgModelDay {
	var observations []orgModelObservation
	for _, node := range b.nodeOrder {
		if node.Kind != "worker" && node.Kind != "subagent" {
			continue
		}
		observations = append(observations, b.modelByNode[node.ID]...)
	}
	slices.SortStableFunc(observations, func(a, b orgModelObservation) int {
		var at, bt time.Time
		if a.ObservedAt != nil {
			at = *a.ObservedAt
		}
		if b.ObservedAt != nil {
			bt = *b.ObservedAt
		}
		return cmp.Or(at.Compare(bt), cmp.Compare(a.NodeID, b.NodeID), cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.ItemID, b.ItemID))
	})
	if observations == nil {
		observations = []orgModelObservation{}
	}
	availability := orgObserved(orgUnknown, "daily_history_limited_to_current_organization_nodes", "transcript_items", orgTime(b.now))
	for _, read := range b.modelRead {
		if read.Status == orgUnknown {
			availability.Reason = "daily_history_partial_transcript_unavailable"
			break
		}
	}
	return orgModelDay{Observations: observations, Availability: availability}
}

func (b *organizationBuild) p0Lineage(source *orgWork, todoID string) ([]string, []string, string, string) {
	if source == nil && todoID != "" {
		for _, candidate := range b.work {
			if slices.Contains(candidate.todoIDs, todoID) {
				if source != nil {
					return nil, nil, "incomplete", "multiple_workers_no_unique_lineage"
				}
				source = candidate
			}
		}
	}
	if source == nil {
		return []string{}, nil, "incomplete", "worker_not_in_current_organization"
	}
	var lineage []string
	current := source.node
	for current.Kind == "subagent" {
		lineage = append(lineage, current.ID)
		if current.ParentID == nil {
			return []string{}, lineage, "incomplete", cmp.Or(current.ParentAvailability.Reason, "subagent_parent_unknown")
		}
		parent := b.nodeByID[*current.ParentID]
		if parent == nil {
			return []string{}, lineage, "incomplete", "parent_node_missing"
		}
		current = parent
	}
	if current.Kind != "worker" {
		return []string{}, lineage, "incomplete", "worker_parent_missing"
	}
	lineage = append(lineage, current.ID)
	path := []string{current.ID}
	for current.ParentID != nil {
		parent := b.nodeByID[*current.ParentID]
		if parent == nil {
			return []string{}, lineage, "incomplete", "parent_node_missing"
		}
		switch parent.Kind {
		case "worker", "subagent":
			lineage = append(lineage, parent.ID)
			current = parent
		case "direction":
			path = append(path, parent.ID)
			if parent.ParentID == nil {
				return []string{}, lineage, "incomplete", "direction_main_missing"
			}
			main := b.nodeByID[*parent.ParentID]
			if main == nil || main.Kind != "main" || main.ParentID == nil {
				return []string{}, lineage, "incomplete", "direction_main_missing"
			}
			owner := b.nodeByID[*main.ParentID]
			if owner == nil || owner.Kind != "owner" {
				return []string{}, lineage, "incomplete", "main_owner_missing"
			}
			path = append(path, main.ID, owner.ID)
			return path, lineage, "complete", ""
		case "main":
			path = append(path, parent.ID)
			if parent.ParentID == nil {
				return []string{}, lineage, "incomplete", "main_owner_missing"
			}
			owner := b.nodeByID[*parent.ParentID]
			if owner == nil || owner.Kind != "owner" {
				return []string{}, lineage, "incomplete", "main_owner_missing"
			}
			path = append(path, owner.ID)
			return path, lineage, "complete", ""
		default:
			return []string{}, lineage, "incomplete", "unexpected_parent_kind"
		}
	}
	return []string{}, lineage, "incomplete", cmp.Or(current.ParentAvailability.Reason, "parent_unknown")
}

func (b *organizationBuild) p0Paths() orgP0Snapshot {
	items := []orgP0Item{}
	for _, interaction := range b.interactions {
		if interaction.PushLevel != "" && interaction.PushLevel != "P0" {
			continue
		}
		key := [2]string{b.s.canonical(interaction.MachineID), interaction.SessionID}
		source := b.work[key]
		path, lineage, status, reason := b.p0Lineage(source, "")
		sourceID := ""
		if source != nil {
			sourceID = source.node.ID
		}
		items = append(items, orgP0Item{
			ID: interaction.ID, Kind: "interaction", SourceNodeID: sourceID,
			Path: path, AgentLineage: lineage, Status: status, Reason: reason,
			Link: b.link(deeplink.At(deeplink.Session(key[0], key[1]), interaction.ID)),
		})
	}
	for _, round := range b.s.awaitingAcceptance(b.ctx) {
		if !decideAsk(&round.ask) || decisionPushLevel(round.ask.WhyOwner) != "P0" {
			continue
		}
		key := [2]string{b.s.canonical(round.machine), round.session}
		var source *orgWork
		if key[0] != "" && key[1] != "" {
			source = b.work[key]
		}
		path, lineage, status, reason := b.p0Lineage(source, round.todo)
		sourceID := ""
		if source != nil {
			sourceID = source.node.ID
		}
		items = append(items, orgP0Item{
			ID: "todo:" + round.todo, Kind: "todo_decision", SourceNodeID: sourceID, TodoID: round.todo,
			Path: path, AgentLineage: lineage, Status: status, Reason: reason,
			ObservedAt: orgTime(round.ask.AskedAt), Link: b.link(deeplink.Todo(round.todo)),
		})
	}
	slices.SortStableFunc(items, func(a, b orgP0Item) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
	})
	availability := orgObserved(orgUnknown, "current_p0_sources_limited_to_open_interactions_and_acceptance_decisions", "current_owner_requests", orgTime(b.now))
	if b.interactionErr != nil || !b.acceptLoaded {
		availability.Reason = "current_p0_source_unavailable"
	}
	return orgP0Snapshot{Items: items, Availability: availability}
}
