package assistant

import (
	"strconv"
	"strings"
	"sync"
	"time"

	crondesc "github.com/lnquy/cron"
	"github.com/robfig/cron/v3"

	"github.com/nwverse/persona/server/internal/i18n"
)

// A cron task (repeat "cron", Task.Cron) runs on a standard 5-field cron expression, `minute hour day-of-month month
// day-of-week`, or an alias (@hourly, @daily / @midnight, @weekly, @monthly, @yearly / @annually), read in the user's
// time zone (docs/assistant/assistant.md "Scheduled tasks"). robfig/cron parses it and finds the next match; on top:
//   - it is matched on the wall clock, so a clock change never runs one time twice: the hour a fall-back repeats runs
//     once (its first pass), and a time the clock skips (spring forward) runs shifted by the gap (02:30 → 03:30), at
//     most once even when that is a time it runs anyway;
//   - day of month and day of week are Vixie cron's: when both are restricted (neither starts with *), a day matching
//     either one runs, otherwise both have to match. robfig takes "*/2" for restricted; Vixie does not;
//   - 7 is Sunday as well as 0;
//   - two runs are at least minEvery minutes apart, as an interval task's;
//   - @every, a seconds field and a TZ= / CRON_TZ= prefix are refused: an interval is `every`, the time zone the user's.

// cronStar is robfig's mark of a field written as * (its starBit): a day field with it leaves the other one in charge.
const cronStar = 1 << 63

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

var cronAliases = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// cronFields name the five fields in errors, with what each takes.
var cronFields = [5]string{
	"assistant-schedule.cron_minute",
	"assistant-schedule.cron_hour",
	"assistant-schedule.cron_day_of_month",
	"assistant-schedule.cron_month",
	"assistant-schedule.cron_day_of_week",
}

// cronRuns is how many upcoming runs a view, a preview and a tool's reply list.
const cronRuns = 5

type cronRule struct {
	expr   string             // as stored: trimmed, single spaces, an alias as written
	fields [5]string          // the five fields it stands for, aliases spelled out and 7 read as Sunday
	spec   *cron.SpecSchedule // matched on the wall clock, written in UTC
}

// parseCron reads a cron expression; the errors say what is wrong in the user's words (English for the tools).
func parseCron(expr string) (cronRule, error) {
	expr = strings.Join(strings.Fields(expr), " ")
	q := strconv.Quote(expr)
	if expr == "" {
		return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_empty")
	}
	if upper := strings.ToUpper(expr); strings.HasPrefix(upper, "TZ=") || strings.HasPrefix(upper, "CRON_TZ=") {
		return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_zone", i18n.Args{"cron": q})
	}
	written := strings.Fields(expr)
	if strings.HasPrefix(expr, "@") {
		if strings.EqualFold(written[0], "@every") {
			return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_every", i18n.Args{"min": minEvery})
		}
		alias, ok := cronAliases[strings.ToLower(written[0])]
		if !ok || len(written) > 1 {
			return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_alias", i18n.Args{"cron": q})
		}
		written = strings.Fields(alias)
	}
	if len(written) != 5 {
		return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_fields", i18n.Args{"cron": q, "n": len(written)})
	}
	r := cronRule{expr: expr}
	copy(r.fields[:], written)
	r.fields[4] = sundaySeven(r.fields[4])
	sched, err := cronParser.Parse(strings.Join(r.fields[:], " "))
	if err != nil {
		return cronRule{}, cronFieldError(q, written, r.fields)
	}
	spec, ok := sched.(*cron.SpecSchedule)
	if !ok { // the parser's options make only these; anything else is no cron line of ours
		return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_alias", i18n.Args{"cron": q})
	}
	spec.Location = time.UTC
	if strings.HasPrefix(r.fields[2], "*") {
		spec.Dom |= cronStar
	}
	if strings.HasPrefix(r.fields[4], "*") {
		spec.Dow |= cronStar
	}
	r.spec = spec
	if g := r.gap(); g < minEvery {
		return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_often", i18n.Args{"cron": q, "gap": g, "min": minEvery})
	}
	if spec.Next(time.Now().UTC()).IsZero() {
		return cronRule{}, i18n.ErrEnglish("assistant-schedule.cron_never", i18n.Args{"cron": q})
	}
	return r, nil
}

// cronFieldError names the first field that does not parse on its own (written: as the user wrote them).
func cronFieldError(q string, written []string, fields [5]string) error {
	for i := range fields {
		probe := []string{"*", "*", "*", "*", "*"}
		probe[i] = fields[i]
		if _, err := cronParser.Parse(strings.Join(probe, " ")); err != nil {
			return i18n.ErrEnglish("assistant-schedule.cron_field", i18n.Args{"cron": q, "field": i18n.ErrEnglish(cronFields[i]),
				"value": strconv.Quote(written[i])})
		}
	}
	return i18n.ErrEnglish("assistant-schedule.cron_fields", i18n.Args{"cron": q, "n": len(written)})
}

// sundaySeven reads a 7 in the day-of-week field as Sunday, as Vixie cron does (robfig knows only 0): "7" → "0",
// "5-7" → "5-6,0". A step over a range to 7 is left for the parser to refuse.
func sundaySeven(f string) string {
	items := strings.Split(f, ",")
	for i, it := range items {
		switch {
		case it == "7":
			items[i] = "0"
		case strings.HasSuffix(it, "-7") && !strings.Contains(it, "/"):
			items[i] = strings.TrimSuffix(it, "-7") + "-6,0"
		}
	}
	return strings.Join(items, ",")
}

