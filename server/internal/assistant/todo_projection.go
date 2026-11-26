package assistant

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/assistant/todo/projection"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/wireids"
)

// The To-dos view (#10226): what the manager works out for each to-do and for the list, by the pure projection in
// todo/projection (its rules: spec/fixtures/todo-view and spec/fixtures/todo-counts). This file is the adapter: it reads
// the to-dos as GET /api/v1/todos lists them, their runs' sessions and the owner's zone once per read, and hands them
// over as projection.Facts; the places also read the knowledge base's project titles. The routes call it once per read
// (todo_view_routes.go). The legacy triage (todo_triage.go) keeps placing to-dos for the direction tree and leader
// reports until they read the view.

// todoProjection is a list's facts and their views, each ranked within its section; facts[i] and views[i] are the
// list's i-th row.
type todoProjection struct {
	facts []projection.Facts
	views []assistantwire.TodoSummary
	waits []*assistantwire.TodoWait
}

// todoProjectionOf works out the view of every to-do of list at now. list holds the rows as GET /api/v1/todos lists
// them (formatTodo, with ask, decision_review and orphaned). The sessions read are those of the active runs of the
// to-dos the projection reads as open (a failed start included, whose runs still date its last activity), each
// computer asked once (todoRunSessions), and with them the pending cards of those runs (todoFactsOf).
func (s *Service) todoProjectionOf(ctx context.Context, list []listedTodo, now time.Time) todoProjection {
	facts := s.todoFactsOf(ctx, list)
	if len(facts) > 0 && facts[0].TodosV2 {
		reposByTodo := s.populateTodoRepoGates(ctx, list)
		populateTodoItemTypes(list, reposByTodo)
		s.populateTodoActiveBlockers(ctx, list)
	}
	views, waits := projection.ViewsAndWaits(facts, now)
	if len(facts) > 0 && facts[0].TodosV2 {
		applyActiveBlockersToViews(list, views, waits)
	}
	return todoProjection{facts: facts, views: views, waits: waits}
}

