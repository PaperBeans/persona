package health

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

// WorkoutReceived persists only strength windows, before the core acknowledges the upload.
func (s *Service) WorkoutReceived(ctx context.Context, w assistantwire.StrengthWorkout) error {
	var report struct {
		Activity string `json:"activity"`
	}
	if err := json.Unmarshal(w.Report, &report); err != nil {
		return err
	}
	if report.Activity != assistantwire.WorkoutActivityStrength {
		return nil
	}
	st := s.h.StrengthStore()
	if st == nil {
		return errors.New("strength storage unavailable")
	}
	linked, err := st.HealthStrengthWorkoutAdd(ctx, w)
	if err == nil && linked > 0 {
		s.strengthChanged(ctx)
	}
	return err
}

type strengthReportSet struct {
	Weight float64 `json:"weight"`
	Reps   int     `json:"reps"`
	Warmup bool    `json:"warmup"`
	Record bool    `json:"record"`
}
type strengthReportPrevious struct {
	Day  string              `json:"day"`
	Sets []strengthReportSet `json:"sets"`
}
type strengthReportExercise struct {
	Exercise string                  `json:"exercise"`
	Sets     []strengthReportSet     `json:"sets"`
	Previous *strengthReportPrevious `json:"previous,omitempty"`
	Best     map[string]float64      `json:"best"`
}

func strengthReportInputs(rows, prior []assistantwire.StrengthSet, es []assistantwire.StrengthExercise, loc *time.Location) []strengthReportExercise {
	out := []strengthReportExercise{}
	for _, e := range es {
		current := strengthExerciseSets(rows, e.ID)
		if len(current) == 0 {
			continue
		}
		slices.Reverse(current)
		history := strengthExerciseSets(prior, e.ID)
		best := strengthBest(history)
		hasBaseline := slices.ContainsFunc(history, func(v assistantwire.StrengthSet) bool { return !v.Warmup })
		item := strengthReportExercise{Exercise: e.Name, Sets: []strengthReportSet{}, Best: best}
		if len(history) > 0 {
			day := history[0].At.In(loc).Format("2006-01-02")
			item.Previous = &strengthReportPrevious{Day: day, Sets: []strengthReportSet{}}
			for _, v := range history {
				if v.At.In(loc).Format("2006-01-02") == day {
					item.Previous.Sets = append(item.Previous.Sets, strengthReportSet{Weight: v.Weight, Reps: v.Reps, Warmup: v.Warmup})
				}
			}
			slices.Reverse(item.Previous.Sets)
		}
		for _, v := range current {
			record := hasBaseline && !v.Warmup && (v.Weight > best["weight"] || v.Weight*(1+float64(v.Reps)/30) > best["e1rm"])
			item.Sets = append(item.Sets, strengthReportSet{Weight: v.Weight, Reps: v.Reps, Warmup: v.Warmup, Record: record})
		}
		out = append(out, item)
	}
	return out
}

// WorkoutReport reads current data for each run; it never changes the stored task or draft.
func (s *Service) WorkoutReport(ctx context.Context, id, language string) (assistantwire.StrengthReportPrompt, bool, error) {
	st := s.h.StrengthStore()
	if st == nil {
		return assistantwire.StrengthReportPrompt{}, false, nil
	}
	added, err := s.strengthAdded(ctx)
	if err != nil || !added {
		return assistantwire.StrengthReportPrompt{}, false, err
	}
	w, err := st.HealthStrengthWorkout(ctx, id)
	if err != nil || w == nil {
		return assistantwire.StrengthReportPrompt{}, false, err
	}
	rows, err := st.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Workout: id})
	if err != nil {
		return assistantwire.StrengthReportPrompt{}, false, err
	}
	prior, err := st.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Until: w.Start})
	if err != nil {
		return assistantwire.StrengthReportPrompt{}, false, err
	}
	// Pre-start sets in the workout's inclusive window are current sets, never comparison baselines.
	prior = slices.DeleteFunc(prior, func(v assistantwire.StrengthSet) bool { return v.WorkoutID != nil && *v.WorkoutID == id })
	es, err := st.HealthStrengthExercises(ctx, true)
	if err != nil {
		return assistantwire.StrengthReportPrompt{}, false, err
	}
	strength := &prompts.WorkoutStrength{}
	if len(rows) > 0 {
		raw, err := json.Marshal(strengthReportInputs(rows, prior, es, s.h.Location(ctx)))
		if err != nil {
			return assistantwire.StrengthReportPrompt{}, false, err
		}
		strength.Sets = string(raw)
	}
	drafts, err := st.HealthStrengthDrafts(ctx, "open")
	if err != nil {
		return assistantwire.StrengthReportPrompt{}, false, err
	}
	day := w.Start.In(s.h.Location(ctx)).Format("2006-01-02")
	for _, d := range drafts {
		if d.Kind == "plan" && d.PlanDay != nil && d.PlanDay.Format("2006-01-02") == day {
			var body strengthDraftBody
			if err = json.Unmarshal(d.Body, &body); err != nil {
				return assistantwire.StrengthReportPrompt{}, false, err
			}
			plan, err := strengthReportPlan(body.Sets, es)
			if err != nil {
				return assistantwire.StrengthReportPrompt{}, false, err
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				return assistantwire.StrengthReportPrompt{}, false, err
			}
			strength.PlanDraft, strength.Plan = d.ID, string(raw)
			break
		}
	}
	return assistantwire.StrengthReportPrompt{Prompt: prompts.RenderWorkoutReport(prompts.WorkoutReport{Data: string(w.Report), Language: language, Strength: strength}), Start: w.Start}, true, nil
}

type strengthReportPlanSet struct {
	Exercise string  `json:"exercise"`
	Weight   float64 `json:"weight"`
	Reps     int     `json:"reps"`
	Warmup   bool    `json:"warmup,omitempty"`
}

func strengthReportPlan(sets []draftSet, es []assistantwire.StrengthExercise) ([]strengthReportPlanSet, error) {
	names := map[string]string{}
	for _, e := range es {
		names[e.ID] = e.Name
	}
	out := make([]strengthReportPlanSet, 0, len(sets))
	for _, v := range sets {
		name := names[v.Exercise]
		if name == "" || v.Weight == nil {
			return nil, errors.New("plan exercise or weight is missing")
		}
		out = append(out, strengthReportPlanSet{Exercise: name, Weight: *v.Weight, Reps: v.Reps, Warmup: v.Warmup})
	}
	return out, nil
}
