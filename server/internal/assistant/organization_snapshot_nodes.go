package assistant

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/deeplink"
	"github.com/nwverse/persona/server/internal/proto"
)

func orgMainNodeID(cfg Settings) string {
	if cfg.Main == "" {
		return "main"
	}
	return "main:" + cfg.Main
}

func orgDirectionNodeID(id string) string { return "direction:" + id }
func orgAgentNodeID(key [2]string) string { return "agent:" + key[0] + "/" + key[1] }

func orgBaseNode(id, kind, name string) *orgNode {
	return &orgNode{
		ID: id, Kind: kind, DisplayName: name,
		CurrentGoal:           orgNoValue[string](orgUnknown, "authoritative_goal_source_missing", "organization_snapshot"),
		Conversation:          orgNoValue[orgLink](orgNotApplicable, "not_applicable_to_node_kind", "node_contract"),
		Todo:                  orgNoValue[orgLink](orgNotApplicable, "not_applicable_to_node_kind", "node_contract"),
		Agent:                 orgNoValue[orgLink](orgNotApplicable, "not_applicable_to_node_kind", "node_contract"),
		OrganizationDirection: orgNoValue[string](orgNotApplicable, "not_applicable_to_node_kind", "node_contract"),
		Launcher:              orgNoValue[string](orgUnknown, "launcher_not_reported", "runtime_session"),
		Model:                 orgNoValue[string](orgUnknown, "no_dated_transcript_model_observation", "transcript_items"),
		RequestedModel:        orgNoValue[string](orgUnknown, "requested_model_not_reported", "runtime_session"),
		Effort:                orgNoValue[string](orgUnknown, "effort_not_reported", "runtime_session"),
		Preset:                orgNoValue[string](orgUnknown, "selected_preset_not_recorded", "task_plan"),
		Review:                orgNoValue[orgReviewSummary](orgNotApplicable, "not_applicable_to_node_kind", "node_contract"),
		Drift:                 orgNoValue[string](orgNotApplicable, "not_applicable_to_node_kind", "node_contract"),
		StateAvailability:     orgObserved(orgNotApplicable, "not_applicable_to_node_kind", "node_contract", nil),
		UpdatedAtAvailability: orgObserved(orgNotApplicable, "no_runtime_update_source", "node_contract", nil),
		ParentAvailability:    orgObserved(orgNotApplicable, "not_applicable_to_node_kind", "node_contract", nil),
	}
}

func (b *organizationBuild) link(path string) orgField[orgLink] {
	if path == "" {
		return orgNoValue[orgLink](orgUnknown, "open_link_unavailable", "deep_links")
	}
	return orgValue(orgLink{Path: path, App: deeplink.App(path), Web: deeplink.Web(b.web, path)}, "deep_links", nil)
}

func (b *organizationBuild) addNode(node *orgNode) {
	b.nodeOrder = append(b.nodeOrder, node)
	b.nodeByID[node.ID] = node
}

func (b *organizationBuild) makeSupervisorNodes() {
	owner := orgBaseNode("owner", "owner", b.s.owner(b.ctx))
	owner.CurrentGoal = orgNoValue[string](orgNotApplicable, "owner_root_has_no_goal", "node_contract")
	owner.Drift = orgNoValue[string](orgNotApplicable, "not_applicable_to_node_kind", "node_contract")
	b.addNode(owner)

	mainID := orgMainNodeID(b.cfg)
	mainName := "Persona"
	if title := strings.TrimSpace(b.titles[b.cfg.Main].title); title != "" {
		mainName = title
	}
	main := orgBaseNode(mainID, "main", mainName)
	main.ParentID = orgString(owner.ID)
	main.ParentAvailability = orgObserved(orgAvailable, "", "settings_main", nil)
	main.Conversation = b.link(deeplink.Conversation(b.cfg.Main))
	main.Drift = orgNoValue[string](orgUnknown, "authoritative_drift_producer_missing", "organization_snapshot")
	b.addNode(main)

	for _, id := range b.tb.leads {
		name := strings.TrimSpace(b.names[id])
		if name == "" {
			name = strings.TrimSpace(b.titles[id].title)
		}
		if name == "" {
			name = "Direction"
		}
		node := orgBaseNode(orgDirectionNodeID(id), "direction", name)
		node.ParentID = orgString(mainID)
		node.ParentAvailability = orgObserved(orgAvailable, "", "current_direction", nil)
		node.Conversation = b.link(deeplink.Conversation(id))
		node.Drift = orgNoValue[string](orgUnknown, "authoritative_drift_producer_missing", "organization_snapshot")
		b.addNode(node)
	}
}

func orgSortedKeys[T any](values map[[2]string]T) [][2]string {
	keys := make([][2]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b [2]string) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	})
	return keys
}

