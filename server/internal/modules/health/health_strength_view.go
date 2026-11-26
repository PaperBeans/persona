package health

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func drawStrengthPage(p strengthPageState, es []assistantwire.StrengthExercise, rows []assistantwire.StrengthSet, drafts []assistantwire.StrengthDraft, d *assistantwire.StrengthDraft, now time.Time, loc *time.Location, l i18n.Lang, version int) wire.View {
	tr := func(key string, args ...i18n.Args) string { return strengthText(l, key, args...) }
	text := func(v string) wire.Component { return wire.Component{Type: "text", Text: v} }
	button := func(key, id string) wire.Component {
		return wire.Component{Type: "button", Title: tr(key), Action: &wire.Action{ID: id, Submit: p.Page == "strength.edit" && id != "cancel"}}
	}
	row := func(title, subtitle, id string) wire.Component {
		v := wire.Component{Type: "row", Title: title, Subtitle: subtitle}
		if id != "" {
			v.Action = &wire.Action{ID: id}
			var target *strengthPageState
			if strings.HasPrefix(id, "exercise:") {
				target = &strengthPageState{Page: "strength.exercise", Exercise: strings.TrimPrefix(id, "exercise:"), Days: 30, Metric: "weight"}
			}
			if strings.HasPrefix(id, "draft:") {
				target = &strengthPageState{Page: "strength.edit", Draft: strings.TrimPrefix(id, "draft:"), Days: 30, Metric: "weight"}
			}
			if target != nil {
				raw, _ := json.Marshal(target)
				v.Action.Push = &wire.Push{State: string(raw), Title: title}
			}
		}
		return v
	}
	section := func(title string, items []wire.Component) wire.Section {
		if len(items) == 0 {
			items = []wire.Component{text(tr("empty"))}
		}
		return wire.Section{Title: tr(title), Items: items}
	}
	names := map[string]string{}
	for _, e := range es {
		names[e.ID] = e.Name
	}
	page := wire.View{Title: tr("title"), Sections: []wire.Section{}}
	week := strengthDay(now, loc)
	week = week.AddDate(0, 0, -(int(week.Weekday())+6)%7)
	switch p.Page {
	case "strength.overview":
		pending, plans := []wire.Component{}, []wire.Component{}
		for _, d := range strengthSlice(drafts, p.DraftsOffset, 40) {
			title := tr("pending")
			if d.Kind == "plan" {
				title = tr("plan")
			}
			v := row(title, d.UpdatedAt.In(loc).Format("2006-01-02 15:04"), "draft:"+d.ID)
			pending = append(pending, v)
			if d.Kind == "plan" {
				plans = append(plans, v)
			}
		}
		if p.DraftsOffset+40 < len(drafts) {
			pending = append(pending, button("more", "more_drafts"))
		}
		if p.DraftsOffset > 0 {
			pending = append(pending, button("first", "first_drafts"))
		}
		if len(pending) > 0 {
			page.Sections = append(page.Sections, section("waiting", pending))
		}
		items := []wire.Component{}
		for _, e := range strengthSlice(es, p.ExercisesOffset, 30) {
			sets := strengthExerciseSets(rows, e.ID)
			summary := tr("empty")
			if len(sets) > 0 {
				summary = tr("last_best", i18n.Args{"last": sets[0].At.In(loc).Format("2006-01-02") + " · " + strengthSetLabel(sets[0].Weight, sets[0].Reps), "best": strengthBest(sets)["weight"]})
			}
			items = append(items, row(e.Name, summary, "exercise:"+e.ID))
			if p.Manage {
				for _, key := range []string{"rename", "setting", "rule", "delete"} {
					v := button(key, key+":"+e.ID)
					if key == "delete" {
						v.Role = "destructive"
						v.Action.Confirm = tr("delete_confirm", i18n.Args{"name": e.Name, "count": len(sets)})
					}
					items = append(items, v)
				}
			}
		}
		if p.ExercisesOffset+30 < len(es) {
			items = append(items, button("more", "more_exercises"))
		}
		if p.ExercisesOffset > 0 {
			items = append(items, button("first", "first_exercises"))
		}
		page.Sections = append(page.Sections, section("exercises", items))
		if p.Manage && p.Field != "" {
			value := ""
			for _, e := range es {
				if e.ID == p.Exercise {
					switch p.Field {
					case "rename":
						value = e.Name
					case "setting":
						value = e.Setting
					case "rule":
						value = e.WeightRule
					}
				}
			}
			if p.EditValue != nil {
				value = *p.EditValue
			}
			field := wire.Component{Type: "field", Name: "value", Label: tr(p.Field), Kind: "text", Value: value}
			if p.Field == "rule" {
				field.Kind = "choice"
				for _, rule := range []string{"stack", "per_hand", "with_bar", "added"} {
					field.Choices = append(field.Choices, wire.Choice{Value: rule, Title: tr("rule_" + rule)})
				}
			}
			save := button("save", "update")
			save.Action.Submit = true
			page.Sections = append(page.Sections, section("manage", []wire.Component{field, save}))
		}
		muscles := map[string]int{}
		for _, e := range es {
			for _, v := range strengthExerciseSets(rows, e.ID) {
				if v.At.Before(week) || v.At.After(now) || v.Warmup {
					continue
				}
				for _, m := range e.Primary {
					muscles[m]++
				}
			}
		}
		keys := []string{}
		for m := range muscles {
			keys = append(keys, m)
		}
		sort.Strings(keys)
		items = nil
		for _, m := range strengthSlice(keys, p.MusclesOffset, 30) {
			items = append(items, row(m, tr("set_count", i18n.Args{"count": muscles[m]}), ""))
		}
		if p.MusclesOffset+30 < len(keys) {
			items = append(items, button("more", "more_muscles"))
		}
		if p.MusclesOffset > 0 {
			items = append(items, button("first", "first_muscles"))
		}
		page.Sections = append(page.Sections, section("muscles", items))
		page.Sections = append(page.Sections, section("last_session", strengthSessionRows(rows, es, loc, l)))
		for _, d := range drafts {
			if d.Kind == "plan" {
				plans = []wire.Component{row(tr("plan"), d.PlanDay.Format("2006-01-02"), "draft:"+d.ID)}
				break
			}
		}
		if len(plans) > 0 {
			page.Sections = append(page.Sections, section("next_plan", plans[:1]))
		}
		key := "manage"
		if p.Manage {
			key = "done"
		}
		page.Sections = append(page.Sections, wire.Section{Items: []wire.Component{button(key, key)}})
	case "strength.exercise":
		for _, e := range es {
			if e.ID == p.Exercise {
				page.Title = e.Name
				info := []wire.Component{text(tr("rule_" + e.WeightRule))}
				if e.Setting != "" {
					info = append(info, text(e.Setting))
				}
				page.Sections = append(page.Sections, section("setting", info))
			}
		}
		sets := strengthPageSets(rows, p, now, loc)
		items := []wire.Component{}
		for _, v := range sets {
			label := v.At.In(loc).Format("2006-01-02 15:04") + " · " + strengthSetLabel(v.Weight, v.Reps)
			if v.Warmup {
				label += " · " + tr("warmup")
			}
			item := row(label, "", "undo:"+v.ID)
			item.Value = tr("undo")
			items = append(items, item)
		}
		if len(sets) == 40 {
			items = append(items, button("older", "older"))
		}
		if p.Offset > 0 {
			items = append(items, button("latest", "latest"))
		}
		all := strengthExerciseSets(rows, p.Exercise)
		days := map[string]float64{}
		from := strengthDay(now, loc).AddDate(0, 0, 1-p.Days)
		for _, v := range all {
			if v.Warmup || v.At.Before(from) || v.At.After(now) {
				continue
			}
			day := v.At.In(loc).Format("2006-01-02")
			value := v.Weight
			if p.Metric == "e1rm" {
				value *= 1 + float64(v.Reps)/30
			}
			days[day] = max(days[day], value)
		}
		keys := []string{}
		for day := range days {
			keys = append(keys, day)
		}
		sort.Strings(keys)
		points := []wire.ChartPoint{}
		for _, day := range keys {
			points = append(points, wire.ChartPoint{Label: day, Value: days[day]})
		}
		// One point per day; sample long ranges to the chart protocol's 120-point bound.
		if len(points) > 120 {
			sample := []wire.ChartPoint{}
			for i := range 120 {
				sample = append(sample, points[i*(len(points)-1)/119])
			}
			points = sample
		}
		chart := []wire.Component{{Type: "segmented", Name: "metric", Value: p.Metric, Choices: []wire.Choice{{Value: "weight", Title: tr("weight")}, {Value: "e1rm", Title: tr("metric_e1rm")}}, Action: &wire.Action{ID: "metric"}}, {Type: "segmented", Name: "days", Value: strconv.Itoa(p.Days), Choices: []wire.Choice{{Value: "30", Title: tr("days", i18n.Args{"days": 30})}, {Value: "90", Title: tr("days", i18n.Args{"days": 90})}, {Value: "365", Title: tr("days", i18n.Args{"days": 365})}}, Action: &wire.Action{ID: "range"}}}
		if len(points) > 0 {
			chart = append(chart, wire.Component{Type: "chart", Kind: "line", Unit: "kg", Series: []wire.ChartSeries{{Name: tr(p.Metric), Points: points}}})
		}
		page.Sections = append(page.Sections, section("progress", chart), section("history", items))
	case "strength.edit":
		page.Title = tr("adjust")
		if d.Status != "open" {
			page.Sections = append(page.Sections, wire.Section{Items: []wire.Component{text(tr("status_" + d.Status))}})
			break
		}
		choices := []wire.Choice{}
		for _, e := range es {
			choices = append(choices, wire.Choice{Value: e.ID, Title: e.Name})
		}
		for i, v := range p.Work {
			label := names[v.Exercise]
			weight := 0.0
			if v.Weight != nil {
				weight = *v.Weight
			}
			items := []wire.Component{text(label + " · " + strengthSetLabel(weight, v.Reps))}
			if version >= 6 {
				action := "set:" + strconv.Itoa(i)
				number := func(key string, value, step, min, max float64) wire.Component {
					return wire.Component{Type: "row", Key: fmt.Sprintf("%s_%d", key, i), Title: tr(key), Control: &wire.RowControl{Kind: "stepper", Name: fmt.Sprintf("%s_%d", key, i), Value: &value, Step: &step, Min: &min, Max: &max, Action: wire.Action{ID: action, Submit: true}}}
				}
				items = []wire.Component{{Type: "field", Kind: "choice", Label: tr("exercises"), Name: fmt.Sprintf("exercise_%d", i), Value: v.Exercise, Choices: choices}, number("weight", weight, 2.5, 0, 1000), number("reps", float64(v.Reps), 1, 1, 1000), {Type: "row", Key: fmt.Sprintf("warmup_%d", i), Title: tr("warmup"), Control: &wire.RowControl{Kind: "switch", Name: fmt.Sprintf("warmup_%d", i), On: v.Warmup, Action: wire.Action{ID: action, Submit: true}}}}
				if len(p.Work) > 1 {
					items = append(items, button("remove", "remove:"+strconv.Itoa(i)))
				}
			}
			page.Sections = append(page.Sections, wire.Section{Title: tr("set_number", i18n.Args{"number": i + 1}), Items: items})
		}
		actions := []wire.Component{button("cancel", "cancel")}
		if version >= 6 {
			if len(p.Work) < 40 {
				actions = append([]wire.Component{button("add", "add")}, actions...)
			}
			actions = append(actions, button("save", "save"))
		} else {
			actions = append([]wire.Component{text(tr("update_app"))}, actions...)
		}
		page.Sections = append(page.Sections, wire.Section{Items: actions})
	case "strength.home":
		items := strengthSessionRows(rows, es, loc, l)
		if len(rows) == 0 {
			items = []wire.Component{text(tr("start_hint"))}
		}
		from := strengthDay(now, loc).AddDate(0, 0, -6)
		counts := map[string]float64{}
		for _, v := range rows {
			if !v.Warmup && v.DeletedAt == nil && !v.At.Before(from) && !v.At.After(now) {
				counts[v.At.In(loc).Format("2006-01-02")]++
			}
		}
		points := []wire.ChartPoint{}
		for i := range 7 {
			day := from.AddDate(0, 0, i)
			points = append(points, wire.ChartPoint{Label: day.Format("01-02"), Value: counts[day.Format("2006-01-02")]})
		}
		chart := wire.Component{Type: "chart", Kind: "bar", Title: tr("sets"), Unit: tr("sets_unit"), Series: []wire.ChartSeries{{Name: tr("sets"), Points: points}}}
		page.Sections = append(page.Sections, section("recent_days", []wire.Component{chart}))
		if len(rows) > 0 {
			records := strengthRecentRecords(rows, es, now, loc, l)
			if len(records) > 0 {
				page.Sections = append(page.Sections, section("records", records))
			}
		}
		page.Sections = append([]wire.Section{section("last_session", items)}, page.Sections...)
		open := button("view", "overview")
		open.Action.Push = &wire.Push{State: `{"page":"strength.overview"}`, Title: tr("title")}
		page.Sections = append(page.Sections, wire.Section{Items: []wire.Component{open}})
	}
	raw, _ := json.Marshal(p)
	page.State = string(raw)
	return page
}

