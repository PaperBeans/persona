package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

type draftSet struct {
	Exercise string     `json:"exercise"`
	Weight   *float64   `json:"weight"`
	Reps     int        `json:"reps"`
	Warmup   bool       `json:"warmup,omitempty"`
	At       *time.Time `json:"at,omitempty"`
}
type strengthToolArgs struct {
	Action     string     `json:"action"`
	Q          string     `json:"q"`
	ID         string     `json:"id"`
	Name       *string    `json:"name"`
	Aliases    []string   `json:"aliases"`
	Primary    []string   `json:"primary"`
	Secondary  []string   `json:"secondary"`
	Equipment  *string    `json:"equipment"`
	WeightRule *string    `json:"weight_rule"`
	Setting    *string    `json:"setting"`
	Sets       []draftSet `json:"sets"`
	Plan       bool       `json:"plan"`
	Days       int        `json:"days"`
}

func (s *Service) toolHealthStrength(ctx context.Context, call assistantwire.HealthToolCall) (string, error) {
	var a strengthToolArgs
	dec := json.NewDecoder(bytes.NewReader(call.Args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return "", err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", errors.New("expected one strength argument object")
	}
	allowed := map[string]string{"find": "q", "create": "name aliases primary secondary equipment weight_rule setting", "update": "id name aliases primary secondary equipment weight_rule setting", "log": "sets", "undo": "id", "history": "id days", "draft": "sets plan", "show": "id"}
	fields, ok := allowed[a.Action]
	if !ok {
		return "", errors.New("unknown strength action")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(call.Args, &object); err != nil {
		return "", err
	}
	for key, value := range object {
		if string(value) == "null" {
			return "", errors.New("strength fields cannot be null")
		}
		if key != "action" && !slices.Contains(strings.Fields(fields), key) {
			return "", fmt.Errorf("%s is not valid for %s", key, a.Action)
		}
	}
	_, hasID := object["id"]
	if (slices.Contains([]string{"update", "undo", "show"}, a.Action) || a.Action == "history" && hasID) && !intakeUUIDPattern.MatchString(a.ID) {
		return "", errors.New("a valid id is required")
	}
	a.ID = strings.ToLower(a.ID)
	st := s.h.StrengthStore()
	if st == nil {
		return "", errors.New("strength storage is unavailable")
	}
	if a.Action != "find" && a.Action != "history" {
		added, err := s.strengthAdded(ctx)
		if err != nil {
			return "", err
		}
		if !added {
			return "Strength training is not added. Ask the owner once with ask_user; after consent use health_intake add_item item=strength, then retry.", nil
		}
	}
	s.strengthMu.Lock()
	defer s.strengthMu.Unlock()
	exercises, err := st.HealthStrengthExercises(ctx, false)
	if err != nil {
		return "", err
	}
	switch a.Action {
	case "find":
		out := []any{}
		for _, e := range exercises {
			hay := e.Name + " " + strings.Join(e.Aliases, " ") + " " + e.Equipment
			if !strings.Contains(strengthMatch(hay), strengthMatch(a.Q)) {
				continue
			}
			rows, err := st.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Exercise: e.ID})
			if err != nil {
				return "", err
			}
			var last any
			if len(rows) > 0 {
				day := rows[0].At.In(s.h.Location(ctx)).Format("2006-01-02")
				var sets []assistantwire.StrengthSet
				for _, r := range rows {
					if r.At.In(s.h.Location(ctx)).Format("2006-01-02") == day {
						sets = append(sets, r)
					}
				}
				last = map[string]any{"at": day, "sets": sets}
			}
			out = append(out, map[string]any{"id": e.ID, "name": e.Name, "aliases": e.Aliases, "equipment": e.Equipment, "weight_rule": e.WeightRule, "setting": e.Setting, "last": last, "best": strengthBest(rows)})
			if len(out) == 20 {
				break
			}
		}
		return strengthJSON(out)
	case "create", "update":
		if a.Equipment != nil && *a.Equipment == "" || a.WeightRule != nil && *a.WeightRule == "" {
			return "", errors.New("equipment and weight_rule must use their declared values")
		}
		e := assistantwire.StrengthExercise{ID: healthIntakeUUID("strength-exercise:" + randomHex(16))}
		if a.Action == "update" {
			i := slices.IndexFunc(exercises, func(e assistantwire.StrengthExercise) bool { return e.ID == strings.ToLower(a.ID) })
			if i < 0 {
				return "", errors.New("exercise not found")
			}
			e = exercises[i]
		}
		if a.Name != nil {
			e.Name = strings.TrimSpace(*a.Name)
		}
		if a.Primary != nil {
			e.Primary = a.Primary
		}
		if a.Secondary != nil {
			e.Secondary = a.Secondary
		}
		if a.Equipment != nil {
			e.Equipment = *a.Equipment
		}
		if a.WeightRule != nil {
			e.WeightRule = *a.WeightRule
		}
		if a.Setting != nil {
			e.Setting = *a.Setting
		}
		for _, alias := range a.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" && !slices.ContainsFunc(e.Aliases, func(v string) bool { return strengthMatch(v) == strengthMatch(alias) }) {
				e.Aliases = append(e.Aliases, alias)
			}
		}
		if e.Name == "" || utf8.RuneCountInString(e.Name) > 120 || utf8.RuneCountInString(e.Setting) > 500 || len(e.Aliases) > 20 || !slices.Contains([]string{"", "machine", "dumbbell", "barbell", "cable", "bodyweight", "other"}, e.Equipment) || !slices.Contains([]string{"", "stack", "per_hand", "with_bar", "added"}, e.WeightRule) {
			return "", errors.New("invalid strength exercise")
		}
		for _, other := range exercises {
			if other.ID != e.ID && strengthMatch(other.Name) == strengthMatch(e.Name) {
				return "", fmt.Errorf("exercise already exists: %s", other.ID)
			}
		}
		saved, _, err := st.HealthStrengthExerciseSave(ctx, e)
		if err != nil {
			return "", err
		}
		s.strengthChanged(ctx)
		return strengthJSON(saved)
	case "log", "draft":
		if err := checkDraftSets(a.Sets, exercises); err != nil {
			return "", err
		}
		now := time.Now().UTC()
		for i := range a.Sets {
			a.Sets[i].Exercise = strings.ToLower(a.Sets[i].Exercise)
			if a.Sets[i].At == nil {
				at := now
				a.Sets[i].At = &at
			}
		}
		if a.Action == "draft" {
			return s.newStrengthDraft(ctx, call, a.Sets, a.Plan)
		}
		rows := make([]assistantwire.StrengthSet, len(a.Sets))
		for i, v := range a.Sets {
			rows[i] = assistantwire.StrengthSet{ID: healthIntakeUUID("strength-set:" + randomHex(16)), ExerciseID: strings.ToLower(v.Exercise), Weight: *v.Weight, Reps: v.Reps, Warmup: v.Warmup, At: *v.At}
		}
		saved, err := st.HealthStrengthSetAdd(ctx, rows)
		if err != nil {
			return "", err
		}
		s.strengthChanged(ctx)
		return strengthJSON(saved)
	case "undo":
		saved, changed, err := st.HealthStrengthSetUndo(ctx, a.ID)
		if err != nil {
			return "", err
		}
		if changed {
			s.strengthChanged(ctx)
		}
		return strengthJSON(saved)
	case "history":
		days := a.Days
		if days == 0 {
			days = 30
		}
		if _, provided := object["days"]; provided && a.Days == 0 {
			return "", errors.New("days must be 1 to 366")
		}
		if days < 1 || days > 366 {
			return "", errors.New("days must be 1 to 366")
		}
		now := time.Now().In(s.h.Location(ctx))
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1-days)
		limit := 0
		if a.ID == "" {
			limit = 500
		}
		rows, err := st.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Exercise: a.ID, From: start, Until: now, Limit: limit})
		if err != nil {
			return "", err
		}
		daysOut := map[string][]assistantwire.StrengthSet{}
		for _, v := range rows {
			day := v.At.In(now.Location()).Format("2006-01-02")
			daysOut[day] = append(daysOut[day], v)
		}
		return strengthJSON(daysOut)
	case "show":
		return s.showStrengthDraft(ctx, call, a.ID)
	}
	return "", errors.New("unknown strength action")
}
func checkDraftSets(sets []draftSet, exercises []assistantwire.StrengthExercise) error {
	if len(sets) < 1 || len(sets) > 40 {
		return errors.New("provide 1 to 40 sets")
	}
	for _, v := range sets {
		if !intakeUUIDPattern.MatchString(v.Exercise) || v.Weight == nil || math.IsNaN(*v.Weight) || math.IsInf(*v.Weight, 0) || *v.Weight < 0 || *v.Weight > 1000 || math.Abs(*v.Weight*100-math.Round(*v.Weight*100)) > 1e-8 || v.Reps < 1 || v.Reps > 1000 || v.At != nil && v.At.IsZero() {
			return errors.New("each set needs an exercise, weight in kg (two decimals), reps 1 to 1000 and a valid time")
		}
		i := slices.IndexFunc(exercises, func(e assistantwire.StrengthExercise) bool { return e.ID == strings.ToLower(v.Exercise) })
		if i < 0 {
			return errors.New("exercise not found")
		}
		if exercises[i].WeightRule == "" {
			return errors.New("ask once how this exercise's weight counts, then update its weight_rule")
		}
	}
	return nil
}
func (s *Service) strengthChanged(ctx context.Context) {
	s.event(ctx, map[string]any{"type": "health_strength"})
}