func (b *organizationBuild) indexSubagents() {
	for _, parentKey := range orgSortedKeys(b.sessions) {
		parent := b.sessions[parentKey]
		running := map[string]bool{}
		for _, agent := range parent.Subagents {
			if agent.Running {
				running[agent.ID] = true
			}
		}
		for _, agent := range parent.Subagents {
			if !agent.Running {
				continue
			}
			key := [2]string{parentKey[0], cmp.Or(agent.SessionID, parent.SessionID+"@"+agent.ID)}
			parentRef := parentKey
			if agent.ParentID != "" && agent.ParentID != agent.ID && running[agent.ParentID] {
				for _, launcher := range parent.Subagents {
					if launcher.ID == agent.ParentID && launcher.Running {
						parentRef = [2]string{parentKey[0], cmp.Or(launcher.SessionID, parent.SessionID+"@"+launcher.ID)}
						break
					}
				}
			}
			if b.subagentParents[key] == nil {
				b.subagentParents[key] = map[[2]string]bool{}
			}
			b.subagentParents[key][parentRef] = true
			if previous, ok := b.subagents[key]; !ok || agent.UpdatedAt.After(previous.info.UpdatedAt) {
				b.subagents[key] = orgSubagent{parent: parentRef, info: agent}
			}
		}
	}
}

func (b *organizationBuild) currentDirection(id string) bool {
	if id == "" || !slices.Contains(b.cfg.Leaders, id) {
		return false
	}
	d := b.s.directionOf(id)
	return d != nil && d.EndedAt == nil
}

// todoRuntimeScope resolves runtime ownership, not organizational DirectionID.
// The existing secretary fallback is named as such; an unresolved direction is
// never upgraded to a fully proved lineage.
func (b *organizationBuild) todoRuntimeScope(t listedTodo) (string, string, orgAvailability) {
	mainID := orgMainNodeID(b.cfg)
	if t.Supervision == nil || t.Supervision.Supervisor == "" {
		return mainID, b.cfg.Main, orgObserved(orgAvailable, "", "direction_tree_secretary_fallback", nil)
	}
	sup := t.Supervision.Supervisor
	if sup == b.cfg.Main {
		return mainID, sup, orgObserved(orgAvailable, "", "supervision_supervisor", nil)
	}
	if lead, _, ok := b.s.directionForTodo(b.ctx, t.Todo); ok {
		return orgDirectionNodeID(lead), lead, orgObserved(orgAvailable, "", "supervision_supervisor", nil)
	}
	if b.s.agentSupervised(t.Todo) {
		if _, ok := b.s.uniqueLeaderSupervisorAbove(t.Todo); !ok {
			return "", sup, orgObserved(orgUnknown, "ambiguous_or_missing_task_leader", "supervision_supervisor", nil)
		}
	}
	return mainID, sup, orgObserved(orgUnknown, "direction_unresolved_secretary_fallback", "direction_tree_secretary_fallback", nil)
}

func (b *organizationBuild) organizationDirection(t listedTodo, runtimeRaw string, runtimeStatus orgAvailability) orgField[string] {
	id := strings.TrimSpace(t.DirectionID)
	if id == "" {
		return orgNoValue[string](orgNotApplicable, "no_organization_direction", "todo_direction_id")
	}
	if !b.currentDirection(id) {
		return orgField[string]{Value: orgString(id), Status: orgUnknown, Reason: "organization_direction_not_current", Source: "todo_direction_id"}
	}
	if runtimeStatus.Status == orgAvailable && runtimeRaw != "" && runtimeRaw != id {
		return orgField[string]{Value: orgString(id), Status: orgUnknown, Reason: "organization_runtime_direction_conflict", Source: "todo_direction_id"}
	}
	return orgValue(id, "todo_direction_id", nil)
}

func (b *organizationBuild) addParent(work *orgWork, parent [2]string, source string) {
	parent[0] = b.s.canonical(parent[0])
	if parent[0] == "" || parent[1] == "" {
		return
	}
	if work.candidates[parent] == nil {
		work.candidates[parent] = map[string]bool{}
	}
	work.candidates[parent][source] = true
}

func (b *organizationBuild) addRuntimeParent(work *orgWork) {
	if work.session == nil {
		return
	}
	session := *work.session
	machine := work.key[0]
	if parentMachine := session.ParentMachineID(); parentMachine != "" {
		machine = b.s.canonical(parentMachine)
	}
	if session.ParentSessionID != "" {
		b.addParent(work, [2]string{machine, session.ParentSessionID}, "runtime_parent_session")
	}
	if parent := session.AskingParentSessionID(); parent != "" {
		b.addParent(work, [2]string{machine, parent}, "runtime_asking_parent_session")
	}
	if session.ParentSessionID == "" && session.AskingParentSessionID() == "" && machine == work.key[0] && session.RootSessionID != "" && session.RootSessionID != session.SessionID {
		b.addParent(work, [2]string{machine, session.RootSessionID}, "runtime_root_session")
	}
}

