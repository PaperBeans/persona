package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	w "github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

const (
	externalInboxSweepInterval = 15 * time.Second
	externalInboxRetry         = time.Minute
	externalInboxBatchSize     = 12
)

type externalInboxReady struct {
	work      w.ExternalInboxWork
	selection externalSelection
	plan      externalPlan
}

func (s *Service) externalInboxLoop(ctx context.Context) {
	s.inbox.init()
	tick := time.NewTicker(externalInboxSweepInterval)
	defer tick.Stop()
	for {
		cctx, stop := context.WithTimeout(ctx, 90*time.Second)
		if err := s.sweepExternalInbox(cctx, time.Now()); err != nil && cctx.Err() == nil {
			s.logf("inbox: external routing: %v", err)
		}
		stop()
		select {
		case <-ctx.Done():
			return
		case <-s.inbox.externalWake:
		case <-tick.C:
		}
	}
}

func (s *Service) sweepExternalInbox(ctx context.Context, now time.Time) error {
	store := s.externalInboxDB()
	if store == nil {
		return nil
	}
	work, err := store.ReadyExternalInboxEvents(ctx, now, 1)
	if err != nil || len(work) == 0 {
		return err
	}
	if err := s.inboxReady(ctx); err != nil {
		return err
	}
	return store.WithExternalInboxSweep(ctx, func() error {
		return s.routeReadyExternalInbox(ctx, store, now)
	})
}

func (s *Service) routeReadyExternalInbox(ctx context.Context, store externalInboxStore, now time.Time) error {
	work, err := store.ReadyExternalInboxEvents(ctx, now, 64)
	if err != nil || len(work) == 0 {
		return err
	}
	cfg, err := s.inboxConfigFresh(ctx)
	if err != nil {
		return err
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	groups := map[string][]externalInboxReady{}
	var groupOrder []string
	var failures []error
	for _, ev := range work {
		ready := externalInboxReady{work: ev}
		visible, err := s.externalInboxVisible(ctx, store, ev, cfg.Visibility)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !visible {
			failures = append(failures, store.SaveExternalInboxProgress(ctx, ev.Key, now, nil, true))
			continue
		}
		selection, err := s.externalSelection(ctx, store, ev)
		if err != nil {
			failures = append(failures, err, store.SaveExternalInboxProgress(ctx, ev.Key, now.Add(externalInboxRetry), ev.Plan, false))
			continue
		}
		ready.selection = selection
		if ev.Plan != nil {
			ready.plan = *ev.Plan
			failures = append(failures, s.deliverExternalPlan(ctx, store, now, ready))
			continue
		}
		if selection.readyAt.After(now) {
			failures = append(failures, store.SaveExternalInboxProgress(ctx, ev.Key, selection.readyAt, nil, false))
			continue
		}
		if plan, ok := deterministicExternalPlan(ev, selection); ok {
			ready.plan = plan
			failures = append(failures, s.admitExternalPlan(ctx, store, now, ready))
			continue
		}
		// A digest groups events with the same candidates at the selected scope.
		// Realtime ambiguity is resolved independently as soon as it arrives.
		key := ev.Key
		if selection.readyAt.After(ev.ReceivedAt) {
			raw, _ := json.Marshal(struct {
				Scope      w.ExternalRef
				At         time.Time
				Candidates []externalCandidateBrief
			}{selection.scope, selection.readyAt, externalBriefs(selection)})
			key = string(raw)
		}
		if len(groups[key]) == 0 {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], ready)
	}
	for _, key := range groupOrder {
		for group := range slices.Chunk(groups[key], externalInboxBatchSize) {
			failures = append(failures, s.triageExternalInboxBatch(ctx, store, now, group))
		}
	}
	return errors.Join(failures...)
}

func (s *Service) externalSelection(ctx context.Context, store externalInboxStore, work w.ExternalInboxWork) (externalSelection, error) {
	config, err := store.ExternalRoutingConfiguration(ctx, work.Path)
	if err != nil {
		return externalSelection{}, err
	}
	if len(config.Bindings) == 0 && len(config.Policies) == 0 {
		return externalSelection{}, nil
	}
	briefs, err := s.externalTargetBriefs(ctx, config)
	if err != nil {
		return externalSelection{}, err
	}
	return selectExternalCandidates(work, config, func(key externalTarget) (externalCandidateBrief, bool) {
		brief, ok := briefs[key]
		return brief, ok
	})
}

