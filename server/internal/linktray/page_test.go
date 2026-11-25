package linktray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

func TestPagesAssemble(t *testing.T) {
	view := Build(i18n.En, running(link.Status{State: "connected", Node: "online", HubName: "</script><b>"}))
	pair := BuildPair(i18n.En, running(link.Status{State: "connected"}))
	for name, boot := range map[string]Boot{"panel": {View: &view}, "pair": {Pair: &pair}} {
		html, err := Page(name, i18n.Ja, boot)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{"/*TOKENS*/", "/*COMPONENTS*/", "/*COMMON*/", "/*SCRIPT*/", "__BOOT__", "__LANG__"} {
			if strings.Contains(html, marker) {
				t.Errorf("%s keeps %s", name, marker)
			}
		}
		if !strings.Contains(html, `<html lang="ja">`) {
			t.Errorf("%s: no language", name)
		}
		// The boot data cannot end its own script element, whatever a hub calls itself.
		m := regexp.MustCompile(`(?s)<script type="application/json" id="boot">(.*?)</script>`).FindStringSubmatch(html)
		if m == nil {
			t.Fatalf("%s: no boot element", name)
		}
		var b Boot
		if err := json.Unmarshal([]byte(m[1]), &b); err != nil {
			t.Fatalf("%s boot: %v", name, err)
		}
		if b.Labels["pair_button"] != "ペアリング" || b.Glyphs["hotspot"] == "" {
			t.Errorf("%s boot %+v", name, b.Labels)
		}
	}
	if _, err := Page("nope", i18n.En, Boot{}); err == nil {
		t.Fatal("unknown page")
	}
}

// Every icon a page or the model names is a glyph, so nothing falls back to the info icon by mistake.
func TestPagesUseKnownGlyphs(t *testing.T) {
	icon := regexp.MustCompile(`icon\(\s*(?:[^"()]*\?\s*)?"([a-z_]+)"(?:\s*:\s*"([a-z_]+)")?`)
	for _, f := range []string{"panel.js", "pair.js", "common.js"} {
		b, err := web.ReadFile("web/" + f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range icon.FindAllStringSubmatch(string(b), -1) {
			for _, name := range m[1:] {
				if name != "" && Glyphs[name] == "" {
					t.Errorf("%s uses icon %q", f, name)
				}
			}
		}
	}
	src, _ := os.ReadFile(filepath.Join(".", "model.go"))
	src2, _ := os.ReadFile(filepath.Join(".", "pair.go"))
	for _, m := range regexp.MustCompile(`Icon: "([a-z_]+)"`).FindAllStringSubmatch(string(src)+string(src2), -1) {
		if Glyphs[m[1]] == "" {
			t.Errorf("model uses icon %q", m[1])
		}
	}
}

// The pages draw only from tokens.css: no colors, and no sizes but hairlines, outside it.
func TestPagesUseTokens(t *testing.T) {
	hex := regexp.MustCompile(`#[0-9A-Fa-f]{3,8}\b`)
	size := regexp.MustCompile(`\b([5-9]|\d{2,})px\b`)
	for _, f := range []string{"components.css", "panel.html", "pair.html", "panel.js", "pair.js", "common.js"} {
		b, err := web.ReadFile("web/" + f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if m := hex.FindAllString(s, -1); len(m) > 0 {
			t.Errorf("%s has colors %v: add a token", f, m)
		}
		if m := size.FindAllString(s, -1); len(m) > 0 {
			t.Errorf("%s has sizes %v: add a token", f, m)
		}
	}
}

func TestScript(t *testing.T) {
	s, err := Script(map[string]string{"type": "view", "x": "</script>"})
	if err != nil || !strings.HasPrefix(s, "window.__lt && window.__lt({") {
		t.Fatalf("%q %v", s, err)
	}
}

// TestWritePages writes every sample's panel and the pairing window as plain HTML files, to look at in a browser
// (LINKTRAY_PAGES=<dir>), where a script can screenshot them or check their layout.
func TestWritePages(t *testing.T) {
	dir := os.Getenv("LINKTRAY_PAGES")
	if dir == "" {
		t.Skip("LINKTRAY_PAGES not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, page string, l i18n.Lang, boot Boot) {
		t.Helper()
		html, err := Page(page, l, boot)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range i18n.Langs {
		for _, dark := range []bool{false, true} {
			theme := Theme{Dark: dark}
			suffix := map[bool]string{false: "light", true: "dark"}[dark] + "-" + string(l)
			for _, s := range Samples(now) {
				v := Build(l, s.Snapshot)
				write("panel-"+s.Name+"-"+suffix, "panel", l, Boot{Theme: theme, Version: "1.4.2", View: &v})
			}
			ps := BuildPair(l, Samples(now)[0].Snapshot)
			prefill := "persona://link?url=https%3A%2F%2Fhub.example.com%3A18661&token=abc&fp=x&tls=18661&machine=studio-pc"
			preview := ParseLink(l, prefill)
			write("pair-"+suffix, "pair", l, Boot{Theme: theme, Version: "1.4.2", Pair: &ps, Prefill: prefill, Preview: &preview})
			write("pair-passive-"+suffix, "pair", l, Boot{Theme: theme, Version: "1.4.2", Pair: &ps, Tab: "passive"})
			// The pairing window with a tunnel saved and in use: its section open, its row in the addresses.
			tunnel := Samples(now)[0].Snapshot
			cfg, st := *tunnel.Config, *tunnel.Status
			cfg.SSH = &link.SSH{Server: "user@example.com", Target: "127.0.0.1:18661"}
			st.Address, st.Kind, st.SSH = cfg.SSH.Server, link.KindSSH, cfg.SSH
			tunnel.Config, tunnel.Status = &cfg, &st
			sshState := BuildPair(l, tunnel)
			write("pair-ssh-"+suffix, "pair", l, Boot{Theme: theme, Version: "1.4.2", Pair: &sshState})
		}
	}
}