func (b *organizationBuild) ensureWorker(t listedTodo, agent TodoAgent) *orgWork {
	key := [2]string{b.s.canonical(agent.MachineID), agent.SessionID}
	scope, runtimeRaw, scopeStatus := b.todoRuntimeScope(t)
	work := b.work[key]
	if work == nil {
		name := strings.TrimSpace(agent.Title)
		if name == "" || name == agent.SessionID {
			name = t.Title
		}
		if strings.TrimSpace(name) == "" || name == agent.SessionID {
			name = "Agent"
		}
		node := orgBaseNode(orgAgentNodeID(key), "worker", name)
		node.Agent = b.link(deeplink.Session(key[0], key[1]))
		node.OrganizationDirection = b.organizationDirection(t, runtimeRaw, scopeStatus)
		work = &orgWork{key: key, node: node, scope: scope, scopeStatus: scopeStatus,
			candidates: map[[2]string]map[string]bool{}}
		b.work[key] = work
		b.addNode(node)
		if session, ok := b.sessions[key]; ok {
			work.session = &session
		}
		if sub, ok := b.subagents[key]; ok {
			work.subagent = &sub.info
		}
		b.addRuntimeParent(work)
	} else if work.scope != scope {
		work.scopeConflict = true
		work.scopeStatus = orgObserved(orgUnknown, "multiple_runtime_directions", "supervision_supervisor", nil)
	}
	if !slices.Contains(work.todoIDs, t.ID) {
		work.todoIDs = append(work.todoIDs, t.ID)
	}
	if agent.Source == sourceSpawned {
		if machine, session, ok := parseLeaderRef(agent.Via); ok {
			b.addParent(work, [2]string{machine, session}, "todo_agent_spawned_via")
		}
	}
	if attach, ok := b.tb.leaderOf(t.Todo); ok {
		b.addParent(work, attach.agent, "task_leader_worker")
	}
	return work
}

func (b *organizationBuild) ensureSubagent(key [2]string) *orgWork {
	key[0] = b.s.canonical(key[0])
	if work := b.work[key]; work != nil {
		return work
	}
	name := "Subagent"
	if sub, ok := b.subagents[key]; ok {
		name = cmp.Or(strings.TrimSpace(sub.info.Description), strings.TrimSpace(sub.info.Type), name)
	}
	if session, ok := b.sessions[key]; ok && strings.TrimSpace(session.Title) != "" && session.Title != key[1] {
		name = session.Title
	}
	if name == key[1] {
		name = "Subagent"
	}
	node := orgBaseNode(orgAgentNodeID(key), "subagent", name)
	node.Agent = b.link(deeplink.Session(key[0], key[1]))
	work := &orgWork{key: key, node: node, candidates: map[[2]string]map[string]bool{},
		scopeStatus: orgObserved(orgUnknown, "parent_scope_unresolved", "runtime_parentage", nil)}
	b.work[key] = work
	b.addNode(node)
	if session, ok := b.sessions[key]; ok {
		work.session = &session
	}
	if sub, ok := b.subagents[key]; ok {
		work.subagent = &sub.info
	}
	b.addRuntimeParent(work)
	for parent := range b.subagentParents[key] {
		b.addParent(work, parent, "in_session_subagent_parent")
	}
	return work
}

// makeWorkNodes reads the authoritative records directly. The old tree's JSON
// is not transformed: it deliberately drops worker-task and edge-kind detail
// that this contract must retain.
func (b *organizationBuild) makeWorkNodes() {
	for _, id := range b.tb.open {
		t := b.tb.todos[id]
		for _, agent := range t.Agents {
			if agent.active() {
				b.ensureWorker(t, agent)
			}
		}
	}
	for _, key := range orgSortedKeys(b.subagents) {
		b.ensureSubagent(key)
	}
	for _, key := range orgSortedKeys(b.sessions) {
		session := b.sessions[key]
		child := session.IsSubagent || session.ParentSessionID != "" || session.AskingParentSessionID() != "" ||
			(session.RootSessionID != "" && session.RootSessionID != session.SessionID)
		if child && treeGoing(session) {
			b.ensureSubagent(key)
		}
	}
}

func (b *organizationBuild) readMachine(machine string) {
	machine = b.s.canonical(machine)
	if machine == "" || b.attempted[machine] {
		return
	}
	b.attempted[machine] = true
	list, err := b.s.sessions(b.ctx, machine)
	if err != nil {
		b.listErrors[machine] = err
		return
	}
	b.lists[machine] = list
}

func (b *organizationBuild) conversationMachine(id string) string {
	if id == b.cfg.Main {
		if b.cfg.MainMachineID != "" {
			return b.s.canonical(b.cfg.MainMachineID)
		}
		return b.s.canonical(b.cfg.MachineID)
	}
	if title, ok := b.titles[id]; ok && title.machine != "" {
		return b.s.canonical(title.machine)
	}
	if d := b.s.directionOf(id); d != nil && d.MachineID != "" {
		return b.s.canonical(d.MachineID)
	}
	return b.s.canonical(b.cfg.MachineID)
}

func (b *organizationBuild) readMachineSources() {
	var listed struct {
		Devices []Device `json:"devices"`
	}
	b.deviceListErr = b.s.call(b.ctx, DeviceAll, DeviceDevicesList, map[string]any{}, &listed)
	for _, device := range listed.Devices {
		device.ID = b.s.canonical(device.ID)
		b.devices[device.ID] = device
		b.readMachine(device.ID)
	}
	b.readMachine(b.conversationMachine(b.cfg.Main))
	for _, id := range b.tb.leads {
		b.readMachine(b.conversationMachine(id))
	}
	b.admission = b.s.admissionSettings(b.ctx)
	b.s.admission.mu.Lock()
	for machine, count := range b.s.admission.machineClaims {
		if count == 0 {
			continue
		}
		machine = b.s.canonical(machine)
		b.machineClaims[machine] = count
		b.readMachine(machine)
	}
	for direction, count := range b.s.admission.directionClaims {
		b.directionClaims[direction] = count
	}
	b.s.admission.mu.Unlock()
	var pending struct {
		Interactions []proto.Interaction `json:"interactions"`
	}
	b.interactionErr = b.s.call(b.ctx, DeviceAll, DeviceInteractionsList, map[string]any{}, &pending)
	b.interactions = pending.Interactions
	for machine := range b.attempted {
		b.machineFresh[machine] = b.machineAvailability(machine)
	}
	for machine := range b.machineClaims {
		if _, ok := b.machineFresh[machine]; !ok {
			b.machineFresh[machine] = b.machineAvailability(machine)
		}
	}
}

