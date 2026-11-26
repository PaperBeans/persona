package wire

import "testing"

func viewWithOpen(target string) View {
	return View{Sections: []Section{{Items: []Component{
		{Type: "text", Text: "before"},
		{Type: "row", Title: "open it", Action: &Action{ID: "open", Open: target}},
	}}}}
}

func actionAt(v View) *Action { return v.Sections[0].Items[1].Action }

func TestStripAssistantFieldsForPageKeepsSessionAndTaskNavigation(t *testing.T) {
	for _, target := range []string{
		"persona://session/machine-1/session-1",
		"persona://task/todo-1",
	} {
		v := viewWithOpen(target)
		dropped := StripAssistantFieldsForPage(&v)
		if a := actionAt(v); a == nil || a.Open != target {
			t.Fatalf("%s: page open was dropped (%v)", target, dropped)
		}
		if err := CheckView(&v); err != nil {
			t.Fatalf("%s: kept view is invalid: %v", target, err)
		}
	}
}

func TestStripAssistantFieldsForPageDropsOtherAndMalformedTargets(t *testing.T) {
	for _, target := range []string{
		"https://example.com/one",
		"persona://kb/projects/notes.md",
		"persona://file/machine-1/etc/hosts",
		"persona://session/only-one-segment",
		"persona://task",
	} {
		v := viewWithOpen(target)
		dropped := StripAssistantFieldsForPage(&v)
		if a := actionAt(v); a != nil {
			t.Fatalf("%s: non-page target survived: %#v", target, a)
		}
		if len(dropped) == 0 || dropped[0] != "assistant_action" {
			t.Fatalf("%s: dropped %v", target, dropped)
		}
	}
}

func TestStripAssistantFieldsOnCardsStillDropsSessionNavigation(t *testing.T) {
	v := viewWithOpen("persona://session/machine-1/session-1")
	dropped := StripAssistantFields(&v)
	if a := actionAt(v); a != nil {
		t.Fatalf("a card kept a session open: %#v", a)
	}
	if len(dropped) == 0 || dropped[0] != "assistant_action" {
		t.Fatalf("dropped %v", dropped)
	}
}