// populateTodoActiveBlockers loads all active persistent blockers in a single DB query and
// attaches them to each matching listedTodo. Callers must invoke this only when todos_v2 is enabled.
func (s *Service) populateTodoActiveBlockers(ctx context.Context, list []listedTodo) {
	if len(list) == 0 {
		return
	}
	st := s.todoBlockerDB()
	if st == nil {
		return
	}
	rows, err := st.ListActiveTodoBlockers(ctx)
	if err != nil {
		s.logf("todo_blockers: list active: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	byTodo := make(map[string][]todoActiveBlockerView)
	for _, r := range rows {
		if r.TodoID == "" {
			continue
		}
		byTodo[r.TodoID] = append(byTodo[r.TodoID], todoActiveBlockerView{
			ID:        r.ID,
			Kind:      r.Kind,
			TargetRef: r.TargetRef,
			State:     r.State,
			Attempts:  r.Attempts,
		})
	}
	for todoID := range byTodo {
		slices.SortFunc(byTodo[todoID], func(a, b todoActiveBlockerView) int {
			return cmp.Or(
				strings.Compare(a.ID, b.ID),
				strings.Compare(a.Kind, b.Kind),
				strings.Compare(a.TargetRef, b.TargetRef),
			)
		})
	}
	for i := range list {
		if blockers := byTodo[list[i].ID]; len(blockers) > 0 {
			list[i].ActiveBlockers = blockers
		}
	}
}

// applyActiveBlockersToViews projects open to-dos with active persistent blockers into waiting
// when they are not already in needs_you, can_wait, working, waiting, just_finished, someday, or done.
func applyActiveBlockersToViews(list []listedTodo, views []assistantwire.TodoSummary, waits []*assistantwire.TodoWait) {
	for i := range list {
		if i >= len(views) || len(list[i].ActiveBlockers) == 0 {
			continue
		}
		if list[i].DoneAt != nil && list[i].StartError == "" {
			continue
		}
		if views[i].Section != assistantwire.TodoListSectionTodo {
			continue
		}
		views[i].Phase = assistantwire.TodoPhaseWaiting
		views[i].Section = assistantwire.TodoListSectionWaiting
		views[i].Reason = assistantwire.TodoReasonParked
		views[i].Line = &assistantwire.TodoLine{
			Key: assistantwire.TodoLineKeyParked,
			Args: map[string]string{
				assistantwire.TodoLineArgReason: activeBlockerWaitReason(list[i].ActiveBlockers),
			},
		}
		if i < len(waits) {
			waits[i] = activeBlockerWait(list[i].ActiveBlockers)
		}
	}
}

func activeBlockerWait(blockers []todoActiveBlockerView) *assistantwire.TodoWait {
	if len(blockers) == 0 {
		return nil
	}
	first := blockers[0]
	_, _, label, canonicalURL := normalizePersistedBlockerKey(first.TargetRef)
	if label == "" {
		label = strings.TrimSpace(first.TargetRef)
	}
	target := label
	if len(blockers) > 1 {
		target = fmt.Sprintf("%s (+%d more)", label, len(blockers)-1)
	}
	kind := assistantwire.TodoWaitKindExternalEvent
	if first.Kind == todoBlockerKindExternalPR {
		kind = assistantwire.TodoWaitKindPrMerge
	}
	return &assistantwire.TodoWait{
		Kind:      kind,
		WaitingOn: target,
		URL:       canonicalURL,
	}
}

func activeBlockerWaitReason(blockers []todoActiveBlockerView) string {
	if len(blockers) == 0 {
		return ""
	}
	first := blockers[0]
	_, _, label, _ := normalizePersistedBlockerKey(first.TargetRef)
	if label == "" {
		label = strings.TrimSpace(first.TargetRef)
	}
	prefix := "Waiting on "
	if first.Kind == todoBlockerKindExternalPR {
		prefix = "Waiting on PR "
	}
	if len(blockers) == 1 {
		return prefix + label
	}
	return fmt.Sprintf("%s%s (+%d more)", prefix, label, len(blockers)-1)
}

// mergePersistedSharedBlockers merges persistent active todo_blockers shared by 2+ open to-dos
// with projection.SharedBlockers, deduplicating by normalized key and preserving union TodoIDs.
// Callers must invoke this only when todos_v2 is enabled.
func mergePersistedSharedBlockers(list []listedTodo, p todoProjection, existing []projection.SharedBlocker) []projection.SharedBlocker {
	type mergedGroup struct {
		key     string
		kind    string
		label   string
		url     string
		todoIDs map[string]struct{}
	}
	byKey := make(map[string]*mergedGroup, len(existing))
	addEntry := func(key, kind, label, u string, ids []string) {
		if key == "" {
			return
		}
		g := byKey[key]
		if g == nil {
			g = &mergedGroup{
				key:     key,
				kind:    kind,
				label:   label,
				url:     u,
				todoIDs: make(map[string]struct{}, len(ids)),
			}
			byKey[key] = g
		} else {
			if g.url == "" && u != "" {
				g.url = u
			}
			if label != "" && (g.label == "" || label < g.label) {
				g.label = label
			}
		}
		for _, id := range ids {
			if id != "" {
				g.todoIDs[id] = struct{}{}
			}
		}
	}

	for _, sb := range existing {
		addEntry(sb.Key, sb.Kind, sb.Label, sb.URL, sb.TodoIDs)
	}

	persistedByKey := make(map[string]*mergedGroup)
	for i := range list {
		if len(list[i].ActiveBlockers) == 0 {
			continue
		}
		if list[i].DoneAt != nil && list[i].StartError == "" {
			continue
		}
		if i < len(p.views) {
			v := p.views[i]
			if v.Phase == assistantwire.TodoPhaseDone || v.Phase == assistantwire.TodoPhaseSomeday ||
				v.Section == assistantwire.TodoListSectionDone || v.Section == assistantwire.TodoListSectionSomeday {
				continue
			}
		}
		for _, b := range list[i].ActiveBlockers {
			key, kind, label, u := normalizePersistedBlockerKey(b.TargetRef)
			if key == "" {
				continue
			}
			g := persistedByKey[key]
			if g == nil {
				g = &mergedGroup{
					key:     key,
					kind:    kind,
					label:   label,
					url:     u,
					todoIDs: make(map[string]struct{}),
				}
				persistedByKey[key] = g
			}
			g.todoIDs[list[i].ID] = struct{}{}
		}
	}

	for _, pg := range persistedByKey {
		if len(pg.todoIDs) < 2 && byKey[pg.key] == nil {
			continue
		}
		ids := make([]string, 0, len(pg.todoIDs))
		for id := range pg.todoIDs {
			ids = append(ids, id)
		}
		addEntry(pg.key, pg.kind, pg.label, pg.url, ids)
	}

	if len(byKey) == 0 {
		return nil
	}
	out := make([]projection.SharedBlocker, 0, len(byKey))
	for _, g := range byKey {
		if len(g.todoIDs) < 2 {
			continue
		}
		ids := make([]string, 0, len(g.todoIDs))
		for id := range g.todoIDs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		out = append(out, projection.SharedBlocker{
			Key:     g.key,
			Kind:    g.kind,
			Label:   g.label,
			URL:     g.url,
			TodoIDs: ids,
			Count:   len(ids),
		})
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(a, b projection.SharedBlocker) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Key, b.Key))
	})
	return out
}