func (b *organizationBuild) machineAvailability(machine string) orgAvailability {
	if b.deviceListErr != nil {
		return orgObserved(orgUnknown, "machine_list_unavailable", "devices_list", nil)
	}
	device, ok := b.devices[machine]
	if !ok {
		return orgObserved(orgUnknown, "machine_not_listed", "devices_list", nil)
	}
	shownUntil := time.Time{}
	if b.s.fleetOn() && b.s.devs != nil {
		if shown, found := b.s.devs.Shown(machine); found {
			shownUntil = shown.Until
		}
	}
	if !device.Online && !device.Reachable && shownUntil.IsZero() {
		return orgObserved(orgOffline, "machine_offline", "devices_list", nil)
	}
	if !shownUntil.IsZero() {
		return orgObserved(orgUnknown, "reconnect_or_warm_grace", "device_session_visibility", orgTime(shownUntil))
	}
	if err := b.listErrors[machine]; err != nil {
		return orgObserved(orgUnknown, "session_list_unavailable", "agents_list_session_list", nil)
	}
	if _, ok := b.lists[machine]; !ok {
		return orgObserved(orgUnknown, "no_session_snapshot", "agents_list_session_list", nil)
	}
	// Without the local fleet, sessions() just returned from the same RPC used
	// by agents_list, so this is a fresh read rather than a retained list.
	if !b.s.fleetOn() || b.s.devs == nil {
		return orgObserved(orgAvailable, "", "agents_list_session_list", orgTime(b.now))
	}
	previousConn := b.s.fleetPreviousConn(machine)
	currentConn := b.s.devs.ConnectedAt(machine)
	if previousConn.IsZero() || currentConn.IsZero() || !previousConn.Equal(currentConn) {
		return orgObserved(orgStale, "session_snapshot_from_old_or_unknown_connection", "fleet_previous_connection", orgTime(previousConn))
	}
	_, sample, _ := b.s.devs.MachineResources(machine)
	if sample == nil {
		return orgObserved(orgUnknown, "no_current_node_evidence", "machine_metrics", nil)
	}
	at := time.Unix(sample.T, 0)
	age := b.now.Sub(at)
	if age < 0 {
		return orgObserved(orgUnknown, "metrics_clock_in_future", "machine_metrics", orgTime(at))
	}
	if at.Unix() <= currentConn.Unix() {
		return orgObserved(orgStale, "metrics_before_current_connection", "machine_metrics", orgTime(at))
	}
	if age > admissionSampleFresh {
		return orgObserved(orgStale, "metrics_stale", "machine_metrics", orgTime(at))
	}
	if stale, _ := b.s.silentNode(device, sample, b.now); stale {
		return orgObserved(orgStale, "node_unresponsive", "machine_metrics", orgTime(at))
	}
	return orgObserved(orgAvailable, "", "current_connection_list_and_metrics", orgTime(at))
}

func orgSources(values map[string]bool) string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

func (b *organizationBuild) attachWorkNodes() {
	keys := make([][2]string, 0, len(b.work))
	for key := range b.work {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b [2]string) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
	for _, key := range keys {
		work := b.work[key]
		for parent := range b.subagentParents[key] {
			b.addParent(work, parent, "in_session_subagent_parent")
		}
		node := work.node
		if work.scopeConflict {
			node.ParentAvailability = orgObserved(orgUnknown, "multiple_runtime_directions", "supervision_supervisor", nil)
			continue
		}
		if len(work.candidates) == 0 {
			if node.Kind == "worker" && work.scope != "" && work.scopeStatus.Status == orgAvailable {
				node.ParentID = orgString(work.scope)
				node.ParentAvailability = orgObserved(orgAvailable, "", work.scopeStatus.Source, nil)
			} else if node.Kind == "subagent" {
				node.ParentAvailability = orgObserved(orgUnknown, "subagent_parent_missing", "runtime_parentage", nil)
			} else {
				node.ParentAvailability = work.scopeStatus
			}
			continue
		}
		if len(work.candidates) > 1 {
			node.ParentAvailability = orgObserved(orgUnknown, "conflicting_parent_records", "runtime_parentage", nil)
			continue
		}
		for parent, sources := range work.candidates {
			if parent == key {
				node.ParentAvailability = orgObserved(orgUnknown, "parent_cycle", orgSources(sources), nil)
				continue
			}
			parentWork := b.work[parent]
			if parentWork == nil {
				node.ParentAvailability = orgObserved(orgUnknown, "parent_node_missing", orgSources(sources), nil)
				continue
			}
			node.ParentID = orgString(parentWork.node.ID)
			node.ParentAvailability = orgObserved(orgAvailable, "", orgSources(sources), nil)
			if work.scope == "" && parentWork.scope != "" {
				work.scope = parentWork.scope
				work.scopeStatus = parentWork.scopeStatus
			}
		}
	}
	// Propagate scopes after all parent choices. A nested subagent's parent may
	// have been attached later in lexical order.
	for changed := true; changed; {
		changed = false
		for _, work := range b.work {
			if work.scope != "" || work.node.ParentID == nil {
				continue
			}
			parent := b.nodeByID[*work.node.ParentID]
			if parent == nil {
				continue
			}
			for _, parentWork := range b.work {
				if parentWork.node.ID == parent.ID && parentWork.scope != "" {
					work.scope, work.scopeStatus = parentWork.scope, parentWork.scopeStatus
					changed = true
					break
				}
			}
		}
	}
}

