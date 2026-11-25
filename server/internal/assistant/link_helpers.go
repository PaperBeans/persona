package assistant

import (
	"context"
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/deeplink"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Links to the web app and the buttons that open them, shared by cards, the briefing and the asks (moved out of feishu_cards.go, #13404).

// openButton opens the conversation in the web app the hub serves; nil (Card.Buttons leaves it out) when the hub has
// no http(s) address for it. Card links are documented for http(s) and AppLink
// only: a persona:// link (the iPhone app) might get the whole card refused, so it is not used.
func (s *Service) openButton(ctx context.Context, machine, session string) botcard.Button {
	_, web := s.openLink(ctx, machine, session)
	return s.openLinkButton(ctx, web)
}

// openLink is the conversation's page in the web app the hub serves (the app's home without a conversation) and what
// a link to it says; "" when the hub has no http(s) address for it.
func (s *Service) openLink(ctx context.Context, machine, session string) (label, web string) {
	st, _ := s.larkModule().StatusKept(ctx)
	web = strings.TrimRight(st.PublicURL, "/")
	if session != "" {
		web = sessionLink(web, machine, session, s.isAssistantConversation(ctx, machine, session))
	}
	u, err := url.Parse(web)
	if web == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", ""
	}
	return s.linkNamer(ctx)(u), web
}

// openLinkButton is the button that opens a page of the web app already built (sessionLink, todoLink), named by what
// it opens and opened in Feishu's sidebar on the desktop; nil for an empty or non-http(s) one, so a card never
// carries a dead empty link (#4073).
func (s *Service) openLinkButton(ctx context.Context, web string) botcard.Button {
	u, err := url.Parse(web)
	if web == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	return botcard.PageBtn(s.linkNamer(ctx)(u), web)
}

// safeLinkButton is a labeled open_url button for web, nil when web is empty or not http(s) with a host: card links
// are documented for http(s) only, so a dirty address (no scheme, empty host) never becomes a dead button.
func safeLinkButton(label, web string) botcard.Button {
	if u, err := url.Parse(web); web == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	return botcard.LinkBtn(label, web)
}

// linkNamer names a link's button by what it opens (botcard.RenderAnswerWith): a page of the hub's web app by the
// page, a Feishu document as one, any other site by its name.
func (s *Service) linkNamer(ctx context.Context) func(*url.URL) string {
	st, _ := s.larkModule().StatusKept(ctx)
	return linkNamer(s.botLang(ctx), st.PublicURL)
}

// feishuDocLink: a Feishu (Lark) document, sheet, base or wiki page.
func feishuDocLink(host, path string) bool {
	if !strings.HasSuffix(host, ".feishu.cn") && !strings.HasSuffix(host, ".larksuite.com") && !strings.HasSuffix(host, ".larkoffice.com") {
		return false
	}
	for _, p := range []string{"/docx/", "/docs/", "/wiki/", "/sheets/", "/base/", "/mindnotes/", "/slides/"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// webBase is the hub's web app (hub.json's public_url) without the trailing slash, "" when the hub does not say.
// It only reads feishu.status (which carries the address whether the bot is on or not): unlike feishuStatus it
// does not reconcile the bot's chat state, so the snapshot does not touch it.
func (s *Service) webBase(ctx context.Context) string {
	var st FeishuStatus
	if err := s.appCall(ctx, AppFeishu, FeishuStatusGet, map[string]any{}, &st); err != nil {
		return ""
	}
	return strings.TrimRight(st.PublicURL, "/")
}

// sessionLink is a conversation's page in the web app at web; "" when web is.
func sessionLink(web, machine, session string, assistant bool) string {
	return deeplink.Web(web, sessionPath(machine, session, assistant))
}

// sessionPath is a conversation's path in the route table (docs/protocol/deep-links.md): a Persona conversation is
// /persona/<id>, an agent /s/<machine>/<id>.
func sessionPath(machine, session string, assistant bool) string {
	if assistant {
		return deeplink.Conversation(session)
	}
	return deeplink.Session(machine, session)
}

// todoLink is a todo's page in the web app at web (/todos/<id>), "" when web is.
func todoLink(web, id string) string {
	return deeplink.Web(web, deeplink.Todo(id))
}

func linkNamer(l i18n.Lang, web string) func(*url.URL) string {
	host := ""
	if w, err := url.Parse(strings.TrimSpace(web)); err == nil {
		host = strings.ToLower(w.Host)
	}
	return func(u *url.URL) string {
		if host != "" && strings.ToLower(u.Host) == host {
			p := u.Path
			switch {
			case strings.HasPrefix(p, "/persona/") || strings.HasPrefix(p, "/s/"):
				return i18n.T(l, "assistant-feishu_cards.link_conversation")
			case strings.HasPrefix(p, "/todos/"):
				return i18n.T(l, "assistant-feishu_cards.link_todo")
			case p == "/inbox" || strings.HasPrefix(p, "/inbox/"):
				return i18n.T(l, "assistant-feishu_cards.link_inbox")
			case p == "/kb" || strings.HasPrefix(p, "/kb/"):
				return i18n.T(l, "assistant-feishu_cards.link_knowledge")
			}
			return i18n.T(l, "assistant-feishu_cards.open_web")
		}
		site := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		if feishuDocLink(site, u.Path) {
			return i18n.T(l, "assistant-feishu_cards.link_feishu_doc")
		}
		return i18n.T(l, "assistant-feishu_cards.link_site", i18n.Args{"site": site})
	}
}