// gap is the fewest minutes between two runs: within an hour, and from the last minute of one hour to the first of the
// next when two hours in a row run (23 and 0 count as in a row).
func (c cronRule) gap() int {
	var mins []int
	for m := range 60 {
		if c.spec.Minute&(1<<m) != 0 {
			mins = append(mins, m)
		}
	}
	g := 24 * 60
	for i := 1; i < len(mins); i++ {
		g = min(g, mins[i]-mins[i-1])
	}
	hours := c.spec.Hour &^ cronStar
	if hours&(hours>>1) != 0 || hours&(1<<23) != 0 && hours&1 != 0 {
		g = min(g, 60-mins[len(mins)-1]+mins[0])
	}
	return g
}

// next is the first run strictly after `after` in loc; zero when there is none in the next five years.
func (c cronRule) next(after time.Time, loc *time.Location) time.Time {
	w := wallClock(after, loc)
	// A match can come out at or before `after`: the second pass of a fall-back hour maps it back to the first.
	for range 1000 {
		if w = c.spec.Next(w); w.IsZero() {
			return time.Time{}
		}
		if at := wallInstant(w, loc); at.After(after) {
			return at
		}
	}
	return time.Time{}
}

// nextRuns are the n runs after `after`.
func (c cronRule) nextRuns(after time.Time, loc *time.Location, n int) []time.Time {
	var out []time.Time
	for at := after; len(out) < n; {
		if at = c.next(at, loc); at.IsZero() {
			break
		}
		out = append(out, at)
	}
	return out
}

// wallClock is what the wall clock in loc shows at t, written in UTC.
func wallClock(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}

// wallInstant is when the wall clock in loc shows w (a minute written in UTC). Of the two passes of a fall-back hour it
// is the first; a time the clock skips is shifted by the gap, as if the clock had not moved.
func wallInstant(w time.Time, loc *time.Location) time.Time {
	t := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), 0, 0, loc)
	_, before := t.Add(-12 * time.Hour).Zone()
	if !shows(t, w, loc) {
		return time.Unix(w.Unix()-int64(before), 0).In(loc)
	}
	if _, off := t.Zone(); before > off { // the clock went back in the last 12 hours: is there an earlier pass?
		if e := t.Add(-time.Duration(before-off) * time.Second); shows(e, w, loc) {
			return e
		}
	}
	return t
}

func shows(t, w time.Time, loc *time.Location) bool {
	l := t.In(loc)
	return l.Year() == w.Year() && l.YearDay() == w.YearDay() && l.Hour() == w.Hour() && l.Minute() == w.Minute()
}

// cronNext is the next run of a stored expression after `after`; zero when it does not parse (checkTask refuses those).
func cronNext(expr string, after time.Time, loc *time.Location) time.Time {
	r, err := parseCron(expr)
	if err != nil {
		return time.Time{}
	}
	return r.next(after, loc)
}

var cronDescriber = sync.OnceValue(func() *crondesc.ExpressionDescriptor {
	d, err := crondesc.NewDescriptor(crondesc.Use24HourTimeFormat(true),
		crondesc.SetLocales(crondesc.Locale_en, crondesc.Locale_zh_CN, crondesc.Locale_ja))
	if err != nil {
		return nil
	}
	return d
})

// text is the expression in words in l ("At 09:00, Monday through Friday"), from lnquy/cron. "" when it has none, and
// when a day field is a step from * and the other one is restricted: Vixie cron then wants both, which the words
// ("every 2 days, and on Monday") would read as either. The next runs show it then.
func (c cronRule) text(l i18n.Lang) string {
	d := cronDescriber()
	dom, dow := c.fields[2], c.fields[4]
	plain := func(f string) bool { return f == "*" || f == "?" }
	if d == nil || !plain(dom) && !plain(dow) && (strings.HasPrefix(dom, "*") || strings.HasPrefix(dow, "*")) {
		return ""
	}
	locale := crondesc.Locale_en
	switch l {
	case i18n.ZhHans:
		locale = crondesc.Locale_zh_CN
	case i18n.Ja:
		locale = crondesc.Locale_ja
	}
	s, err := d.ToDescription(strings.Join(c.fields[:], " "), locale)
	if err != nil {
		return ""
	}
	return s
}

// cronText is a stored expression in words in l; "" when it does not parse or has none.
func cronText(expr string, l i18n.Lang) string {
	r, err := parseCron(expr)
	if err != nil {
		return ""
	}
	return r.text(l)
}

// runsText lists runs for the tools: "11-12 09:00 Thu, 11-13 09:00 Fri".
func runsText(runs []time.Time, loc *time.Location) string {
	parts := make([]string, len(runs))
	for i, at := range runs {
		parts[i] = at.In(loc).Format("01-02 15:04 Mon")
	}
	return strings.Join(parts, ", ")
}

// zoneName names loc for the tools and the preview: its IANA name, and the offset now.
func zoneName(loc *time.Location, now time.Time) string {
	return loc.String() + " (UTC" + now.In(loc).Format("-07:00") + ")"
}

// cronReply is a rule as the tools' replies say it: "cron `0 9 * * 1-5` (At 09:00, Monday through Friday) in Asia/Taipei
// (UTC+08:00); next runs 11-12 09:00 Thu, 11-13 09:00 Fri, …".
func cronReply(r cronRule, loc *time.Location, now time.Time) string {
	s := "cron `" + r.expr + "`"
	if text := r.text(i18n.En); text != "" {
		s += " (" + text + ")"
	}
	return s + " in " + zoneName(loc, now) + "; next runs " + runsText(r.nextRuns(now, loc, cronRuns), loc)
}