func strengthSetLabel(weight float64, reps int) string {
	return fmt.Sprintf("%g kg × %d", weight, reps)
}
func strengthExerciseSets(rows []assistantwire.StrengthSet, id string) []assistantwire.StrengthSet {
	out := []assistantwire.StrengthSet{}
	for _, v := range rows {
		if v.ExerciseID == id {
			out = append(out, v)
		}
	}
	return out
}
func strengthSessionRows(rows []assistantwire.StrengthSet, es []assistantwire.StrengthExercise, loc *time.Location, l i18n.Lang) []wire.Component {
	if len(rows) == 0 {
		return nil
	}
	last := rows[0]
	counts := map[string]int{}
	total := 0
	for _, v := range rows {
		same := v.At.In(loc).Format("2006-01-02") == last.At.In(loc).Format("2006-01-02")
		if last.WorkoutID != nil {
			same = v.WorkoutID != nil && *v.WorkoutID == *last.WorkoutID
		}
		if same {
			counts[v.ExerciseID]++
			total++
		}
	}
	summary := last.At.In(loc).Format("2006-01-02") + " · " + strengthText(l, "session_count", i18n.Args{"exercises": len(counts), "sets": total})
	out := []wire.Component{{Type: "text", Text: summary}}
	for _, e := range es {
		if n := counts[e.ID]; n > 0 {
			out = append(out, wire.Component{Type: "row", Title: e.Name, Value: strengthText(l, "set_count", i18n.Args{"count": n})})
			if len(out) == 31 {
				break
			}
		}
	}
	return out
}

