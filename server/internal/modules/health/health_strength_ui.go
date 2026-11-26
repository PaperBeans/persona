package health

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

type strengthPageState struct {
	Page            string     `json:"page"`
	Exercise        string     `json:"exercise,omitempty"`
	Metric          string     `json:"metric,omitempty"`
	Days            int        `json:"days,omitempty"`
	Offset          int        `json:"offset,omitempty"`
	ExercisesOffset int        `json:"exercises_offset,omitempty"`
	DraftsOffset    int        `json:"drafts_offset,omitempty"`
	MusclesOffset   int        `json:"muscles_offset,omitempty"`
	Manage          bool       `json:"manage,omitempty"`
	Field           string     `json:"field,omitempty"`
	EditValue       *string    `json:"edit_value,omitempty"`
	Draft           string     `json:"draft,omitempty"`
	Rev             int64      `json:"rev,omitempty"`
	Work            []draftSet `json:"work,omitempty"`
}

func readStrengthPage(raw string) (strengthPageState, bool) {
	p := strengthPageState{Days: 30, Metric: "weight"}
	if len(raw) > 16384 || json.Unmarshal([]byte(raw), &p) != nil {
		return p, false
	}
	if !slices.Contains([]string{"strength.overview", "strength.exercise", "strength.edit", "strength.home"}, p.Page) || !slices.Contains([]int{30, 90, 365}, p.Days) || !slices.Contains([]string{"weight", "e1rm"}, p.Metric) || p.Offset < 0 || p.ExercisesOffset < 0 || p.DraftsOffset < 0 || p.MusclesOffset < 0 || len(p.Work) > 40 {
		return p, false
	}
	if p.Page == "strength.exercise" && !intakeUUIDPattern.MatchString(p.Exercise) || p.Page == "strength.edit" && (!intakeUUIDPattern.MatchString(p.Draft) || p.Rev < 0) {
		return p, false
	}
	if p.Field != "" && (!p.Manage || !intakeUUIDPattern.MatchString(p.Exercise) || !slices.Contains([]string{"rename", "setting", "rule"}, p.Field)) {
		return p, false
	}
	if p.EditValue != nil && (p.Field == "" || utf8.RuneCountInString(*p.EditValue) > 500) {
		return p, false
	}
	p.Exercise, p.Draft = strings.ToLower(p.Exercise), strings.ToLower(p.Draft)
	return p, true
}

func strengthText(l i18n.Lang, key string, args ...i18n.Args) string {
	return i18n.T(l, "assistant-health_strength."+key, args...)
}

