package subscriptionsui

import (
	"cmp"
	"net/url"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// Set Up with Persona (#5278, in place of drafting with a model): a source's or a subscription's page hands it to
// Persona's main conversation, which sets it up with the subscription tools (subscription_sources, _dry_run, _save,
// _run; every save is confirmed on the phone). The page only names what it is about; Persona reads the rest itself.

// AskURL is the open_url these pages answer for it: the app sends text (the query's "text") to Persona's main
// conversation as the user and opens it, as Ask Persona does. Only the pages drawn under PagesID may answer it.
const AskURL = "persona://ask"

// assistant is the assistant's name as the app shows it, at most 40 characters on one line; "" without one.
func (p *pages) assistant() string {
	return clipRunes(oneLine(p.env.Assistant), 40)
}

// askRow is the press on a page about a source (connector c, source s) or a subscription (id), named after the
// assistant as the app's "Ask {name}…" is ("the assistant" when the app gives no name). key is its subtitle's.
func (p *pages) askRow(key string) wire.Component {
	name := p.assistant()
	title := p.t("ask.title", i18n.Args{"name": name})
	if name == "" {
		name, title = p.t("ask.assistant"), p.t("ask.title_assistant")
	}
	r := row("x.ask", title)
	r.Subtitle, r.Icon, r.IconColor = p.t(key, i18n.Args{"name": name}), p.icon("person"), "purple"
	r.Action = &wire.Action{ID: "ask"}
	return r
}

// ask answers the press with the message for Persona.
func (p *pages) ask() (wire.ActionResult, error) {
	text, err := p.askText()
	if err != nil {
		return p.errorResult(err)
	}
	q := strings.ReplaceAll(url.QueryEscape(text), "+", "%20") // a space as %20: the apps read the query as a URL's
	return wire.ActionResult{OpenURL: AskURL + "?text=" + q}, nil
}

// askText is a sentence of what the user wants and a <persona-context> block naming the source: connector, source,
// subscription (left out before there is one) and its setup now in one line, the fields the subscription tools'
// descriptions read (#5240).
func (p *pages) askText() (string, error) {
	var lines []string
	var title, sentence string
	if p.st.ID != "" {
		s, _, err := p.svc.Get(p.ctx, p.st.ID)
		if err != nil {
			return "", err
		}
		_, src := p.cached(s.Connector, s.Source)
		lines = []string{"connector: " + s.Connector, "source: " + s.Source, "subscription: " + s.ID,
			"now: " + p.askNow(s, src)}
		title = p.t("ask.context_subscription", i18n.Args{"label": s.Label})
		sentence = p.t("ask.subscription", i18n.Args{"label": s.Label})
	} else {
		cat, err := p.svc.Catalog(p.ctx, p.st.C)
		if err != nil {
			return "", err
		}
		var src *subscriptions.CatalogSource
		for i := range cat.Sources {
			if cat.Sources[i].ID == p.st.S {
				src = &cat.Sources[i]
			}
		}
		if src == nil {
			return "", &subscriptions.Error{Status: 404, Code: subscriptions.CodeNotFound}
		}
		now := p.t("ask.now_new")
		if len(src.Recipes) > 0 {
			now = p.t("ask.now_new_recipe", i18n.Args{"tool": toolTitle(src, src.Recipes[0].Tool)})
		}
		lines = []string{"connector: " + p.st.C, "source: " + p.st.S, "now: " + now}
		title = p.t("ask.context_source", i18n.Args{"source": src.Name})
		sentence = p.t("ask.source", i18n.Args{"source": src.Name})
	}
	return contextBlock(title, strings.Join(lines, "\n")) + "\n\n" + sentence, nil
}

// askNow is a subscription's setup in one line: its tool, where it goes, how often, its triage rules, how it is doing.
func (p *pages) askNow(s subscriptions.Subscription, src *subscriptions.CatalogSource) string {
	parts := []string{p.t("ask.now_tool", i18n.Args{"tool": toolTitle(src, s.Recipe.Tool)}), p.destinations(s), p.schedule(s)}
	if s.TriageRules != "" {
		parts = append(parts, p.t("ask.now_rules", i18n.Args{"rules": clipRunes(oneLine(s.TriageRules), 120)}))
	}
	if s.State != subscriptions.StateOK && s.State != subscriptions.StateOff {
		parts = append(parts, cmp.Or(p.stateBadge(s.State), &wire.Badge{Text: s.State}).Text)
	}
	return join(parts)
}

// contextBlock is a <persona-context> block as the apps write one (LinkSheet's ContextRef): attributes escaped, and
// a text that can't close it.
func contextBlock(title, body string) string {
	esc := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
	body = strings.ReplaceAll(body, "</persona-context", "&lt;/persona-context")
	return `<persona-context kind="context" title="` + esc.Replace(title) + `" url="">` + "\n" + body + "\n</persona-context>"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