func strengthSlice[T any](vs []T, offset, limit int) []T {
	offset = min(offset, len(vs))
	return vs[offset:min(offset+limit, len(vs))]
}
func strengthRecentRecords(rows []assistantwire.StrengthSet, es []assistantwire.StrengthExercise, now time.Time, loc *time.Location, l i18n.Lang) []wire.Component {
	out := []wire.Component{}
	from := strengthDay(now, loc).AddDate(0, 0, -29)
	for _, e := range es {
		sets := strengthExerciseSets(rows, e.ID)
		slices.Reverse(sets)
		best := map[string]float64{"weight": 0, "e1rm": 0}
		hasBaseline := false
		for _, v := range sets {
			if v.Warmup {
				continue
			}
			b := strengthBest([]assistantwire.StrengthSet{v})
			record := hasBaseline && (b["weight"] > best["weight"] || b["e1rm"] > best["e1rm"])
			best["weight"], best["e1rm"] = max(best["weight"], b["weight"]), max(best["e1rm"], b["e1rm"])
			hasBaseline = true
			if record && !v.At.Before(from) && !v.At.After(now) {
				out = append(out, wire.Component{Type: "row", Title: e.Name, Subtitle: v.At.In(loc).Format("2006-01-02"), Value: strengthSetLabel(v.Weight, v.Reps)})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b wire.Component) int {
		if a.Subtitle > b.Subtitle {
			return -1
		}
		if a.Subtitle < b.Subtitle {
			return 1
		}
		return 0
	})
	return out[:min(3, len(out))]
}