func (b *organizationBuild) breakParentCycles() {
	for _, start := range b.nodeOrder {
		if start.Kind != "worker" && start.Kind != "subagent" {
			continue
		}
		path := map[string]int{}
		var chain []*orgNode
		node := start
		for node != nil && (node.Kind == "worker" || node.Kind == "subagent") && node.ParentID != nil {
			if at, seen := path[node.ID]; seen {
				for _, member := range chain[at:] {
					member.ParentID = nil
					member.ParentAvailability = orgObserved(orgUnknown, "parent_cycle", "runtime_parentage", nil)
				}
				break
			}
			path[node.ID] = len(chain)
			chain = append(chain, node)
			node = b.nodeByID[*node.ParentID]
		}
	}
}

func orgFreshValue(value, emptyReason, source string, fresh orgAvailability, now time.Time) orgField[string] {
	if value == "" {
		return orgNoValue[string](orgUnknown, emptyReason, source)
	}
	if fresh.Status == orgAvailable {
		return orgValue(value, source, orgTime(now))
	}
	return orgField[string]{Value: orgString(value), Status: fresh.Status, Reason: fresh.Reason, Source: source, ObservedAt: fresh.ObservedAt}
}

func (b *organizationBuild) runtimeFields(work *orgWork) (launcher, model, requested, effort, preset orgField[string]) {
	fresh := b.machineFresh[work.key[0]]
	if fresh.Status == "" {
		fresh = orgObserved(orgUnknown, "machine_freshness_unavailable", "agents_list_session_list", nil)
	}
	launcher = orgNoValue[string](orgUnknown, "launcher_not_reported", "runtime_session")
	model = orgNoValue[string](orgUnknown, "no_dated_transcript_model_observation", "transcript_items")
	requested = orgNoValue[string](orgUnknown, "requested_model_not_reported", "runtime_session")
	effort = orgNoValue[string](orgUnknown, "effort_not_reported", "runtime_session")
	preset = orgNoValue[string](orgUnknown, "selected_preset_not_recorded", "task_plan")
	if work.session != nil {
		session := *work.session
		launcher = orgFreshValue(session.Launcher, "launcher_not_reported", "runtime_session", fresh, b.now)
		if session.OffLauncher && launcher.Value != nil {
			launcher.Status = orgUnknown
			launcher.Reason = "off_launcher_current_process_unconfirmed"
		}
		if session.RequestedModel != nil {
			requested = orgFreshValue(*session.RequestedModel, "requested_model_not_reported", "runtime_session", fresh, b.now)
			if *session.RequestedModel == "" {
				requested = orgValue("", "runtime_session_launcher_default", orgTime(b.now))
				if fresh.Status != orgAvailable {
					requested.Status, requested.Reason, requested.ObservedAt = fresh.Status, fresh.Reason, fresh.ObservedAt
				}
			}
		}
		effort = orgFreshValue(session.Effort, "effort_not_reported", "runtime_session", fresh, b.now)
	}
	if work.subagent != nil && work.subagent.Model != "" {
		model = orgValue(work.subagent.Model, "runtime_subagent_transcript_model", orgTime(work.subagent.UpdatedAt))
	}
	if len(work.todoIDs) == 1 {
		if tier := b.presetByTodo[work.todoIDs[0]]; tier != "" {
			preset = orgValue(tier, "task_plan_tier", nil)
		}
	}
	return launcher, model, requested, effort, preset
}

func orgTaskForWork(b *organizationBuild, work *orgWork) *listedTodo {
	if len(work.todoIDs) != 1 {
		return nil
	}
	t, ok := b.tb.todos[work.todoIDs[0]]
	if !ok {
		return nil
	}
	return &t
}

func orgDurableDecision(t *listedTodo) (bool, string, *time.Time) {
	if t == nil {
		return false, "", nil
	}
	if t.Supervision != nil && t.Supervision.state(t.Todo) == reviewNeedsUser && !ownerDecisionReturned(t.Supervision) {
		return true, "current_needs_user_review", orgTime(t.Supervision.UpdatedAt)
	}
	if t.Ask != nil && (t.Ask.Outcome == "decide" || len(t.Ask.Questions) > 0) {
		return true, "open_acceptance_decision", orgTime(t.Ask.AskedAt)
	}
	return false, "", nil
}

