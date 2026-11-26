package wire

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const agentPageOK = `{
  "title": "Running",
  "fallback": "Ran 3 of 5 days this week.",
  "refresh_after": 60,
  "sections": [
    {"title": "This week", "items": [
      {"type": "metric", "key": "days", "label": "Days run", "value": "3 / 5", "tone": "caution"},
      {"type": "progress", "label": "Goal", "value": "60%", "ratio": 0.6},
      {"type": "stat_grid", "stats": [{"label": "Distance", "value": "14 km", "tone": "positive"}]},
      {"type": "row", "key": "history", "title": "History", "action": {"id": "open-history", "push": {"page": "badge"}}},
      {"type": "button", "title": "Done today", "action": {"id": "done", "reply": "I ran today", "values": {"badge": "x", "tone": "danger"}}},
      {"type": "graph", "nodes": [{"id": "a", "title": "Plan", "tone": "positive"}, {"id": "b", "title": "Run"}], "edges": [{"from": "a", "to": "b"}]}
    ]}
  ],
  "pages": {
    "badge": {"title": "History", "refresh_after": 30, "sections": [{"items": [
      {"type": "table", "columns": [{"title": "Day"}, {"title": "km"}], "rows": [["Mon", "5"]]},
      {"type": "button", "title": "Back", "action": {"id": "back", "pop": true}}
    ]}]}
  }
}`

// agentPage is agentPageOK with change applied to its decoded JSON.
func agentPage(t *testing.T, change func(v map[string]any)) json.RawMessage {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(agentPageOK), &v); err != nil {
		t.Fatal(err)
	}
	change(v)
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func items(v map[string]any) []any {
	return v["sections"].([]any)[0].(map[string]any)["items"].([]any)
}

func setItems(v map[string]any, list []any) {
	v["sections"].([]any)[0].(map[string]any)["items"] = list
}

func item(v map[string]any, i int) map[string]any { return items(v)[i].(map[string]any) }

func TestParseAgentPageKeepsWhatAPageMayDraw(t *testing.T) {
	v, err := ParseAgentPage(json.RawMessage(agentPageOK))
	if err != nil {
		t.Fatal(err)
	}
	if v.RefreshAfter != 0 || v.Pages["badge"].RefreshAfter != 0 {
		t.Fatalf("refresh_after kept: %d, %d", v.RefreshAfter, v.Pages["badge"].RefreshAfter)
	}
	if v.Title != "Running" || len(v.Sections[0].Items) != 6 || v.Pages["badge"].Title != "History" {
		t.Fatalf("view changed: %+v", v)
	}
	if v.Sections[0].Items[4].Action.Values["tone"] != "danger" {
		t.Fatal("an action's values are its own words, not a tone")
	}
	again, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(again), "refresh_after") {
		t.Fatalf("refresh_after written back: %s", again)
	}
	if _, err := ParseAgentPage(again); err != nil {
		t.Fatalf("a parsed page parses again: %v", err)
	}
}

