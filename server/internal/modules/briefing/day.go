package briefing

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/modapi"
)

// The evening routine's facts about today's all-day recordings (#4741): those whose span overlaps the owner's today,
// as at the recap time, their parts merged by time. For the model only: never a row of the card, the brief item or a
// channel's message (BriefSnapshot keeps it apart in Day), so the routine's answer is where the user reads about the
// day besides the recap's own push.

const (
	DayRunes   = 4000 // what the section may take, all of it (BoundDay): entries from the middle of the day go first
	DayReasons = 6    // gap reasons given (those with the most time; the phone's own six fit); the rest are counted together
	DayRanges  = 5    // stretches of one gap reason given with their times (the longest); the rest are counted
	DayParts   = 5    // parts given (the first and the latest); those from the middle are counted
	DaySummary = 300
	DayTitle   = 60
	DayWhat    = 100

	recapStatePending = "pending"
	recapStateFailed  = "failed"
)

// Day is today's all-day recording as the evening routine's facts say it, at now in the owner's zone loc; nil without
// one today (or without the knowledge base).
func (r *runner) Day(ctx context.Context, now time.Time, loc *time.Location) *modapi.BriefDay {
	if r.dayRecap == nil {
		return nil
	}
	if loc == nil {
		loc = time.UTC
	}
	recs := r.dayRecap.TodaysRecordings(ctx, now, loc)
	if len(recs) == 0 {
		return nil
	}
	today := now.In(loc).Format(time.DateOnly)
	clock := func(t time.Time) string {
		if t = t.In(loc); t.Format(time.DateOnly) != today {
			return t.Format("01-02 15:04")
		}
		return t.Format("15:04")
	}
	type entry struct {
		at time.Time
		en modapi.BriefDayEntry
	}
	type action struct {
		at time.Time
		a  modapi.BriefDayAction
	}
	var gaps []dayGap
	var entries []entry
	var actions []action
	var first, last, text time.Time
	type gapKey struct {
		from, to int64
		reason   string
	}
	seen := map[gapKey]bool{}
	d := &modapi.BriefDay{Date: today}
	for i := range recs {
		rec := &recs[i]
		at := func(sec float64) time.Time { return rec.StartedAt.Add(time.Duration(sec * float64(time.Second))) }
		to := rec.Duration
		if rec.HasCoverage {
			to = rec.CoverageTo
			for _, g := range rec.Gaps {
				// A later part holds the day's coverage as the earlier one did (the parts share started_at).
				x := dayGap{at(g.From), at(g.To), g.Reason}
				if k := (gapKey{x.from.UnixNano(), x.to.UnixNano(), x.reason}); !seen[k] {
					seen[k] = true
					gaps = append(gaps, x)
				}
			}
		}
		// Recorded is its span (until now while it is open); how far its text goes is said apart.
		start, end := rec.SpanStart, rec.SpanEnd
		if first.IsZero() || start.Before(first) {
			first = start
		}
		if end.After(last) {
			last = end
		}
		if t := at(to); t.After(text) {
			text = t
		}
		part := modapi.BriefDayPart{Part: max(rec.Part, 1)}
		switch {
		case rec.Recap != nil:
			rc := rec.Recap
			part.Made, part.Upto, part.Final, part.Partial = clock(rc.GeneratedAt), clock(at(rc.CoversTo)), rc.Final, rc.Partial
			part.Summary = clip(oneLine(rc.Summary), DaySummary)
			if rec.RecapState == recapStatePending || rec.RecapState == recapStateFailed { // a newer one, the one shown stays
				part.State = rec.RecapState
			}
			for _, en := range rc.Timeline {
				entries = append(entries, entry{at(en.From), modapi.BriefDayEntry{
					From:    clock(at(en.From)),
					To:      clock(at(en.To)),
					Meeting: en.Meeting,
					Title:   clip(oneLine(en.Title), DayTitle),
				}})
			}
			for _, a := range rc.Actions {
				if a.TodoID == "" { // confirmed ones are to-dos the routine reads as such
					actions = append(actions, action{at(a.At), modapi.BriefDayAction{Who: oneLine(a.Who), What: clip(oneLine(a.What), DayWhat)}})
				}
			}
		case rec.Chars == 0:
			part.State, part.Recorded = "none", int(math.Round(rec.Duration/60))
		default:
			part.State = rec.RecapState
		}
		d.Parts = append(d.Parts, part)
	}
	d.From, d.To = clock(first), clock(last)
	if t := clock(text); text.Before(last) && t != d.To {
		d.Transcribed = t
	}
	d.Gaps = dayGaps(gaps, clock)
	slices.SortStableFunc(entries, func(a, b entry) int { return a.at.Compare(b.at) })
	for _, x := range entries {
		d.Timeline = append(d.Timeline, x.en)
	}
	slices.SortStableFunc(actions, func(a, b action) int { return a.at.Compare(b.at) })
	for _, x := range actions {
		d.Actions = append(d.Actions, x.a)
	}
	r.BoundDay(d)
	return d
}