func orgStartFailed(t *listedTodo, key [2]string) bool {
	if t == nil {
		return false
	}
	if t.StartError != "" {
		return true
	}
	for _, agent := range t.Agents {
		if agent.MachineID == key[0] && agent.SessionID == key[1] && agent.StartError != "" {
			return true
		}
	}
	return false
}

// orgRuntimeState maps only authoritative runtime and owner-wait signals. A
// supervisor-held OwnerWait alone deliberately does not create an owner-facing
// needs_you or P0 item.
func orgRuntimeState(session *proto.Session, sub *proto.Subagent, task *listedTodo, key [2]string, fresh orgAvailability, acceptLoaded bool) (*string, string, orgAvailability) {
	stopped, working, needs := "stopped", "working", "needs_you"
	if decision, source, at := orgDurableDecision(task); decision {
		availability := orgObserved(orgAvailable, "", source, at)
		if fresh.Status != orgAvailable {
			availability.Status = fresh.Status
			availability.Reason = "durable_owner_decision_" + fresh.Status
		}
		return &needs, "owner_decision", availability
	}
	if orgStartFailed(task, key) {
		return &stopped, "start_failed", orgObserved(orgAvailable, "", "todo_agent_start_error", nil)
	}
	if task != nil && notStarted(task.Todo) {
		return &stopped, "planned", orgObserved(orgAvailable, "", "todo_planned", nil)
	}
	if fresh.Status != orgAvailable {
		if !acceptLoaded && fresh.Status == orgUnknown {
			fresh.Reason = "owner_decision_source_unavailable"
		}
		return nil, "", fresh
	}
	if session == nil && sub == nil {
		return nil, "", orgObserved(orgUnknown, "session_not_in_fresh_list", "agents_list_session_list", nil)
	}
	if session != nil {
		if session.Status == proto.StatusInterrupted || session.TurnError != "" && session.State != proto.StateWorking && session.State != proto.StateNeedsYou {
			return &stopped, "interrupted", orgObserved(orgAvailable, "", "runtime_session", orgTime(session.UpdatedAt))
		}
		state := triageStateOf(*session)
		if session.AwaitsReply {
			return &needs, "awaits_reply", orgObserved(orgAvailable, "", "runtime_session", orgTime(session.UpdatedAt))
		}
		switch state {
		case proto.StateNeedsYou:
			return &needs, "open_interaction", orgObserved(orgAvailable, "", "runtime_session", orgTime(session.UpdatedAt))
		case proto.StateWorking:
			activity := "working"
			if session.BackgroundWaiting {
				activity = "waiting_background"
			}
			return &working, activity, orgObserved(orgAvailable, "", "runtime_session", orgTime(session.UpdatedAt))
		case proto.StateOffline:
			return &stopped, "offline", orgObserved(orgAvailable, "", "runtime_session", orgTime(session.UpdatedAt))
		case proto.StateUnread:
			return &stopped, "unread", orgObserved(orgAvailable, "", "runtime_session", orgTime(session.UpdatedAt))
		case proto.StateIdle:
			return &stopped, "idle", orgObserved(orgAvailable, "", "runtime_session", orgTime(session.UpdatedAt))
		default:
			return nil, "", orgObserved(orgUnknown, "host_state_unknown", "runtime_session", orgTime(session.UpdatedAt))
		}
	}
	if sub.Running {
		return &working, "working", orgObserved(orgAvailable, "", "runtime_subagent", orgTime(sub.UpdatedAt))
	}
	return &stopped, "idle", orgObserved(orgAvailable, "", "runtime_subagent", orgTime(sub.UpdatedAt))
}

func (b *organizationBuild) currentGoal(work *orgWork) orgField[string] {
	fresh := b.machineFresh[work.key[0]]
	if work.session != nil && work.session.Goal != nil && strings.TrimSpace(work.session.Goal.Objective) != "" {
		goal := *work.session.Goal
		field := orgValue(goal.Objective, "runtime_goal_info", orgTime(goal.UpdatedAt))
		if fresh.Status != orgAvailable {
			field.Status, field.Reason = fresh.Status, "runtime_goal_not_fresh"
		}
		return field
	}
	if work.node.Kind == "worker" && len(work.todoIDs) == 1 {
		id := work.todoIDs[0]
		if brief, ok := b.s.briefOfTodo(id); ok && len(brief.Criteria) > 0 {
			return orgValue(strings.Join(brief.Criteria, "\n"), "current_task_brief_criteria", nil)
		}
		if t, ok := b.tb.todos[id]; ok && t.Supervision != nil && len(t.Supervision.DoneWhen) > 0 {
			return orgValue(strings.Join(t.Supervision.DoneWhen, "\n"), "supervision_done_when", orgTime(t.Supervision.UpdatedAt))
		}
	}
	if fresh.Status != orgAvailable && work.session != nil {
		return orgNoValue[string](fresh.Status, "runtime_goal_unavailable_and_no_durable_criteria", "runtime_goal_info")
	}
	return orgNoValue[string](orgUnknown, "no_runtime_goal_or_task_criteria", "runtime_goal_info")
}