// normalizePersistedBlockerKey mirrors projection.SharedBlockers key normalization so
// persistent todo_blockers deduplicate identically against parked/PR shared blockers.
func normalizePersistedBlockerKey(raw string) (key, kind, label, canonicalURL string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", ""
	}
	if !strings.ContainsAny(raw, " \t\r\n") {
		clean := raw
		if idx := strings.IndexAny(clean, "#?"); idx >= 0 {
			clean = clean[:idx]
		}
		if sep := strings.Index(clean, "://"); sep > 0 {
			scheme := strings.ToLower(clean[:sep])
			if scheme == "http" || scheme == "https" {
				rest := clean[sep+3:]
				host := rest
				path := ""
				if slash := strings.IndexByte(rest, '/'); slash >= 0 {
					host = rest[:slash]
					path = rest[slash:]
				}
				host = strings.ToLower(host)
				if host != "" && !strings.Contains(host, "@") {
					trimmedPath := strings.TrimRight(path, "/")
					var segs []string
					if trimmed := strings.Trim(trimmedPath, "/"); trimmed != "" {
						segs = strings.Split(trimmed, "/")
					}
					if len(segs) >= 4 {
						if n, err := strconv.Atoi(segs[3]); err == nil && n > 0 {
							owner := strings.ToLower(segs[0])
							repo := strings.ToLower(segs[1])
							num := segs[3]
							switch strings.ToLower(segs[2]) {
							case "pull":
								u := scheme + "://" + host + "/" + owner + "/" + repo + "/pull/" + num
								return projection.SharedBlockerKindPullRequest + ":" + u,
									projection.SharedBlockerKindPullRequest,
									owner + "/" + repo + "#" + num,
									u
							case "issues":
								u := scheme + "://" + host + "/" + owner + "/" + repo + "/issues/" + num
								return projection.SharedBlockerKindIssue + ":" + u,
									projection.SharedBlockerKindIssue,
									owner + "/" + repo + "#" + num,
									u
							}
						}
					}
					for i := 2; i+2 < len(segs); i++ {
						if segs[i] != "-" {
							continue
						}
						if n, err := strconv.Atoi(segs[i+2]); err != nil || n <= 0 {
							continue
						}
						repoPath := strings.ToLower(strings.Join(segs[:i], "/"))
						num := segs[i+2]
						switch strings.ToLower(segs[i+1]) {
						case "merge_requests":
							u := scheme + "://" + host + "/" + repoPath + "/-/merge_requests/" + num
							return projection.SharedBlockerKindPullRequest + ":" + u,
								projection.SharedBlockerKindPullRequest,
								repoPath + "!" + num,
								u
						case "issues":
							u := scheme + "://" + host + "/" + repoPath + "/-/issues/" + num
							return projection.SharedBlockerKindIssue + ":" + u,
								projection.SharedBlockerKindIssue,
								repoPath + "#" + num,
								u
						}
					}
					u := scheme + "://" + host + trimmedPath
					return "url:" + u, projection.SharedBlockerKindExternalTarget, host + trimmedPath, u
				}
			}
		}
	}
	collapsed := strings.Join(strings.Fields(raw), " ")
	if collapsed == "" {
		return "", "", "", ""
	}
	return "target:" + strings.ToLower(collapsed), projection.SharedBlockerKindExternalTarget, collapsed, ""
}

