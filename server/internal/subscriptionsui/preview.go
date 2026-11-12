package subscriptionsui

import (
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// The preview (#5278): a dry run's items drawn as the Inbox would list them (title, sender and the row's summary, time,
// the section they would go to), what each could not read, and what the recipe says that does nothing. All of it is
// the dry run's answer, shown here only and never kept.

// previewTier is where an item goes when no model can triage it (subscriptions' deliverer.fallback): "" when it waits
// for the model.
func previewTier(fallback, kind string, due bool) string {
	switch fallback {
	case "wait":
		return ""
	case "action", "fyi":
		return fallback
	}
	if due || kind == wire.ConnectorSubscriptionKindSubDeadline {
		return "action"
	}
	return "fyi"
}

// previewTime is an item's time as the Inbox writes it; what can't be read is shown as it came.
func (p *pages) previewTime(s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, *s); err == nil {
			return p.when(t)
		}
	}
	return *s
}

// initials are an avatar's letters: the first of the name, two for a Latin name of two words.
func initials(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r == '@' || r == '.' })
	if len(words) == 0 {
		return "?"
	}
	first := []rune(words[0])
	out := string(unicode.ToUpper(first[0]))
	if len(words) > 1 && first[0] < unicode.MaxLatin1 {
		if second := []rune(words[1]); second[0] < unicode.MaxLatin1 {
			out += string(unicode.ToUpper(second[0]))
		}
	}
	return out
}

// preview are a dry run's sections: its items as Inbox rows (kind and fallback say where they go), each item's
// problems under it, the recipe's warnings, the result's shape, then the checks.
func (p *pages) preview(dry *subscriptions.DryRun, kind, fallback string) []wire.Section {
	var verdicts []Verdict
	if p.try != nil && p.try.dry == dry {
		verdicts = p.try.verdicts
	}
	var out []wire.Section
	items := wire.Section{Key: "dry", Title: p.t("preview.title"), Footer: p.t("preview.footer")}
	if dry.Error != nil {
		items.Items = append(items.Items, text("dry.err", p.t("dry.error", i18n.Args{"problem": p.problemText(dry.Error)}), "secondary"))
	}
	for i, it := range dry.Items {
		r := row("dry."+itoa(i), it.Title)
		if r.Title == "" {
			r.Title = p.t("preview.no_title")
		}
		r.Avatar = &wire.Avatar{Text: initials(strings.TrimSpace(it.Sender + " " + it.Title))}
		line := it.Summary
		if it.Sender != "" {
			line = p.t("preview.line", i18n.Args{"sender": it.Sender, "summary": it.Summary})
		}
		r.Subtitle = line
		var verdict *Verdict
		if i < len(verdicts) {
			verdict = &verdicts[i]
		}
		tier := previewTier(fallback, kind, it.DueDate != nil)
		if verdict != nil && verdict.Tier != "" {
			tier = verdict.Tier
			if verdict.Summary != "" { // the model's summary is the row's second line, as in the Inbox
				r.Subtitle = "✦ " + verdict.Summary
			}
		}
		switch tier {
		case "action":
			r.Badge = badge(p.t("tier.action"), "danger")
		case "fyi":
			r.Badge = badge(p.t("tier.fyi"), "neutral")
		case "noise":
			r.Badge = badge(p.t("preview.noise"), "neutral")
		default:
			r.Badge = badge(p.t("preview.waits"), "neutral")
		}
		items.Items = append(items.Items, r)
		notes := []string{p.previewTime(it.Time), p.t(map[bool]string{true: "preview.new", false: "preview.seen"}[it.New])}
		switch {
		case verdict != nil && verdict.Tier != "" && verdict.Reason != "":
			notes = append(notes, p.t("preview.model_reason", i18n.Args{"reason": verdict.Reason}))
		case verdict != nil && verdict.Tier == "":
			notes = append(notes, p.t("preview.model_none", i18n.Args{"why": verdict.Why}))
		}
		for _, pr := range it.Problems {
			notes = append(notes, p.fieldProblem(pr))
		}
		tone := "caption"
		if len(it.Problems) > 0 {
			tone = "secondary"
		}
		items.Items = append(items.Items, text("dry.n."+itoa(i), join(notes), tone))
	}
	if dry.Shape != nil {
		shape := p.t("dry.shape_text")
		if dry.Shape.Type == "json" {
			shape = p.t("dry.shape_json", i18n.Args{"count": dry.Count})
		}
		if dry.MaybeMore {
			shape += " " + p.t("dry.maybe_more")
		}
		items.Items = append(items.Items, text("dry.shape", shape, "caption"))
	}
	if verdicts != nil {
		items.Footer = p.t("preview.model_footer")
	} else if p.try != nil && p.try.verdictErr != nil {
		items.Items = append(items.Items, text("dry.model_err", p.t("preview.model_failed"), "secondary"))
	}
	if r, ok := p.triagePress(dry); ok && verdicts == nil {
		items.Items = append(items.Items, r)
	}
	if len(items.Items) > 0 {
		out = append(out, items)
	}
	if len(dry.Warnings) > 0 {
		warn := wire.Section{Key: "warnings", Title: p.t("preview.warnings"), Footer: p.t("preview.warnings_footer")}
		for i, w := range dry.Warnings {
			r := row("w."+itoa(i), p.warning(w))
			r.Icon, r.IconColor = "warning", "orange"
			if w.Path != "" {
				r.Subtitle = p.t("preview.from", i18n.Args{"path": shortPath(w.Path)})
			}
			warn.Items = append(warn.Items, r)
		}
		out = append(out, warn)
	}
	if len(dry.Checks) > 0 {
		checks := wire.Section{Key: "checks", Title: p.t("checks.title")}
		for _, c := range dry.Checks {
			r := row("c."+c.Key, p.t("check."+c.Key))
			if c.OK {
				r.Icon, r.IconColor = "check", "green"
			} else {
				r.Icon, r.IconColor = "warning", "red"
				r.Subtitle = p.problemText(c.Error)
			}
			checks.Items = append(checks.Items, r)
		}
		out = append(out, checks)
	}
	return out
}