func TestParseAgentPageRefuses(t *testing.T) {
	big := make([]any, 0, ItemsMost+1)
	for i := 0; i <= ItemsMost; i++ {
		big = append(big, map[string]any{"type": "text", "text": fmt.Sprint(i)})
	}
	cases := []struct {
		name   string
		change func(v map[string]any)
		want   string
	}{
		{"html", func(v map[string]any) {
			setItems(v, append(items(v), map[string]any{"type": "html", "html": "<p>hi</p>", "height": 200}))
		}, "no html components"},
		{"image", func(v map[string]any) {
			setItems(v, append(items(v), map[string]any{"type": "image", "alt": "A run", "src": map[string]any{"url": "https://example.com/a.png"}}))
		}, "no image components"},
		{"badge on a row", func(v map[string]any) {
			item(v, 3)["badge"] = map[string]any{"text": "New"}
		}, "view.sections[0].items[3].badge: a page has no badges"},
		{"badge on a detail page", func(v map[string]any) {
			page := v["pages"].(map[string]any)["badge"].(map[string]any)
			page["sections"] = []any{map[string]any{"items": []any{map[string]any{"type": "row", "title": "Mon", "badge": map[string]any{"text": "1"}}}}}
		}, "pages.badge.sections[0].items[0].badge: a page has no badges"},
		{"tab badge", func(v map[string]any) {
			v["tabs"] = map[string]any{"name": "range", "value": "week", "items": []any{map[string]any{"value": "week", "badge": "2"}, map[string]any{"value": "month"}}, "action": map[string]any{"id": "range", "reply": "Show this range"}}
		}, "view.tabs.items[0].badge: a page has no badges"},
		{"danger metric", func(v map[string]any) {
			item(v, 0)["tone"] = "danger"
		}, "view.sections[0].items[0].tone: a page has no danger tone"},
		{"danger stat", func(v map[string]any) {
			item(v, 2)["stats"].([]any)[0].(map[string]any)["tone"] = "danger"
		}, "view.sections[0].items[2].stats[0].tone: a page has no danger tone"},
		{"danger graph node", func(v map[string]any) {
			item(v, 5)["nodes"].([]any)[0].(map[string]any)["tone"] = "danger"
		}, "nodes[0].tone: a page has no danger tone"},
		{"reserved action", func(v map[string]any) {
			item(v, 4)["action"].(map[string]any)["id"] = "persona.pin"
		}, `action ids starting with "persona."`},
		{"reserved action in any case", func(v map[string]any) {
			item(v, 4)["action"].(map[string]any)["id"] = "Persona.remove"
		}, `action ids starting with "persona."`},
		{"reserved action on a detail page", func(v map[string]any) {
			page := v["pages"].(map[string]any)["badge"].(map[string]any)
			page["sections"].([]any)[0].(map[string]any)["items"].([]any)[1].(map[string]any)["action"].(map[string]any)["id"] = "persona.restore:pg_0123456789abcdef"
		}, "pages.badge.action.persona.restore"},
		{"more than 300 components", func(v map[string]any) { setItems(v, big) }, "more than 300 items"},
		{"more than 8 detail pages", func(v map[string]any) {
			pages := v["pages"].(map[string]any)
			for i := range AssistantPagesMost {
				pages[fmt.Sprintf("p%d", i)] = map[string]any{"sections": []any{map[string]any{"items": []any{map[string]any{"type": "text", "text": "x"}}}}}
			}
		}, "at most 8 named pages"},
		{"more than 64 KB", func(v map[string]any) {
			v["fallback"] = strings.Repeat("x", AssistantViewMost)
		}, "at most 65536 bytes"},
		{"a secret field", func(v map[string]any) {
			setItems(v, append(items(v), map[string]any{"type": "field", "name": "token", "kind": "secret", "label": "Token"}))
		}, "must not collect secrets"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseAgentPage(agentPage(t, c.change))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error with %q", err, c.want)
			}
		})
	}
}

// A scene (UI version 11) and a map (UI version 12) are more than UI version 10 draws; CheckAgentPage refuses them
// whatever else is in them.
func TestCheckAgentPageRefusesNewerComponents(t *testing.T) {
	for _, typ := range []string{ComponentScene, ComponentMap} {
		v := &View{Fallback: "x", Sections: []Section{{Items: []Component{{Type: ComponentText, Text: "x"}, {Type: typ}}}}}
		if err := CheckAgentPage(v); err == nil || !strings.Contains(err.Error(), "view.sections[0].items[1]") {
			t.Fatalf("%s: %v", typ, err)
		}
		v = &View{Fallback: "x", Sections: []Section{{Items: []Component{{Type: ComponentText, Text: "x"}}}},
			Pages: map[string]View{"more": {Sections: []Section{{Items: []Component{{Type: typ}}}}}}}
		if err := CheckAgentPage(v); err == nil || !strings.Contains(err.Error(), "pages.more.sections[0].items[0]") {
			t.Fatalf("%s on a detail page: %v", typ, err)
		}
	}
	for typ, since := range ComponentSince {
		v := &View{Fallback: "x", Sections: []Section{{Items: []Component{{Type: typ}}}}}
		refused := since > AgentPageUIVersion || slices.Contains([]string{ComponentHtml, ComponentImage}, typ)
		if err := CheckAgentPage(v); (err != nil) != refused {
			t.Errorf("%s (UI version %d): %v", typ, since, err)
		}
	}
	if err := CheckAgentPage(nil); err == nil {
		t.Fatal("nil view passed")
	}
}