// dayGap is a stretch of the day with no text and why (coverage gap at its clock times).
type dayGap struct {
	from, to time.Time
	reason   string
}

// dayGaps are gaps by reason (in the order the reasons first come in the day): how many, how long in all, and the
// longest DayRanges of them, by time.
func dayGaps(gaps []dayGap, clock func(time.Time) string) []modapi.BriefDayGap {
	slices.SortStableFunc(gaps, func(a, b dayGap) int { return a.from.Compare(b.from) })
	var order []string
	by := map[string][]dayGap{}
	for _, g := range gaps {
		if _, ok := by[g.reason]; !ok {
			order = append(order, g.reason)
		}
		by[g.reason] = append(by[g.reason], g)
	}
	length := func(g dayGap) time.Duration { return g.to.Sub(g.from) }
	var out []modapi.BriefDayGap
	for _, reason := range order {
		list := by[reason]
		var total time.Duration
		for _, g := range list {
			total += length(g)
		}
		longest := slices.Clone(list)
		slices.SortStableFunc(longest, func(a, b dayGap) int { return cmp.Compare(length(b), length(a)) })
		longest = longest[:min(len(longest), DayRanges)]
		slices.SortStableFunc(longest, func(a, b dayGap) int { return a.from.Compare(b.from) })
		g := modapi.BriefDayGap{
			Reason:  cmp.Or(reason, "?"),
			Count:   len(list),
			Minutes: max(1, int(math.Round(total.Minutes()))),
			More:    len(list) > len(longest),
		}
		for _, x := range longest {
			g.Ranges = append(g.Ranges, clock(x.from)+"–"+clock(x.to))
		}
		out = append(out, g)
	}
	return out
}

// BoundDay keeps d within DayRunes in all. At most DayParts parts are listed (those from the middle go, counted) and
// DayReasons gap reasons (those with the least time go, counted together): with the reasons, the summaries and the
// titles clipped, that is well within it. Then while it is longer the timeline's entries from the middle of the day go
// (its start and its latest hours say the most), then the last suggested to-dos, then the summaries are cut short.
func (r *runner) BoundDay(d *modapi.BriefDay) {
	runes := func(day *modapi.BriefDay) int {
		if r.prompts == nil {
			return 0
		}
		return r.prompts.BriefDayRunes(day)
	}
	BoundDayWith(d, runes)
}

// BoundDayWith bounds d using runes to measure rendered prompt length.
func BoundDayWith(d *modapi.BriefDay, runes func(*modapi.BriefDay) int) {
	for len(d.Parts) > DayParts {
		mid := len(d.Parts) / 2
		d.Parts = slices.Delete(d.Parts, mid, mid+1)
		d.PartsLeft++
	}
	if len(d.Gaps) > DayReasons {
		most := slices.Clone(d.Gaps)
		slices.SortStableFunc(most, func(a, b modapi.BriefDayGap) int { return cmp.Compare(b.Minutes, a.Minutes) })
		keep := map[string]bool{}
		for _, g := range most[:DayReasons] {
			keep[g.Reason] = true
		}
		d.Gaps = slices.DeleteFunc(d.Gaps, func(g modapi.BriefDayGap) bool { // in the day's order still
			if keep[g.Reason] {
				return false
			}
			d.GapReasonsLeft++
			d.GapsLeft += g.Count
			d.GapMinutesLeft += g.Minutes
			return true
		})
	}
	over := func() bool { return runes(d) > DayRunes }
	for over() && len(d.Timeline) > 0 {
		mid := len(d.Timeline) / 2
		d.Timeline = slices.Delete(d.Timeline, mid, mid+1)
		d.Left++
	}
	for over() && len(d.Actions) > 0 {
		d.Actions = d.Actions[:len(d.Actions)-1]
		d.ActionsLeft++
	}
	for i := range d.Parts {
		if !over() {
			return
		}
		d.Parts[i].Summary = clip(d.Parts[i].Summary, DaySummary/3)
	}
}