func (s *Service) externalTargetBriefs(ctx context.Context, config w.ExternalRoutingConfiguration) (map[externalTarget]externalCandidateBrief, error) {
	out := map[externalTarget]externalCandidateBrief{}
	needDirections := slices.ContainsFunc(config.Lookup.Matches, func(m w.ExternalBindingMatch) bool {
		return m.TargetKind == w.ExternalTargetDirection
	})
	for _, m := range config.Lookup.Matches {
		if m.TargetKind != w.ExternalTargetTodo {
			continue
		}
		key := externalTarget{ID: m.TargetID, Kind: m.TargetKind}
		if t, ok := s.todoNow(string(m.TargetID)); ok && taskOpen(t) && !isAskTodo(t.ID) {
			out[key] = externalCandidateBrief{externalTarget: key, Title: cleanExternalText(t.Title, 120),
				Scope: cleanExternalText(t.Notes, 180), Status: "open"}
		}
	}
	if !needDirections {
		return out, nil
	}
	b := &s.directions
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.directionsLocked(ctx) {
		return nil, errors.New("could not read external routing directions")
	}
	for _, m := range config.Lookup.Matches {
		if m.TargetKind != w.ExternalTargetDirection {
			continue
		}
		key := externalTarget{ID: m.TargetID, Kind: m.TargetKind}
		if d := b.m[string(m.TargetID)]; d != nil && d.EndedAt == nil {
			title := directionName(d, "", d.Project)
			if title == "" && d.Told != nil {
				title = d.Told.Title
			}
			out[key] = externalCandidateBrief{externalTarget: key, Title: cleanExternalText(title, 120),
				Scope: cleanExternalText(d.Notes, 180), Status: "active"}
		}
	}
	return out, nil
}

func (s *Service) externalInboxVisible(ctx context.Context, store externalInboxStore, work w.ExternalInboxWork, cfg *w.InboxVisibilityConfig) (bool, error) {
	b := &s.inbox
	id := string(work.InboxID)
	b.mu.Lock()
	it := b.items[id]
	if it == nil {
		b.mu.Unlock()
		raw, err := store.LoadExternalInboxItem(ctx, work.InboxID)
		if err != nil || len(raw) == 0 {
			return false, err
		}
		var row InboxItem
		if err := json.Unmarshal(raw, &row); err != nil {
			return false, err
		}
		b.mu.Lock()
		it = b.items[id]
		if it == nil {
			b.stored(&row)
			b.items[id] = &row
			it = &row
		}
	}
	defer b.mu.Unlock()
	return it.open() && !it.VisibilityExcluded && slices.Equal(it.ExternalPath, work.Path) &&
		s.inboxVisibilityProviderAllowed(cfg, it.Source) && !inboxVisibilityWithMembersExcludedLocked(b, it, cfg), nil
}

func externalBriefs(selection externalSelection) []externalCandidateBrief {
	briefs := make([]externalCandidateBrief, 0, len(selection.candidates))
	for _, c := range selection.candidates {
		briefs = append(briefs, c.externalCandidateBrief)
	}
	return briefs
}

func externalUpdateText(work w.ExternalInboxEvent) string {
	if work.Summary == "" || work.Summary == work.Title {
		return work.Title
	}
	return work.Title + "\n" + work.Summary
}

func (s *Service) triageExternalInboxBatch(ctx context.Context, store externalInboxStore, now time.Time, group []externalInboxReady) error {
	input := externalTriageInput{Candidates: externalBriefs(group[0].selection)}
	for _, ev := range group {
		input.Summaries = append(input.Summaries, externalUpdateText(ev.work.ExternalInboxEvent))
	}
	triage := s.inbox.externalTriage
	if triage == nil {
		triage = s.askExternalInboxTriage
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	verdicts, err := triage(tctx, input)
	cancel()
	if err == nil && len(verdicts) != len(group) {
		err = errExternalVerdict
	}
	if err != nil {
		failures := []error{err}
		for _, ev := range group {
			failures = append(failures, store.SaveExternalInboxProgress(ctx, ev.work.Key, now.Add(externalInboxRetry), nil, false))
		}
		return errors.Join(failures...)
	}
	cfg, err := s.inboxConfigFresh(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for i, ev := range group {
		visible, err := s.externalInboxVisible(ctx, store, ev.work, cfg.Visibility)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !visible {
			failures = append(failures, store.SaveExternalInboxProgress(ctx, ev.work.Key, now, nil, true))
			continue
		}
		plan, err := externalPlanFromVerdict(ev.work, ev.selection, verdicts[i])
		if err != nil {
			failures = append(failures, err, store.SaveExternalInboxProgress(ctx, ev.work.Key, now.Add(externalInboxRetry), nil, false))
			continue
		}
		// Policy and visibility can change while the model is answering.
		ev.selection, err = s.externalSelection(ctx, store, ev.work)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if verdicts[i].Disposition == "ignore" {
			if err := s.foldExternalChatter(ctx, ev.work); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		ev.plan = plan
		failures = append(failures, s.admitExternalPlan(ctx, store, now, ev))
	}
	return errors.Join(failures...)
}

func (s *Service) askExternalInboxTriage(ctx context.Context, input externalTriageInput) ([]externalVerdict, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Results []externalVerdict `json:"results"`
	}
	system := prompts.RenderExternalInboxTriage(answerLanguage(s.hello(ctx).Locale))
	err = s.inboxModelJSON(ctx, purposeTriage, system, string(raw), 400+len(input.Summaries)*250, &answer)
	return answer.Results, err
}