// populateTodoRepoGates computes and attaches the weakest-link Target Repo delivery gate summary
// to each listed to-do that has bound repositories, and returns the loaded repository bindings by to-do ID.
// Callers must invoke this only when todos_v2 is enabled.
func (s *Service) populateTodoRepoGates(ctx context.Context, list []listedTodo) map[string][]repository.TaskRepository {
	ids := make([]string, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	reposByTodo := s.loadOpenTodoRepositories(ctx, ids)
	if len(reposByTodo) == 0 {
		return reposByTodo
	}
	for i := range list {
		bound := reposByTodo[list[i].ID]
		if len(bound) == 0 {
			continue
		}
		list[i].RepoGate = aggregateRepoGate(buildRepoGateInputs(list[i].Todo, bound))
	}
	return reposByTodo
}

func buildRepoGateInputs(t Todo, bound []repository.TaskRepository) []repoGateRepo {
	prByURL := make(map[string]TodoPRStatus, len(t.OtherPRs)+1)
	for _, pr := range t.OtherPRs {
		if key := normalizeRepoGateURL(pr.URL); key != "" {
			prByURL[key] = pr
		}
	}
	if t.PRStatus != nil {
		if key := normalizeRepoGateURL(t.PRStatus.URL); key != "" {
			prByURL[key] = *t.PRStatus
		}
	} else if key := normalizeRepoGateURL(t.PRURL); key != "" {
		if _, exists := prByURL[key]; !exists {
			prByURL[key] = TodoPRStatus{URL: strings.TrimSpace(t.PRURL), State: "open"}
		}
	}

	out := make([]repoGateRepo, 0, len(bound))
	for _, repo := range bound {
		repoKey := strings.TrimSpace(repo.SourceSnapshot.Identity.CanonicalURL)
		if repoKey == "" {
			repoKey = strings.TrimSpace(repo.RepoID)
		}
		repoLabel := strings.TrimSpace(repo.SourceSnapshot.Identity.Path)
		if repoLabel == "" {
			repoLabel = strings.TrimSpace(repo.DirectoryAlias)
		}
		if repoLabel == "" {
			repoLabel = repoKey
		}

		isTarget := len(repo.Links) == 0
		refPRs := map[string]bool{}
		for _, l := range repo.Links {
			role := strings.TrimSpace(l.Role)
			if role == "" || role == repository.LinkRoleDelivery {
				isTarget = true
			} else if role == repository.LinkRoleReference {
				if key := normalizeRepoGateURL(l.CanonicalURL); key != "" {
					refPRs[key] = true
				}
			}
		}

		var prs []repoGatePR
		if isTarget {
			seenPRs := map[string]bool{}
			for _, l := range repo.Links {
				role := strings.TrimSpace(l.Role)
				if role == repository.LinkRoleReference || l.Kind != "pull_request" {
					continue
				}
				key := normalizeRepoGateURL(l.CanonicalURL)
				if key == "" || seenPRs[key] {
					continue
				}
				seenPRs[key] = true
				if obs, ok := prByURL[key]; ok {
					prs = append(prs, toRepoGatePR(l.CanonicalURL, obs))
				} else {
					prs = append(prs, repoGatePR{URL: l.CanonicalURL, State: "open"})
				}
			}
			// Fall back to observed PRs belonging to this repository when not explicitly linked yet,
			// while strictly excluding any URL marked as a reference link.
			if repoPrefix := normalizeRepoGateURL(repo.SourceSnapshot.Identity.CanonicalURL); repoPrefix != "" {
				prefix := repoPrefix + "/"
				for key, obs := range prByURL {
					if seenPRs[key] || refPRs[key] || !strings.HasPrefix(key, prefix) {
						continue
					}
					seenPRs[key] = true
					prs = append(prs, toRepoGatePR(obs.URL, obs))
				}
			}
		}

		out = append(out, repoGateRepo{
			RepoKey:   repoKey,
			RepoLabel: repoLabel,
			IsTarget:  isTarget,
			PRs:       prs,
		})
	}
	return out
}

func toRepoGatePR(fallbackURL string, obs TodoPRStatus) repoGatePR {
	u := strings.TrimSpace(obs.URL)
	if u == "" {
		u = strings.TrimSpace(fallbackURL)
	}
	return repoGatePR{
		URL:       u,
		State:     obs.State,
		Draft:     obs.Draft,
		Review:    obs.Review,
		Checks:    obs.Checks,
		Conflicts: obs.Conflicts,
	}
}

func normalizeRepoGateURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.IndexAny(raw, "#?"); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.ToLower(strings.TrimRight(raw, "/"))
}

