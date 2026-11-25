package linktray

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The panel and the pairing window are each one HTML page with everything inline (tokens, components, script and
// the first data), loaded with NavigateToString: nothing to serve, nothing to fetch, drawn at once.

//go:embed web
var web embed.FS

// Theme is how the system looks right now, for the pages.
type Theme struct {
	Dark bool `json:"dark"`
	// Backdrop: the system draws Mica or acrylic behind the page, so the page stays transparent.
	Backdrop bool `json:"backdrop"`
	// The system's accent, already picked for this theme (a lighter shade in dark, a darker one in light); empty
	// keeps the tokens' default.
	Accent      string `json:"accent,omitempty"`
	AccentHover string `json:"accent_hover,omitempty"`
	AccentText  string `json:"accent_text,omitempty"`
	OnAccent    string `json:"on_accent,omitempty"`
}

// Boot is the data a page starts with.
type Boot struct {
	Labels map[string]string `json:"labels"`
	Glyphs map[string]string `json:"glyphs"`
	// Helper is the Persona helper as a data: URL, the mark in the panel's header (the tray icon's art).
	Helper  string       `json:"helper,omitempty"`
	Theme   Theme        `json:"theme"`
	Version string       `json:"version"`
	View    *View        `json:"view,omitempty"`    // the panel
	Pair    *PairState   `json:"pair,omitempty"`    // the pairing window
	Prefill string       `json:"prefill,omitempty"` // a link opened from outside, for the pairing window
	Preview *LinkPreview `json:"preview,omitempty"` // what Prefill is, so the window shows it at once
	Tab     string       `json:"tab,omitempty"`
}

// Page is the named page ("panel" or "pair") ready to load, in language l.
func Page(name string, l i18n.Lang, boot Boot) (string, error) {
	read := func(file string) (string, error) {
		b, err := web.ReadFile("web/" + file)
		return string(b), err
	}
	html, err := read(name + ".html")
	if err != nil {
		return "", fmt.Errorf("no page %q: %w", name, err)
	}
	parts := map[string]string{}
	for marker, file := range map[string]string{"/*TOKENS*/": "tokens.css", "/*COMPONENTS*/": "components.css",
		"/*COMMON*/": "common.js", "/*SCRIPT*/": name + ".js"} {
		if parts[marker], err = read(file); err != nil {
			return "", err
		}
	}
	if boot.Labels == nil {
		boot.Labels = Labels(l)
	}
	if boot.Glyphs == nil {
		boot.Glyphs = Glyphs
	}
	if boot.Helper == "" {
		if b, err := helperArt.ReadFile("art/helper-48.png"); err == nil {
			boot.Helper = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		}
	}
	data, err := json.Marshal(boot)
	if err != nil {
		return "", err
	}
	// Inside <script>, "</" would end the element: JSON lets it be written <\/.
	parts[`"__BOOT__"`] = strings.ReplaceAll(string(data), "</", `<\/`)
	parts["__LANG__"] = string(l.Or(i18n.Default))
	for marker, text := range parts {
		if !strings.Contains(html, marker) {
			return "", fmt.Errorf("page %s has no %s", name, marker)
		}
		html = strings.Replace(html, marker, text, 1)
	}
	return html, nil
}

// Script is the JavaScript that hands msg to the page (common.js's receive).
func Script(msg any) (string, error) {
	b, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return "window.__lt && window.__lt(" + string(b) + ")", nil
}
