package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

const strengthCardPrefix = "health-strength-"

type strengthDraftBody struct {
	Sets    []draftSet          `json:"sets"`
	Settled *strengthSettlement `json:"settled,omitempty"`
}
type strengthSettlement struct {
	Action      string    `json:"action"`
	ClientMsgID string    `json:"client_msg_id"`
	Version     int64     `json:"version"`
	At          time.Time `json:"at"`
}

func (s *Service) strengthCard(ctx context.Context, d assistantwire.StrengthDraft, body strengthDraftBody, status string) (json.RawMessage, error) {
	l := s.lang(ctx)
	tr := func(key string, args ...map[string]any) string {
		if len(args) > 0 {
			return i18n.T(l, "assistant-health_strength."+key, args[0])
		}
		return i18n.T(l, "assistant-health_strength."+key)
	}
	title := tr("pending")
	if d.Kind == "plan" {
		title = tr("plan")
	}
	switch status {
	case "saved":
		title = tr("saved", map[string]any{"count": len(body.Sets)})
	case "discarded":
		title = tr("discarded")
	case "moved":
		title = tr("moved")
	}
	v := wire.View{Title: title, Fallback: title, Sections: []wire.Section{{Items: []wire.Component{}}}}
	if status != "discarded" && status != "moved" {
		exercises, err := s.h.StrengthStore().HealthStrengthExercises(ctx, true)
		if err != nil {
			return nil, err
		}
		names := map[string]string{}
		for _, e := range exercises {
			names[e.ID] = e.Name
		}
		for _, set := range body.Sets {
			name := names[set.Exercise]
			if name == "" {
				return nil, errors.New("draft exercise no longer exists")
			}
			if set.Weight == nil {
				return nil, errors.New("draft weight is missing")
			}
			subtitle := ""
			if set.Warmup {
				subtitle = tr("warmup")
			}
			v.Sections[0].Items = append(v.Sections[0].Items, wire.Component{Type: "row", Title: fmt.Sprintf("%s · %g kg × %d", name, *set.Weight, set.Reps), Subtitle: subtitle})
		}
	}
	button := func(label, id, reply, open string) {
		v.Sections[0].Items = append(v.Sections[0].Items, wire.Component{Type: "button", Title: label, Action: &wire.Action{ID: id, Reply: reply, Open: open}})
	}
	if status == "open" {
		save := tr("save")
		if d.Kind == "plan" {
			save = tr("done_plan")
		}
		button(save, "save", save, "")
		button(tr("discard"), "discard", tr("discard"), "")
		state, err := json.Marshal(map[string]any{"page": "strength.edit", "draft": d.ID, "rev": d.Revision})
		if err != nil {
			return nil, err
		}
		if link := s.h.PageLink(ctx, "pages", string(state)); link != "" {
			button(tr("adjust"), "adjust", "", link)
		} else {
			v.Sections[0].Footer = tr("adjust_hint")
		}
	} else if status == "saved" {
		v.Sections[0].Footer = tr("undo_hint")
		if link := s.h.PageLink(ctx, "pages", `{"page":"strength.overview"}`); link != "" {
			button(tr("view"), "view", "", link)
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if _, err = wire.ParseAssistantView(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
func (s *Service) newStrengthDraft(ctx context.Context, call assistantwire.HealthToolCall, sets []draftSet, plan bool) (string, error) {
	if call.Machine == "" || call.Session == "" {
		return "", errors.New("draft needs its calling Health conversation")
	}
	now := time.Now().UTC()
	id := healthIntakeUUID("strength-draft:" + randomHex(16))
	body := strengthDraftBody{Sets: sets}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	d := assistantwire.StrengthDraft{ID: id, Kind: "sets", Status: "open", Revision: 1, Body: raw, Conversation: call.Session, MachineID: call.Machine, CardID: strengthCardPrefix + id, CardVersion: 1, CreatedAt: now, UpdatedAt: now}
	if plan {
		d.Kind = "plan"
		local := now.In(s.h.Location(ctx))
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
		d.PlanDay = &day
	}
	view, err := s.strengthCard(ctx, d, body, "open")
	if err != nil {
		return "", err
	}
	c := assistantwire.StrengthCard{ID: d.CardID, Conversation: d.Conversation, MachineID: d.MachineID, FeedID: "feed_" + randomHex(8), View: view, Version: 1, At: now}
	if err := s.h.StrengthStore().HealthStrengthDraftCreate(ctx, d, c); err != nil {
		return "", err
	}
	s.h.CardShown(ctx, c)
	s.strengthChanged(ctx)
	return strengthJSON(map[string]any{"draft": id, "card": c.ID, "sets": len(sets), "message": "The card is in the conversation. Wait for the owner to tap; do not ask again."})
}
func (s *Service) showStrengthDraft(ctx context.Context, call assistantwire.HealthToolCall, id string) (string, error) {
	if call.Machine == "" || call.Session == "" {
		return "", errors.New("show needs its calling Health conversation")
	}
	st := s.h.StrengthStore()
	d, err := st.HealthStrengthDraft(ctx, id)
	if err != nil {
		return "", err
	}
	if d == nil {
		return "", errors.New("draft not found")
	}
	if d.Status != "open" {
		return "", assistantwire.ErrHealthStrengthDraftSettled
	}
	var body strengthDraftBody
	if err := json.Unmarshal(d.Body, &body); err != nil {
		return "", err
	}
	if d.Conversation == call.Session && d.MachineID == call.Machine {
		return strengthJSON(map[string]any{"draft": d.ID, "card": d.CardID, "sets": len(body.Sets)})
	}
	old, err := s.strengthCard(ctx, *d, body, "moved")
	if err != nil {
		return "", err
	}
	next := *d
	next.Revision++
	next.CardID = fmt.Sprintf("%s%s-%d", strengthCardPrefix, id, next.Revision)
	next.CardVersion = 1
	next.Conversation = call.Session
	next.MachineID = call.Machine
	view, err := s.strengthCard(ctx, next, body, "open")
	if err != nil {
		return "", err
	}
	c := assistantwire.StrengthCard{ID: next.CardID, Conversation: next.Conversation, MachineID: next.MachineID, FeedID: "feed_" + randomHex(8), View: view, Version: 1, At: time.Now().UTC()}
	if _, err := st.HealthStrengthDraftMove(ctx, id, d.Revision, old, c); err != nil {
		return "", err
	}
	s.h.CardChanged(ctx, assistantwire.StrengthCard{ID: d.CardID, Conversation: d.Conversation, MachineID: d.MachineID, View: old, Version: d.CardVersion + 1, At: c.At})
	s.h.CardShown(ctx, c)
	s.strengthChanged(ctx)
	return strengthJSON(map[string]any{"draft": id, "card": c.ID, "sets": len(body.Sets), "message": "The card is in the conversation. Wait for the owner to tap; do not ask again."})
}

// editStrengthDraft updates the same durable revision and its card; pages will use it in the next phase.
func (s *Service) editStrengthDraft(ctx context.Context, id string, revision int64, sets []draftSet) (assistantwire.StrengthDraft, error) {
	s.strengthMu.Lock()
	defer s.strengthMu.Unlock()
	st := s.h.StrengthStore()
	if st == nil {
		return assistantwire.StrengthDraft{}, errors.New("strength storage unavailable")
	}
	d, err := st.HealthStrengthDraft(ctx, id)
	if err != nil {
		return assistantwire.StrengthDraft{}, err
	}
	if d == nil {
		return assistantwire.StrengthDraft{}, errors.New("draft not found")
	}
	if d.Status != "open" {
		return *d, assistantwire.ErrHealthStrengthDraftSettled
	}
	if d.Revision != revision {
		return *d, assistantwire.ErrHealthStrengthDraftStale
	}
	es, err := st.HealthStrengthExercises(ctx, false)
	if err != nil {
		return *d, err
	}
	if err := checkDraftSets(sets, es); err != nil {
		return *d, err
	}
	now := time.Now().UTC()
	for i := range sets {
		if sets[i].At == nil {
			at := now
			sets[i].At = &at
		}
	}
	body := strengthDraftBody{Sets: sets}
	raw, err := json.Marshal(body)
	if err != nil {
		return *d, err
	}
	next := *d
	next.Revision++
	view, err := s.strengthCard(ctx, next, body, "open")
	if err != nil {
		return *d, err
	}
	saved, card, err := st.HealthStrengthDraftEdit(ctx, id, revision, raw, view, now)
	if err == nil {
		s.h.CardChanged(ctx, card)
		s.strengthChanged(ctx)
	}
	return saved, err
}
func strengthCardOutcome(d *assistantwire.StrengthDraft, a assistantwire.HealthCardAction) (assistantwire.HealthCardResult, error) {
	var body strengthDraftBody
	if err := json.Unmarshal(d.Body, &body); err != nil {
		return assistantwire.HealthCardResult{}, err
	}
	if body.Settled == nil {
		return assistantwire.HealthCardResult{}, errors.New("settled draft has no receipt")
	}
	receipt := body.Settled
	outcome := "answered"
	if receipt.Action == a.Action && receipt.ClientMsgID == a.ClientMsgID && receipt.Version == a.Version {
		outcome = "done"
	}
	return assistantwire.HealthCardResult{Outcome: outcome, Action: receipt.Action, ClientMsgID: receipt.ClientMsgID, At: receipt.At, Version: receipt.Version}, nil
}
func (s *Service) CardAction(ctx context.Context, a assistantwire.HealthCardAction) (assistantwire.HealthCardResult, error) {
	s.strengthMu.Lock()
	defer s.strengthMu.Unlock()
	st := s.h.StrengthStore()
	if st == nil {
		return assistantwire.HealthCardResult{}, errors.New("strength storage unavailable")
	}
	d, err := st.HealthStrengthDraftByCard(ctx, a.Key)
	if err != nil {
		return assistantwire.HealthCardResult{}, err
	}
	if d == nil || d.Conversation != a.Conversation {
		return assistantwire.HealthCardResult{Outcome: "not_found"}, nil
	}
	if d.Status != "open" {
		return strengthCardOutcome(d, a)
	}
	if a.Version != d.CardVersion {
		return assistantwire.HealthCardResult{Outcome: "changed"}, nil
	}
	if !a.Declared || (a.Action != "save" && a.Action != "discard") || a.ClientMsgID == "" {
		return assistantwire.HealthCardResult{Outcome: "invalid"}, nil
	}
	var body strengthDraftBody
	if err := json.Unmarshal(d.Body, &body); err != nil {
		return assistantwire.HealthCardResult{}, err
	}
	if a.Action == "save" {
		es, err := st.HealthStrengthExercises(ctx, false)
		if err != nil {
			return assistantwire.HealthCardResult{}, err
		}
		if err := checkDraftSets(body.Sets, es); err != nil {
			return assistantwire.HealthCardResult{Outcome: "invalid_draft"}, nil
		}
	}
	now := time.Now().UTC()
	body.Settled = &strengthSettlement{Action: a.Action, ClientMsgID: a.ClientMsgID, Version: a.Version, At: now}
	raw, err := json.Marshal(body)
	if err != nil {
		return assistantwire.HealthCardResult{}, err
	}
	status := "saved"
	if a.Action == "discard" {
		status = "discarded"
	}
	view, err := s.strengthCard(ctx, *d, body, status)
	if err != nil {
		return assistantwire.HealthCardResult{}, err
	}
	var card assistantwire.StrengthCard
	if a.Action == "save" {
		planAt := now
		if d.Kind == "plan" && d.PlanDay != nil {
			loc := s.h.Location(ctx)
			day := time.Date(d.PlanDay.Year(), d.PlanDay.Month(), d.PlanDay.Day(), 0, 0, 0, 0, loc)
			workout, err := st.HealthStrengthWorkoutLatest(ctx, day, day.AddDate(0, 0, 1))
			if err != nil {
				return assistantwire.HealthCardResult{}, err
			}
			if workout != nil {
				planAt = workout.Start
			}
		}
		sets := make([]assistantwire.StrengthSet, len(body.Sets))
		for i, v := range body.Sets {
			at := planAt
			if v.At != nil && d.Kind != "plan" {
				at = *v.At
			}
			sets[i] = assistantwire.StrengthSet{ID: healthIntakeUUID(fmt.Sprintf("strength:%s:%d:%d", d.ID, d.Revision, i)), ExerciseID: v.Exercise, Weight: *v.Weight, Reps: v.Reps, Warmup: v.Warmup, At: at, DraftID: &d.ID}
		}
		_, card, _, err = st.HealthStrengthDraftConfirm(ctx, d.ID, d.Revision, sets, raw, view, now)
	} else {
		_, card, err = st.HealthStrengthDraftDiscard(ctx, d.ID, d.Revision, raw, view, now)
	}
	if errors.Is(err, assistantwire.ErrHealthStrengthDraftSettled) {
		current, readErr := st.HealthStrengthDraftByCard(ctx, a.Key)
		if readErr != nil {
			return assistantwire.HealthCardResult{}, readErr
		}
		if current == nil {
			return assistantwire.HealthCardResult{Outcome: "not_found"}, nil
		}
		return strengthCardOutcome(current, a)
	}
	if errors.Is(err, assistantwire.ErrHealthStrengthDraftStale) {
		return assistantwire.HealthCardResult{Outcome: "changed"}, nil
	}
	if err != nil {
		return assistantwire.HealthCardResult{}, err
	}
	s.h.CardChanged(ctx, card)
	s.strengthChanged(ctx)
	return assistantwire.HealthCardResult{Outcome: "done", Action: a.Action, ClientMsgID: a.ClientMsgID, Version: a.Version, At: now}, nil
}