func (b *organizationBuild) finishWorkNodes() {
	keys := make([][2]string, 0, len(b.work))
	for key := range b.work {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b [2]string) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
	for _, key := range keys {
		work := b.work[key]
		node := work.node
		task := orgTaskForWork(b, work)
		fresh := b.machineFresh[key[0]]
		if fresh.Status == "" {
			fresh = orgObserved(orgUnknown, "machine_freshness_unavailable", "agents_list_session_list", nil)
		}
		node.State, node.Activity, node.StateAvailability = orgRuntimeState(work.session, work.subagent, task, key, fresh, b.acceptLoaded)
		node.CurrentGoal = b.currentGoal(work)
		node.Launcher, node.Model, node.RequestedModel, node.Effort, node.Preset = b.runtimeFields(work)
		b.applyLatestModel(node)
		if len(work.todoIDs) == 1 {
			node.Todo = b.link(deeplink.Todo(work.todoIDs[0]))
		} else if len(work.todoIDs) > 1 {
			node.Todo = orgNoValue[orgLink](orgUnknown, "multiple_active_task_links", "todo_agents")
			node.CurrentGoal = orgNoValue[string](orgUnknown, "multiple_active_task_links", "todo_agents")
			node.OrganizationDirection = orgNoValue[string](orgUnknown, "multiple_active_task_links", "todo_agents")
		}
		var updated time.Time
		source := "todo_agent_link"
		if work.session != nil {
			updated = work.session.UpdatedAt
			source = "runtime_session"
		} else if work.subagent != nil {
			updated = work.subagent.UpdatedAt
			source = "runtime_subagent"
		} else if task != nil {
			updated = task.UpdatedAt
			for _, agent := range task.Agents {
				if agent.MachineID == key[0] && agent.SessionID == key[1] && agent.LinkedAt.After(updated) {
					updated = agent.LinkedAt
				}
			}
		}
		node.UpdatedAt = orgTime(updated)
		if node.UpdatedAt != nil {
			node.UpdatedAtAvailability = orgObserved(orgAvailable, "", source, node.UpdatedAt)
			if fresh.Status != orgAvailable && strings.HasPrefix(source, "runtime_") {
				node.UpdatedAtAvailability.Status = fresh.Status
				node.UpdatedAtAvailability.Reason = fresh.Reason
			}
		} else {
			node.UpdatedAtAvailability = orgObserved(orgUnknown, "updated_at_not_reported", source, nil)
		}
	}
}

func (b *organizationBuild) sessionForConversation(id string) *proto.Session {
	machine := b.conversationMachine(id)
	for _, session := range b.lists[machine] {
		if session.SessionID == id {
			copy := session
			return &copy
		}
	}
	return nil
}

func orgMergeState(nodes []*orgNode, fallback orgAvailability) (*string, string, orgAvailability) {
	needs, working, stopped := "needs_you", "working", "stopped"
	unknown := orgAvailability{}
	for _, node := range nodes {
		if node.State != nil && *node.State == needs {
			return &needs, "owner_decision", node.StateAvailability
		}
	}
	for _, node := range nodes {
		if node.State != nil && *node.State == working {
			return &working, "working", node.StateAvailability
		}
	}
	for _, node := range nodes {
		if node.State == nil {
			unknown = node.StateAvailability
		}
	}
	if unknown.Status != "" {
		return nil, "", unknown
	}
	if fallback.Status != "" && fallback.Status != orgAvailable && len(nodes) == 0 {
		return nil, "", fallback
	}
	return &stopped, "stopped", orgObserved(orgAvailable, "", "descendant_runtime_states", nil)
}