// applyTodosV2FactsConfig copies resolved todos_v2 projection SLA preferences onto f.
// When todos_v2 is off, f is untouched so legacy projection remains byte-for-byte identical.
func applyTodosV2FactsConfig(f *projection.Facts, cfg Settings) {
	if !cfg.todosV2On() {
		return
	}
	f.TodosV2 = true
	if h := cfg.todosV2PendingReviewSLAHours(); h == 0 {
		f.PendingReviewSLADisabled = true
	} else {
		f.PendingReviewSLAHours = h
	}
	if h := cfg.todosV2CanWaitSLAHours(); h == 0 {
		f.CanWaitSLADisabled = true
	} else {
		f.CanWaitSLAHours = h
	}
}

// hasParkInteractionLink says whether this park's projection must retain its workers' pending cards, even after release.
func hasParkInteractionLink(v *Supervision) bool {
	return v != nil && v.State == reviewParked && v.Parked != nil && v.Parked.Evidence != nil && v.Parked.Evidence.Kind == parkExternalEvent && v.Parked.Evidence.InteractionID != ""
}

// todoFactsOf are the rows of list as the projection reads them, with their active runs' sessions and those runs'
// pending cards (todoRunCards), which say what a run that asks is asking (todoProjectionOf; todo_view_push.go works
// out the views of a part of the list from them).
func (s *Service) todoFactsOf(ctx context.Context, list []listedTodo) []projection.Facts {
	want := map[string]map[string]bool{} // by computer, the session ids of the open to-dos' active runs
	for i := range list {
		t := &list[i]
		linked := hasParkInteractionLink(t.Supervision)
		if t.DoneAt != nil && t.StartError == "" {
			continue
		}
		for _, a := range t.Agents {
			if a.ReleasedAt != nil && !linked || a.DismissedAt != nil {
				continue
			}
			if want[a.MachineID] == nil {
				want[a.MachineID] = map[string]bool{}
			}
			want[a.MachineID][a.SessionID] = true
		}
	}
	sessions, failed := map[[2]string]proto.Session{}, map[string]bool{}
	for machine, ids := range want {
		l, err := s.todoRunSessions(ctx, machine, ids)
		if err != nil {
			failed[machine] = true
			continue
		}
		for _, c := range l {
			c.MachineID = machine
			sessions[[2]string{machine, c.SessionID}] = c
		}
	}
	cards := s.todoRunCards(want)
	loc := s.location(ctx)
	cfg := s.settings(ctx)
	facts := make([]projection.Facts, len(list))
	for i := range list {
		t := &list[i]
		linked := hasParkInteractionLink(t.Supervision)
		facts[i] = projectionFacts(*t, sessions, loc)
		applyTodosV2FactsConfig(&facts[i], cfg)
		if t.DoneAt != nil && t.StartError == "" {
			continue
		}
		for _, a := range t.Agents {
			if a.ReleasedAt != nil && !linked || a.DismissedAt != nil {
				continue
			}
			// A computer that did not answer lists nothing: its runs read as offline, and the to-do gets no Inactive
			// mark.
			facts[i].Unreached = facts[i].Unreached || failed[a.MachineID]
			facts[i].Interactions = append(facts[i].Interactions, cards[[2]string{a.MachineID, a.SessionID}]...)
		}
	}
	return facts
}