func (s *Service) healthStrengthUI(w http.ResponseWriter, r *http.Request, req wire.UIRequest) {
	p, ok := readStrengthPage(req.State)
	bad := func() { apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-health_strength.invalid_page")) }
	if !ok {
		bad()
		return
	}
	st := s.h.StrengthStore()
	if st == nil {
		intakeUnavailable(w)
		return
	}
	ctx := r.Context()
	l := i18n.FromRequest(r).Or(s.lang(ctx))
	version := s.h.UIVersion()
	if req.Context != nil {
		l = i18n.Lang(req.Context.Locale).Or(l)
		if req.Context.UIVersion > 0 {
			version = min(version, req.Context.UIVersion)
		}
	}
	es, err := st.HealthStrengthExercises(ctx, false)
	if err != nil {
		intakeUnavailable(w)
		return
	}
	now, loc := time.Now(), s.h.Location(ctx)
	rows, err := st.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if err != nil {
		intakeUnavailable(w)
		return
	}
	drafts, err := st.HealthStrengthDrafts(ctx, "open")
	if err != nil {
		intakeUnavailable(w)
		return
	}
	var d *assistantwire.StrengthDraft
	loadDraft := func() error {
		var e error
		d, e = st.HealthStrengthDraft(ctx, p.Draft)
		if e != nil {
			return e
		}
		if d == nil {
			return errors.New("draft not found")
		}
		if p.Work == nil || p.Rev != d.Revision {
			var b strengthDraftBody
			if e = json.Unmarshal(d.Body, &b); e != nil {
				return e
			}
			p.Work, p.Rev = b.Sets, d.Revision
		}
		return nil
	}
	res := wire.ActionResult{}
	stale := false
	if p.Page == "strength.edit" {
		old := p.Rev
		if err = loadDraft(); err != nil {
			intakeUnavailable(w)
			return
		}
		stale = old > 0 && old != p.Rev
	}
	action, id, _ := strings.Cut(req.Action, ":")
	findExercise := func(id string) int {
		return slices.IndexFunc(es, func(e assistantwire.StrengthExercise) bool { return e.ID == id })
	}
	switch action {
	case "":
	case "overview":
		p = strengthPageState{Page: "strength.overview", Days: 30, Metric: "weight"}
	case "exercise":
		if p.Page != "strength.overview" || findExercise(id) < 0 {
			bad()
			return
		}
		p = strengthPageState{Page: "strength.exercise", Exercise: id, Days: 30, Metric: "weight"}
	case "draft":
		if p.Page != "strength.overview" || !slices.ContainsFunc(drafts, func(d assistantwire.StrengthDraft) bool { return d.ID == id }) {
			bad()
			return
		}
		p = strengthPageState{Page: "strength.edit", Draft: id, Days: 30, Metric: "weight"}
		if err = loadDraft(); err != nil {
			intakeUnavailable(w)
			return
		}
	case "manage", "done":
		if p.Page != "strength.overview" {
			bad()
			return
		}
		p.Manage = action == "manage"
		p.Field, p.EditValue = "", nil
	case "more_exercises", "first_exercises", "more_drafts", "first_drafts", "more_muscles", "first_muscles":
		if p.Page != "strength.overview" {
			bad()
			return
		}
		switch action {
		case "more_exercises":
			p.ExercisesOffset = min(p.ExercisesOffset, len(es)) + 30
		case "first_exercises":
			p.ExercisesOffset = 0
		case "more_drafts":
			p.DraftsOffset = min(p.DraftsOffset, len(drafts)) + 40
		case "first_drafts":
			p.DraftsOffset = 0
		case "more_muscles":
			p.MusclesOffset = min(p.MusclesOffset, math.MaxInt-30) + 30
		case "first_muscles":
			p.MusclesOffset = 0
		}
	case "rename", "setting", "rule", "delete":
		p.EditValue = nil
		i := findExercise(id)
		if p.Page != "strength.overview" || !p.Manage || i < 0 {
			bad()
			return
		}
		if action == "delete" {
			_, err = st.HealthStrengthExerciseDelete(ctx, id)
			p.Field = ""
		} else {
			p.Exercise, p.Field = id, action
		}
	case "update":
		i := findExercise(p.Exercise)
		if p.Page != "strength.overview" || !p.Manage || p.Field == "" || i < 0 {
			bad()
			return
		}
		e := es[i]
		v := strings.TrimSpace(req.Values["value"])
		switch p.Field {
		case "rename":
			if v == "" || utf8.RuneCountInString(v) > 120 {
				bad()
				return
			}
			e.Name = v
		case "setting":
			if utf8.RuneCountInString(v) > 500 {
				bad()
				return
			}
			e.Setting = v
		case "rule":
			if !slices.Contains([]string{"stack", "per_hand", "with_bar", "added"}, v) {
				bad()
				return
			}
			e.WeightRule = v
		}
		_, _, err = st.HealthStrengthExerciseSave(ctx, e)
		if errors.Is(err, assistantwire.ErrHealthStrengthName) {
			err = nil
			submitted := v
			p.EditValue = &submitted
			res.Toast = strengthText(l, "name_exists")
		} else if err == nil {
			p.Field, p.EditValue = "", nil
		}
	case "metric", "range":
		if p.Page != "strength.exercise" {
			bad()
			return
		}
		p.Offset = 0
		if action == "metric" {
			if !slices.Contains([]string{"weight", "e1rm"}, req.Values["metric"]) {
				bad()
				return
			}
			p.Metric = req.Values["metric"]
		} else {
			n, e := strconv.Atoi(req.Values["days"])
			if e != nil || !slices.Contains([]int{30, 90, 365}, n) {
				bad()
				return
			}
			p.Days = n
		}
	case "older", "latest":
		if p.Page != "strength.exercise" {
			bad()
			return
		}
		if action == "older" {
			p.Offset = min(p.Offset, len(rows)) + 40
		} else {
			p.Offset = 0
		}
	case "undo":
		if p.Page != "strength.exercise" {
			bad()
			return
		}
		visible := strengthPageSets(rows, p, now, loc)
		if slices.ContainsFunc(visible, func(v assistantwire.StrengthSet) bool { return v.ID == id }) {
			_, _, err = st.HealthStrengthSetUndo(ctx, id)
			res.Toast = strengthText(l, "undone")
		}
	case "set", "remove", "add", "save", "cancel":
		if p.Page != "strength.edit" {
			bad()
			return
		}
		if action == "cancel" {
			res.Pop = true
			break
		}
		if version < 6 {
			bad()
			return
		}
		if d.Status != "open" {
			p.Work = nil
			res.Toast = strengthText(l, "status_"+d.Status)
			break
		}
		if stale {
			res.Toast = strengthText(l, "changed")
			break
		}
		// A submit can overtake a debounced control action. Reconcile every submitted
		// value before saving or changing the working copy's row indexes.
		if applyStrengthWorkValues(p.Work, req.Values) != nil || checkDraftSets(p.Work, es) != nil {
			bad()
			return
		}
		if action == "save" {
			_, err = s.editStrengthDraft(ctx, p.Draft, p.Rev, p.Work)
			if errors.Is(err, assistantwire.ErrHealthStrengthDraftStale) || errors.Is(err, assistantwire.ErrHealthStrengthDraftSettled) {
				p.Work = nil
				err = loadDraft()
				res.Toast = strengthText(l, "changed")
			} else if err == nil {
				res.Pop = true
				res.Toast = strengthText(l, "updated")
			}
			break
		}
		if action == "add" {
			if len(p.Work) == 0 || len(p.Work) >= 40 {
				bad()
				return
			}
			last := p.Work[len(p.Work)-1]
			if last.Weight != nil {
				v := *last.Weight
				last.Weight = &v
			}
			p.Work = append(p.Work, last)
			break
		}
		index, e := strconv.Atoi(id)
		if e != nil || index < 0 || index >= len(p.Work) {
			bad()
			return
		}
		if action == "remove" {
			if len(p.Work) < 2 {
				bad()
				return
			}
			p.Work = slices.Delete(p.Work, index, index+1)
			break
		}
	default:
		bad()
		return
	}
	if err != nil {
		intakeUnavailable(w)
		return
	}
	if slices.Contains([]string{"delete", "update", "undo"}, action) {
		s.strengthChanged(ctx)
		es, err = st.HealthStrengthExercises(ctx, false)
		if err == nil {
			rows, err = st.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
		}
		if err != nil {
			intakeUnavailable(w)
			return
		}
	}
	view := drawStrengthPage(p, es, rows, drafts, d, now, loc, l, version)
	if err = s.h.FitPage(&view, version, l); err != nil {
		s.logf("strength page: %v", err)
		intakeUnavailable(w)
		return
	}
	res.View = &view
	intakeJSON(w, res)
}

func applyStrengthWorkValues(work []draftSet, values map[string]string) error {
	for i := range work {
		v := &work[i]
		suffix := "_" + strconv.Itoa(i)
		if value, ok := values["exercise"+suffix]; ok {
			v.Exercise = strings.ToLower(value)
		}
		if value, ok := values["weight"+suffix]; ok {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return err
			}
			v.Weight = &n
		}
		if value, ok := values["reps"+suffix]; ok {
			n, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			v.Reps = n
		}
		if value, ok := values["warmup"+suffix]; ok {
			if value != "true" && value != "false" {
				return errors.New("invalid warmup value")
			}
			v.Warmup = value == "true"
		}
	}
	return nil
}

func strengthPageSets(rows []assistantwire.StrengthSet, p strengthPageState, now time.Time, loc *time.Location) []assistantwire.StrengthSet {
	day := strengthDay(now, loc).AddDate(0, 0, 1-p.Days)
	out := []assistantwire.StrengthSet{}
	for _, v := range rows {
		if v.ExerciseID == p.Exercise && !v.At.Before(day) && !v.At.After(now) {
			out = append(out, v)
		}
	}
	offset := min(p.Offset, len(out))
	return out[offset:min(offset+40, len(out))]
}
func strengthDay(t time.Time, loc *time.Location) time.Time {
	v := t.In(loc)
	return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, loc)
}
