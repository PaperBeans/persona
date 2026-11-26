package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A page the assistant keeps for the owner (Dashboards, #15017) is a live card held to a floor: it stays in the sidebar
// long after the conversation that drafted it, so it draws nothing the web and Android apps (UI version 10) cannot, runs
// no code, cannot pass for one of the app's own places, and leaves the page frame's actions to the hub.

const (
	// AgentPageUIVersion is the newest UI version whose components a page may use.
	AgentPageUIVersion = 10
	// AgentPageTitleMost is how many characters a page's title has at most.
	AgentPageTitleMost = 40
	// AgentPageActionPrefix starts the action ids the hub keeps for the page frame (pin, rename, remove, restore...).
	AgentPageActionPrefix = "persona."
	// AgentPageIconDefault is a page's icon when it names none.
	AgentPageIconDefault = "chart"
)

// agentPageComponentsRefused are components a page never has, whatever the app's version: html runs code, a scene is
// more than a page needs and an image loads whatever its URL serves.
var agentPageComponentsRefused = []string{ComponentHtml, ComponentScene, ComponentImage}

// agentPageIconsRefused are the icons of settings, security and the app's own places.
var agentPageIconsRefused = []string{"settings", "lock", "unlock", "shield", "home", "mail", "chat", "robot", "check"}

// AgentPageIcons are the icons a page may have: NavIcons without settings, security and the app's own places.
var AgentPageIcons = slices.DeleteFunc(slices.Clone(NavIcons), func(icon string) bool {
	return slices.Contains(agentPageIconsRefused, icon)
})

// ParseAgentPage reads a page's view: an assistant card (ParseAssistantView: 64 KB, 8 detail pages, 300 components a
// page, no secrets, controls or picture refs, assistant actions only), and then the page floor: no html, scene or image,
// no component newer than AgentPageUIVersion, no badge, no danger tone and no action id the hub keeps. refresh_after is
// dropped: a page is drawn again when it changes, not on a timer.
func ParseAgentPage(raw json.RawMessage) (*View, error) {
	v, err := ParseAssistantView(raw)
	if err != nil {
		return nil, err
	}
	if err := CheckAgentPage(v); err != nil {
		return nil, err
	}
	v.RefreshAfter = 0
	for name, page := range v.Pages {
		page.RefreshAfter = 0
		v.Pages[name] = page
	}
	return v, nil
}

// CheckAgentPage holds a view that already passed CheckAssistantView to the page floor (ParseAgentPage).
func CheckAgentPage(v *View) error {
	if v == nil {
		return errors.New("view: an object with sections and fallback is required")
	}
	check := func(page *View, where string) error {
		for i, s := range page.Sections {
			for j, e := range s.Items {
				at := fmt.Sprintf("%s.sections[%d].items[%d]", where, i, j)
				if slices.Contains(agentPageComponentsRefused, e.Type) {
					return fmt.Errorf("%s: a page has no %s components", at, e.Type)
				}
				if since, known := ComponentSince[e.Type]; !known {
					return fmt.Errorf("%s: unknown component type %q", at, e.Type)
				} else if since > AgentPageUIVersion {
					return fmt.Errorf("%s: %s needs UI version %d; a page uses what UI version %d draws", at, e.Type, since, AgentPageUIVersion)
				}
			}
		}
		for _, a := range Actions(page) {
			if len(a.ID) >= len(AgentPageActionPrefix) && strings.EqualFold(a.ID[:len(AgentPageActionPrefix)], AgentPageActionPrefix) {
				return fmt.Errorf("%s.action.%s: action ids starting with %q are the hub's", where, a.ID, AgentPageActionPrefix)
			}
		}
		return nil
	}
	if err := check(v, "view"); err != nil {
		return err
	}
	for _, name := range sortedPageNames(v.Pages) {
		page := v.Pages[name]
		if err := check(&page, "pages."+name); err != nil {
			return err
		}
	}
	// Badges and tones are looked for by name in the view's JSON, so one a later UI version adds anywhere is refused too.
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("view: %w", err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return fmt.Errorf("view: %w", err)
	}
	return checkAgentPageMarks(tree, "view")
}

// checkAgentPageMarks refuses a badge anywhere in node and a danger tone: on a page that never goes away they would read
// as the app's own counts and alarms. An action's values and the names of detail pages are the page's own words, not
// fields, and are not looked into by name.
func checkAgentPageMarks(node any, where string) error {
	switch n := node.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(n)) {
			value := n[key]
			at := where + "." + key
			switch key {
			case "badge", "badges":
				if !emptyJSON(value) {
					return fmt.Errorf("%s: a page has no badges", at)
				}
				continue
			case "tone":
				if value == ToneDanger {
					return fmt.Errorf("%s: a page has no danger tone; use caution", at)
				}
				continue
			case "values":
				if _, free := value.(map[string]any); free {
					continue
				}
			case "pages":
				if pages, ok := value.(map[string]any); ok && where == "view" {
					for _, name := range slices.Sorted(maps.Keys(pages)) {
						if err := checkAgentPageMarks(pages[name], "pages."+name); err != nil {
							return err
						}
					}
					continue
				}
			}
			if err := checkAgentPageMarks(value, at); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range n {
			if err := checkAgentPageMarks(item, fmt.Sprintf("%s[%d]", where, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func emptyJSON(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// CheckAgentPageIcon refuses an icon that is not one of AgentPageIcons.
func CheckAgentPageIcon(icon string) error {
	if !slices.Contains(AgentPageIcons, icon) {
		return fmt.Errorf("icon: %q is not a page icon; use one of %s", icon, strings.Join(AgentPageIcons, ", "))
	}
	return nil
}

// AgentPageTitleRules are what a page's title is held to besides its characters: the names of the app's own places,
// which a page may not pass for.
type AgentPageTitleRules struct {
	// Reserved are the built-in places' names in every language the app speaks.
	Reserved []string
	// Owner is the owner renaming a page: only a title that is a reserved name is refused. An Agent's title is refused
	// when it has a reserved name in it ("Inbox (2)", "收件箱 ·"): anywhere for a name in a script written without
	// spaces (Chinese, Japanese), as a whole word for a name in Latin letters ("Inbox · work" but not "Personal budget"
	// for Persona, nor "Todoist export" for ToDo).
	Owner bool
	// Fold maps a title and a reserved name to the form they are compared in. The hub passes NFKC with case folding;
	// this package has the standard library only, which has no normalization tables, so nil folds case alone.
	Fold func(string) string
}

// Check gives the title back without the white space around it, or why it is refused: 1 to AgentPageTitleMost
// characters on one line, without control, direction, private-use or invisible characters, and no reserved name.
func (r AgentPageTitleRules) Check(title string) (string, error) {
	title, err := CheckAgentPageTitle(title)
	if err != nil {
		return "", err
	}
	fold := r.Fold
	if fold == nil {
		fold = foldCase
	}
	key := fold(title)
	for _, name := range r.Reserved {
		reserved := fold(strings.TrimSpace(name))
		if reserved == "" {
			continue
		}
		if key == reserved || !r.Owner && hasReservedName(key, reserved) {
			return "", fmt.Errorf("title: %q is the name of one of the app's own places; choose another title", name)
		}
	}
	return title, nil
}

// hasReservedName says whether title has the reserved name in it, both folded alike: as a whole word when the name's
// letters are all Latin (a letter or digit on either side makes it part of another word), anywhere otherwise.
func hasReservedName(title, name string) bool {
	if !latinName(name) {
		return strings.Contains(title, name)
	}
	for from := 0; from <= len(title)-len(name); {
		i := strings.Index(title[from:], name)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(name)
		before, _ := utf8.DecodeLastRuneInString(title[:start])
		after, _ := utf8.DecodeRuneInString(title[end:])
		if start == 0 || !wordRune(before) {
			if end == len(title) || !wordRune(after) {
				return true
			}
		}
		_, size := utf8.DecodeRuneInString(title[start:])
		from = start + size
	}
	return false
}

// latinName: every letter of name is a Latin one (it has one at least).
func latinName(name string) bool {
	letters := false
	for _, r := range name {
		if unicode.IsLetter(r) {
			if !unicode.Is(unicode.Latin, r) {
				return false
			}
			letters = true
		}
	}
	return letters
}

// wordRune is a character that continues a word in Latin letters: a Latin letter, a digit or a mark. A Chinese or
// Japanese character next to it does not ("週のToDo"): those scripts put no space between words.
func wordRune(r rune) bool {
	return unicode.Is(unicode.Latin, r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// CheckAgentPageTitle holds a title to the characters a page's title may have (AgentPageTitleRules.Check without the
// reserved names) and gives it back without the white space around it.
func CheckAgentPageTitle(title string) (string, error) {
	if !utf8.ValidString(title) {
		return "", errors.New("title: not valid UTF-8")
	}
	title = strings.TrimFunc(title, unicode.IsSpace)
	if n := utf8.RuneCountInString(title); n < 1 || n > AgentPageTitleMost {
		return "", fmt.Errorf("title: 1 to %d characters on one line", AgentPageTitleMost)
	}
	for _, r := range title {
		if hiddenTitleRune(r) {
			return "", fmt.Errorf("title: no line breaks, control, direction or invisible characters (U+%04X)", r)
		}
	}
	return title, nil
}

// hiddenTitleRune is a character a title may not have: control characters and line breaks, format characters (the
// direction marks and overrides, zero-width spaces and joiners), private-use and surrogate code points, and the other
// characters that draw nothing (Hangul fillers, the combining grapheme joiner).
func hiddenTitleRune(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs, unicode.Other_Default_Ignorable_Code_Point)
}

// foldCase maps each character to the first of its case orbit, so titles that differ only in case compare equal.
func foldCase(s string) string {
	return strings.Map(func(r rune) rune {
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			lowest = min(lowest, f)
		}
		return lowest
	}, s)
}