// todoRunCards are the pending cards of the sessions in want (by computer, the session ids), by computer and
// session, oldest first. A card of a space's conversation is marked Private on this copy, as the hub's push treats it
// (hub/push_payload.go askQuestion), so the view lets neither be answered from a row or a lock screen.
func (s *Service) todoRunCards(want map[string]map[string]bool) map[[2]string][]proto.Interaction {
	if len(want) == 0 {
		return nil
	}
	cards := s.pendingOf(want)
	for _, l := range cards {
		for i := range l {
			l[i].Private = l[i].Private || identity.InSpace(l[i].Cwd)
		}
		slices.SortFunc(l, func(a, b proto.Interaction) int {
			if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
	}
	return cards
}

// todoRunSessions are those of machine's sessions whose id is in want, each as the apps get it: the node's rows read
// as listedSession reads one, and for one the node no longer lists, the archive's row (archivedRow), which reads as
// offline as in the machine's whole list. Neither the computer's other sessions nor its archive's months of records
// are copied: a list read is on every GET /api/v1/todos. Without the fleet in this process, from the node's whole list
// (sessions). An error when the computer is not there or does not answer.
func (s *Service) todoRunSessions(ctx context.Context, machine string, want map[string]bool) ([]proto.Session, error) {
	if !s.fleetOn() || machine == DeviceAll {
		list, err := s.sessions(ctx, machine)
		if err != nil {
			return nil, err
		}
		kept := make([]proto.Session, 0, len(want))
		for i := range list {
			if want[list[i].SessionID] {
				kept = append(kept, list[i])
			}
		}
		return kept, nil
	}
	m, ok := s.fleetMachine(machine)
	if !ok {
		return nil, ErrNoDevice // as sessions.list says
	}
	mark := s.archiveMark()
	rows, _ := s.nodeSessionsWhere(m.ID, func(c *proto.Session) bool { return want[c.SessionID] })
	list, err := s.archivedListed(ctx, m.ID, s.decorateInventory(m.ID, rows, false), mark)
	if err != nil {
		s.logf("load agent archives: %v", err)
	}
	if len(list) < len(want) {
		listed := make(map[string]bool, len(list))
		for i := range list {
			listed[list[i].SessionID] = true
		}
		for sid := range want {
			if listed[sid] {
				continue
			}
			row, found, err := s.archivedRow(ctx, m.ID, sid)
			if err != nil {
				s.logf("load agent archives: %v", err)
			}
			if found {
				list = append(list, row)
			}
		}
	}
	return s.deletionRows(ctx, machine, list, false), nil
}

// byID is the view of each to-do, by id.
func (p todoProjection) byID() map[string]assistantwire.TodoSummary {
	out := make(map[string]assistantwire.TodoSummary, len(p.views))
	for i, f := range p.facts {
		out[f.ID] = p.views[i]
	}
	return out
}

// counts are the list's numbers.
func (p todoProjection) counts() assistantwire.TodoCounts { return projection.Counts(p.facts, p.views) }

// sharedBlockers are external blockers shared by two or more open to-dos when todos_v2 is enabled.
func (p todoProjection) sharedBlockers() []projection.SharedBlocker {
	return projection.SharedBlockers(p.facts, p.views)
}

// todoPlaces are the list's places, each project's titled by the knowledge base (todoProjectTitles).
func (s *Service) todoPlaces(ctx context.Context, p todoProjection) assistantwire.TodoPlaces {
	return projection.Places(p.facts, p.views, s.todoProjectTitles(ctx, p.facts))
}

// todoProjectTitles are the titles of the live projects the to-dos are in, by slug, as ownProject finds them; a
// project it does not find, or a knowledge base that cannot be read, leaves the place titled by its slug.
func (s *Service) todoProjectTitles(ctx context.Context, facts []projection.Facts) map[string]string {
	e, err := s.kbGet(ctx)
	if err != nil {
		return nil
	}
	out, looked := map[string]string{}, map[string]bool{}
	for _, f := range facts {
		if f.Project == "" || looked[f.Project] {
			continue
		}
		looked[f.Project] = true
		if p := ownProject(e, Todo{Project: f.Project}); p != nil && p.Name != "" {
			out[f.Project] = p.Name
		}
	}
	return out
}

// projectionFacts is listed row t as the projection reads it: the fields a decoder of the row's JSON would fill (the
// test TestProjectionFactsMatchTheList holds the two together), the sessions of its active runs, and the owner's zone.
func projectionFacts(t listedTodo, sessions map[[2]string]proto.Session, loc *time.Location) projection.Facts {
	linked := hasParkInteractionLink(t.Supervision)
	f := projection.Facts{ID: t.ID, Title: t.Title, DoneAt: t.DoneAt, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		Origin: t.Origin, PlanID: t.PlanID, StartError: t.StartError, DoneBy: t.DoneBy, ReopenedAt: t.ReopenedAt,
		CancelledAt: t.CancelledAt, CancelledReason: t.CancelledReason, Quiet: t.Quiet, PRURL: t.PRURL,
		DueAt: t.DueAt, DueDate: t.DueDate, Priority: t.Priority, Project: t.Project, Kind: t.Kind, SeenAt: t.SeenAt,
		PartOf: t.PartOf, After: slices.Clone(t.After), Loc: loc}
	f.RestoredAt, f.SomedayUntil = t.RestoredAt, t.SomedayUntil // the owner's marks on the hub (todo_marks.go)
	if t.Agents != nil {
		f.Agents = make([]projection.Agent, len(t.Agents))
	}
	for i, a := range t.Agents {
		f.Agents[i] = projection.Agent{MachineID: a.MachineID, SessionID: a.SessionID, ReleasedAt: a.ReleasedAt,
			Title: a.Title, StartError: a.StartError, DismissedAt: a.DismissedAt}
		if c, ok := sessions[[2]string{a.MachineID, a.SessionID}]; ok && (a.ReleasedAt == nil || linked) && a.DismissedAt == nil {
			f.Sessions = append(f.Sessions, c)
		}
	}
	if v := t.Supervision; v != nil {
		f.Supervision = &projection.Supervision{State: v.State, Supervisor: v.Supervisor, PendingSince: v.PendingSince, UpdatedAt: v.UpdatedAt}
		if p := v.Parked; p != nil {
			parked := &projection.Parked{Reason: p.Reason, Until: p.Until, WaitingOn: p.WaitingOn}
			if p.Evidence != nil {
				ev := &projection.ParkEvidence{
					Kind:          p.Evidence.Kind,
					Target:        p.Evidence.Target,
					URL:           p.Evidence.URL,
					Wake:          p.Evidence.Wake,
					InteractionID: p.Evidence.InteractionID,
				}
				if c := p.Evidence.Contact; c != nil {
					ev.Contact = &assistantwire.TodoWaitContact{
						Channel: c.Channel,
						URL:     c.URL,
						Sender:  c.Sender,
						SentAt:  c.SentAt,
						Request: c.Request,
					}
				}
				parked.Evidence = ev
			}
			if h := p.Handoff; h != nil {
				parked.Handoff = &assistantwire.TodoHandoffRef{
					TodoID: wireids.TodoID(h.TodoID),
					Title:  h.Title,
					Check:  h.Check,
				}
			}
			f.Supervision.Parked = parked
		}
		if v.Reviews != nil {
			f.Supervision.Reviews = make([]projection.Review, len(v.Reviews))
		}
		for i, r := range v.Reviews {
			f.Supervision.Reviews[i] = projection.Review{At: r.At, Verdict: r.Verdict, Note: r.Note, Reason: r.Reason,
				Recommend: r.Recommend}
		}
		if v.Progress != nil {
			f.Supervision.Progress = &projection.Progress{At: v.Progress.At}
		}
	}
	if p := t.PRStatus; p != nil {
		f.PRStatus = &projection.PRStatus{URL: p.URL, Title: p.Title, State: p.State, Draft: p.Draft,
			Review: p.Review, Checks: p.Checks, Conflicts: p.Conflicts, CheckedAt: p.CheckedAt}
	}
	for _, p := range t.OtherPRs {
		f.OtherPRs = append(f.OtherPRs, projection.PRStatus{URL: p.URL, Title: p.Title, State: p.State, Draft: p.Draft,
			Review: p.Review, Checks: p.Checks, Conflicts: p.Conflicts, CheckedAt: p.CheckedAt})
	}
	if d := t.Delivery; d != nil {
		f.Delivery = &projection.Delivery{State: d.State, At: d.At, Owner: d.waitsForOwner(), Gate: d.Gate}
	}
	if a := t.Ask; a != nil {
		f.Ask = &projection.Ask{Outcome: a.Outcome, Verdict: a.Verdict, Recommend: a.Recommend, AskedAt: a.AskedAt,
			Risk: a.Risk, Question: a.Question, Deadline: a.Deadline, Default: a.Default, ForBriefing: a.ForBriefing}
		for _, o := range a.Options {
			f.Ask.Options = append(f.Ask.Options, assistantwire.OwnerAskOption{Label: o.Label, LeadsTo: o.LeadsTo})
		}
	}
	if r := t.DecisionReview; r != nil {
		f.DecisionReview = &projection.DecisionReview{ReviewAt: r.ReviewAt, DismissalID: r.DismissalID}
	}
	return f
}