func TestAgentPageIcons(t *testing.T) {
	if len(AgentPageIcons) != len(NavIcons)-len(agentPageIconsRefused) {
		t.Fatalf("%d page icons for %d nav icons", len(AgentPageIcons), len(NavIcons))
	}
	for _, icon := range agentPageIconsRefused {
		if !slices.Contains(NavIcons, icon) {
			t.Errorf("%s is not a nav icon", icon)
		}
		if err := CheckAgentPageIcon(icon); err == nil {
			t.Errorf("%s passed", icon)
		}
	}
	for _, icon := range AgentPageIcons {
		if err := CheckAgentPageIcon(icon); err != nil {
			t.Error(err)
		}
	}
	for _, icon := range []string{"", "Chart", "rocket"} {
		if err := CheckAgentPageIcon(icon); err == nil {
			t.Errorf("%q passed", icon)
		}
	}
	if err := CheckAgentPageIcon(AgentPageIconDefault); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAgentPageTitle(t *testing.T) {
	ok := map[string]string{
		"  Running log ":               "Running log",
		strings.Repeat("跑", 40):        strings.Repeat("跑", 40),
		"\u2764\ufe0f Reading list":    "\u2764\ufe0f Reading list", // a variation selector is part of the emoji
		"Café — März":                  "Café — März",
		"x":                            "x",
		strings.Repeat("ab", 20):       strings.Repeat("ab", 20),
		"\u3000Full-width space\u3000": "Full-width space",
	}
	for in, want := range ok {
		got, err := CheckAgentPageTitle(in)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	refused := []string{"", "   ", strings.Repeat("x", 41), strings.Repeat("跑", 41),
		"Two\nlines", "Two\u2028lines", "Two\u2029lines", "Tab\there", "Next\u0085line", "Bell\u0007",
		"Inbox\u202e", "\u2066Inbox\u2069", "\u200fInbox", "In\u200bbox", "In\u200cbox", "In\u200dbox", "In\ufeffbox",
		"In\u00adbox", "In\u2060box", "In\u3164box", "In\u034fbox", "In\ue000box", "In\U000e0041box",
		string([]byte{'a', 0xff})}
	for _, in := range refused {
		if got, err := CheckAgentPageTitle(in); err == nil {
			t.Errorf("%q passed as %q", in, got)
		}
	}
}

// fullWidth stands in for NFKC in this package's tests (the hub passes the real one): it maps full-width ASCII to
// ASCII, then folds case.
func fullWidth(s string) string {
	return foldCase(strings.Map(func(r rune) rune {
		if r >= 0xFF01 && r <= 0xFF5E {
			return r - 0xFEE0
		}
		return r
	}, s))
}

func TestAgentPageTitleRules(t *testing.T) {
	reserved := []string{"Inbox", "收件箱", "To-dos", " ", ""}
	agent := AgentPageTitleRules{Reserved: reserved, Fold: fullWidth}
	owner := AgentPageTitleRules{Reserved: reserved, Owner: true, Fold: fullWidth}
	for _, title := range []string{"Inbox", "inbox", "Inbox (2)", "My INBOX", "收件箱 ·", "看收件箱", "\uff29\uff4e\uff42\uff4f\uff58", "to-dos today"} {
		if got, err := agent.Check(title); err == nil {
			t.Errorf("agent %q passed as %q", title, got)
		}
	}
	for _, title := range []string{"Inbox", " INBOX ", "\uff29\uff4e\uff42\uff4f\uff58", "收件箱", "TO-DOS"} {
		if got, err := owner.Check(title); err == nil {
			t.Errorf("owner %q passed as %q", title, got)
		}
	}
	for _, title := range []string{"Inbox (2)", "My inbox", "收件箱 ·"} {
		if got, err := owner.Check(title); err != nil || got != title {
			t.Errorf("owner %q: %q, %v", title, got, err)
		}
	}
	for _, title := range []string{"Running", "Reading list", "Todo-free days"} {
		if got, err := agent.Check(title); err != nil || got != title {
			t.Errorf("agent %q: %q, %v", title, got, err)
		}
	}
	// A name in Latin letters is refused as a whole word only; a name in Chinese or Japanese anywhere.
	words := AgentPageTitleRules{Reserved: []string{"Persona", "ToDo", "Inbox", "Needs you", "收件箱", "ダッシュボード"}, Fold: fullWidth}
	for title, refused := range map[string]bool{
		"Inbox (2)": true, "Inbox · work": true, "Work inbox": true, "inbox": true, "My Persona notes": true, "Persona's week": true,
		"ToDo list": true, "Needs you today": true, "看收件箱": true, "我的ダッシュボード": true, "Personas Persona": true, "週のToDo": true,
		"Personal budget": false, "Todoist export": false, "Inboxes": false, "Unpersonable": false, "Personaé": false,
		"Needs your input": false, "To do list": false,
	} {
		if _, err := words.Check(title); (err != nil) != refused {
			t.Errorf("agent %q: refused %v, want %v", title, err != nil, refused)
		}
	}
	if _, err := agent.Check("In\u200bbox"); err == nil || !strings.Contains(err.Error(), "U+200B") {
		t.Fatalf("characters are checked first: %v", err)
	}
	// Without Fold a title is compared by case alone.
	plain := AgentPageTitleRules{Reserved: reserved}
	if _, err := plain.Check("INBOX zero"); err == nil {
		t.Fatal("case was not folded")
	}
	if _, err := plain.Check("ΣΑΣ"); err != nil {
		t.Fatal(err)
	}
	if foldCase("ΣΑΣ") != foldCase("σας") || foldCase("K") != foldCase("K") {
		t.Fatal("foldCase does not fold whole case orbits")
	}
}