func (b *organizationBuild) reviewSummary(scope, raw string) orgField[orgReviewSummary] {
	ids := make([]string, 0, len(b.tb.todos))
	for id := range b.tb.todos {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	summary := orgReviewSummary{TaskIDs: []string{}}
	var latest time.Time
	latestState := ""
	wantScope := orgMainNodeID(b.cfg)
	if raw != b.cfg.Main {
		wantScope = orgDirectionNodeID(raw)
	}
	for _, id := range ids {
		t := b.tb.todos[id]
		if t.Supervision == nil {
			continue
		}
		scope, _, _ := b.todoRuntimeScope(t)
		if scope != wantScope {
			continue
		}
		summary.TaskIDs = append(summary.TaskIDs, id)
		v := t.Supervision
		if v.state(t.Todo) == reviewPending {
			summary.PendingCount++
		}
		if v.AutoReview != nil && v.AutoReview.State == autoRunning {
			summary.AutoReviewPendingCount++
		}
		for _, review := range v.Reviews {
			if review.At.After(latest) {
				latest, latestState = review.At, review.Verdict
			}
		}
		if v.AutoReview != nil {
			at := v.AutoReview.StartedAt
			if v.AutoReview.EndedAt != nil {
				at = *v.AutoReview.EndedAt
			}
			if at.After(latest) {
				latest, latestState = at, v.AutoReview.State
			}
		}
	}
	if len(summary.TaskIDs) == 0 {
		return orgNoValue[orgReviewSummary](orgNotApplicable, "no_supervised_tasks", "task_supervision")
	}
	summary.LatestReviewAt = orgTime(latest)
	switch {
	case summary.PendingCount > 0:
		summary.State = reviewPending
	case summary.AutoReviewPendingCount > 0:
		summary.State = autoRunning
	case latestState != "":
		summary.State = latestState
	default:
		summary.State = "supervised"
	}
	return orgValue(summary, "task_supervision_reviews", orgTime(latest))
}

func (b *organizationBuild) directionConcurrency(scope, raw string) *orgConcurrency {
	running := b.s.directionCountLocked(raw)
	claims := b.directionClaims[raw]
	projects := map[string]bool{}
	for _, id := range b.tb.open {
		t := b.tb.todos[id]
		if t.Supervision != nil && t.Supervision.Supervisor == raw && t.Project != "" {
			projects[t.Project] = true
		}
	}
	if len(projects) == 0 && raw != b.cfg.Main {
		if d := b.s.directionOf(raw); d != nil && d.Project != "" {
			projects[d.Project] = true
		}
	}
	limitField := orgNoValue[int](orgUnknown, "multiple_direction_projects", "admission_resolved_direction_limit")
	if len(projects) <= 1 {
		project := ""
		for value := range projects {
			project = value
		}
		limitField = orgValue(b.s.directionCap(b.ctx, raw, project, b.admission), "admission_resolved_direction_limit", orgTime(b.now))
	}
	fresh := orgObserved(orgAvailable, "", "admission_direction_count", orgTime(b.now))
	for _, id := range b.tb.open {
		t := b.tb.todos[id]
		if t.Supervision == nil || t.Supervision.Supervisor != raw || directionIdle(&t.Todo) {
			continue
		}
		for _, agent := range t.Agents {
			if !agent.active() {
				continue
			}
			availability := b.machineFresh[b.s.canonical(agent.MachineID)]
			if availability.Status != orgAvailable {
				fresh = availability
			}
		}
	}
	var runningField orgField[int]
	if fresh.Status == orgAvailable {
		runningField = orgValue(running, "admission_direction_count", orgTime(b.now))
	} else {
		runningField = orgNoValue[int](fresh.Status, fresh.Reason, fresh.Source)
	}
	return &orgConcurrency{
		Running:             runningField,
		UnlinkedStartClaims: orgValue(claims, "admission_direction_claims", orgTime(b.now)),
		Limit:               limitField,
	}
}

func (b *organizationBuild) finishSupervisorNodes() {
	for _, node := range b.nodeOrder {
		if node.Kind != "main" && node.Kind != "direction" {
			continue
		}
		raw := b.cfg.Main
		if node.Kind == "direction" {
			raw = strings.TrimPrefix(node.ID, "direction:")
		}
		machine := b.conversationMachine(raw)
		session := b.sessionForConversation(raw)
		fresh := b.machineFresh[machine]
		work := &orgWork{key: [2]string{machine, raw}, session: session}
		node.Launcher, node.Model, node.RequestedModel, node.Effort, _ = b.runtimeFields(work)
		b.applyLatestModel(node)
		node.Preset = orgNoValue[string](orgNotApplicable, "not_applicable_to_supervisor", "task_plan")
		// Build an own-state node even when the session is absent from the current list.
		// Its explicit unknown must participate in the same merge as known descendants.
		ownState := &orgNode{}
		ownState.State, ownState.Activity, ownState.StateAvailability = orgRuntimeState(session, nil, nil, work.key, fresh, b.acceptLoaded)
		var descendants []*orgNode
		for _, candidate := range b.work {
			if candidate.scope == node.ID {
				descendants = append(descendants, candidate.node)
			}
		}
		descendants = append(descendants, ownState)
		node.State, node.Activity, node.StateAvailability = orgMergeState(descendants, fresh)
		node.Review = b.reviewSummary(node.ID, raw)
		node.Concurrency = b.directionConcurrency(node.ID, raw)
		updated := time.Time{}
		if session != nil {
			updated = session.UpdatedAt
		}
		for _, todo := range b.tb.todos {
			scope, _, _ := b.todoRuntimeScope(todo)
			if scope != node.ID {
				continue
			}
			if todo.UpdatedAt.After(updated) {
				updated = todo.UpdatedAt
			}
			if todo.Supervision != nil && todo.Supervision.UpdatedAt.After(updated) {
				updated = todo.Supervision.UpdatedAt
			}
		}
		if node.Kind == "direction" {
			if d := b.s.directionOf(raw); d != nil && d.At.After(updated) {
				updated = d.At
			}
		}
		node.UpdatedAt = orgTime(updated)
		if node.UpdatedAt != nil {
			node.UpdatedAtAvailability = orgObserved(orgAvailable, "", "direction_and_task_records", node.UpdatedAt)
		} else {
			node.UpdatedAtAvailability = orgObserved(orgUnknown, "updated_at_not_reported", "direction_and_task_records", nil)
		}
	}

	// The owner is a view of the completed main node, so it must be synthesized
	// after every main/direction node has established its own state and subtree.
	if owner := b.nodeByID["owner"]; owner != nil {
		main := b.nodeByID[orgMainNodeID(b.cfg)]
		owner.State, owner.Activity, owner.StateAvailability = orgMergeState([]*orgNode{main}, orgAvailability{})
	}
}

// planFromJSON is kept local to the read projection so a malformed plan does
// not become a fabricated selected preset.
func planFromJSON(raw json.RawMessage) []PlanTask {
	var tasks []PlanTask
	if json.Unmarshal(raw, &tasks) != nil {
		return nil
	}
	return tasks
}