// fieldName is a recipe key ("fields.sender") as the field tables name it.
func (p *pages) fieldName(key string) string {
	name := strings.TrimPrefix(key, "fields.")
	if i18n.Has("subscriptions-pages.field." + name) {
		return p.t("field." + name)
	}
	return name
}

// fieldProblem is what one item could not read, in words.
func (p *pages) fieldProblem(pr subscriptions.FieldProblem) string {
	args := i18n.Args{"field": p.fieldName(pr.Key)}
	if pr.Code == subscriptions.FieldBadTime {
		return p.t("preview.bad_time", args)
	}
	return p.t("preview.empty", args)
}

// warning is a dry run's warning in words, by its code (the service's message is English).
func (p *pages) warning(w subscriptions.Warning) string {
	switch w.Code {
	case subscriptions.WarnUnknownKey:
		if name, ok := strings.CutPrefix(w.Key, "fields."); ok {
			return p.t("preview.unknown_field", i18n.Args{"key": name})
		}
		return p.t("preview.unknown_key", i18n.Args{"key": w.Key})
	case subscriptions.WarnEmptyField:
		return p.t("preview.empty_everywhere", i18n.Args{"field": p.fieldName(w.Key)})
	}
	return p.t("problem.other")
}

// fallbackOf is the page's subscription's fallback tier: "" (the default) for a source nobody subscribed to yet.
func (p *pages) fallbackOf() string {
	if p.st.ID == "" {
		return ""
	}
	s, _, err := p.svc.Get(p.ctx, p.st.ID)
	if err != nil {
		return ""
	}
	return s.FallbackTier
}

// tried is a preview pressed on a source's page: its dry run, or why there is none, and the triage model's verdicts
// when it was asked to try them (verdictErr: it could not be asked).
type tried struct {
	dry        *subscriptions.DryRun
	err        error
	verdicts   []Verdict
	verdictErr error
}

// Verdict is the triage model's answer for one previewed item (tier action, fyi or noise, its summary and reason), or
// why there is none (State; Why in the user's words).
type Verdict struct {
	Tier, Summary, Reason string
	Urgent                bool
	State, Why            string
}

// previewPress is the press on a source's page that tries a pull and shows what would come in.
func (p *pages) previewPress() wire.Component {
	r := row("x.preview", p.t("preview.press"))
	r.Subtitle, r.Icon, r.IconColor = p.t("preview.press_line"), "search", "blue"
	r.Action = &wire.Action{ID: "dry_run"}
	return r
}

// previewOf is the preview section of a source's page once its press was pressed: nothing before.
func (p *pages) previewOf(kind, fallback string) []wire.Section {
	switch {
	case p.try == nil:
		return nil
	case p.try.err != nil || p.try.dry == nil:
		return []wire.Section{{Key: "dry", Title: p.t("preview.title"), Items: []wire.Component{
			text("dry.err", p.t("dry.failed"), "secondary")}}}
	}
	return p.preview(p.try.dry, kind, fallback)
}

// triagePress asks the triage model to try a source page's preview (#5278): only where the pages can ask it, with items
// to try; it says first what goes to which model and that each item is one call.
func (p *pages) triagePress(dry *subscriptions.DryRun) (wire.Component, bool) {
	if p.env.Triage == nil || p.st.P != pageSource || len(dry.Items) == 0 {
		return wire.Component{}, false
	}
	r := row("x.triage", p.t("preview.model"))
	r.Icon, r.IconColor = p.icon("robot"), "purple"
	m := p.modelOf("inbox.triage")
	if !m.OK {
		r.Subtitle = p.t("preview.model_unconfigured")
		return r, true
	}
	n := min(len(dry.Items), 3)
	r.Subtitle = p.t("preview.model_line", i18n.Args{"count": n})
	r.Action = &wire.Action{ID: "triage_preview",
		Confirm: p.t("preview.model_confirm", i18n.Args{"count": n, "provider": modelName(m)})}
	return r, true
}

// triagePreview pulls once more and has the triage model try what came: the source page with its verdicts.
func (p *pages) triagePreview() (wire.ActionResult, error) {
	if p.env.Triage == nil {
		return wire.ActionResult{}, errBadValues
	}
	res, err := p.dryRun(nil)
	if err != nil || p.try == nil || p.try.dry == nil {
		return res, err
	}
	sub := subscriptions.Subscription{ID: p.st.ID}
	if p.st.ID != "" {
		if s, _, err := p.svc.Get(p.ctx, p.st.ID); err == nil {
			sub = s
		}
	} else if cat, err := p.svc.Catalog(p.ctx, p.st.C); err == nil {
		for _, src := range cat.Sources {
			if src.ID == p.st.S && len(src.Recipes) > 0 {
				sub = subscriptions.Subscription{Connector: p.st.C, Extension: cat.Extension, Source: src.ID, Label: src.Name,
					Recipe: src.Recipes[0]}
			}
		}
	}
	items := p.try.dry.Items
	if len(items) > 3 {
		items = items[:3]
	}
	p.try.verdicts, p.try.verdictErr = p.env.Triage(p.ctx, p.svc.PreviewThings(sub, items))
	if p.try.verdictErr != nil {
		p.try.verdicts = nil
	}
	v := p.draw()
	return wire.ActionResult{View: &v}, nil
}
