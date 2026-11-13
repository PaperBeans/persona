package kb

import (
	"slices"
	"testing"
)

func TestProjectNoteClearingAndAttribution(t *testing.T) {
	e := editEngine(t)
	res := extApply(t, e, "curator", KindProject, "Project note", Update{Note: " First\n second "})
	p, _ := e.Store.Project(res.ID)
	if p.Note != "First second" || p.Provenance["note"].By != "ext:curator" || !slices.Equal(p.LockedFields, []string{"note"}) {
		t.Fatalf("extension note: %+v", p)
	}
	editApply(t, e, KindProject, res.ID, Update{Note: " \n\t "})
	p, _ = e.Store.Project(res.ID)
	if p.Note != "" || len(p.Provenance) != 0 || !slices.Equal(p.LockedFields, []string{"note"}) {
		t.Fatalf("cleared note: %+v", p)
	}
	if _, err := e.ApplyExtension(KindProject, res.ID, Update{Note: "replacement"}, ExtensionWrite{Extension: "curator"}); err == nil {
		t.Fatal("an extension rewrote the user's cleared note")
	}
}

func TestProjectOnlyClaimsApplicableFields(t *testing.T) {
	e := editEngine(t)
	_ = e.Store.PutChat(&Chat{ID: "chat", Name: "Chat"})
	res := extApply(t, e, "curator", KindProject, "Project", Update{Note: "note", Chats: []string{"missing"}, Projects: []string{"missing"}, Status: "invalid", Kind: "invalid"})
	p, _ := e.Store.Project(res.ID)
	if !slices.Equal(p.LockedFields, []string{"note"}) || len(p.Provenance) != 1 || len(p.Chats) != 0 {
		t.Fatalf("unapplied fields: %+v", p)
	}
	extApply(t, e, "curator", KindProject, res.ID, Update{Chats: []string{"missing", "chat"}})
	p, _ = e.Store.Project(res.ID)
	if !slices.Equal(p.Chats, []string{"chat"}) || p.Provenance["chats"].By != "ext:curator" {
		t.Fatalf("mixed chat links: %+v", p)
	}
	editApply(t, e, KindProject, res.ID, Update{Chats: []string{"missing"}})
	p, _ = e.Store.Project(res.ID)
	if p.Provenance["chats"].By != "ext:curator" {
		t.Fatalf("an unapplied user update took ownership: %+v", p.Provenance)
	}
	extApply(t, e, "curator", KindProject, res.ID, Update{RemoveChats: []string{"chat"}})
	p, _ = e.Store.Project(res.ID)
	if len(p.Chats) != 0 || p.Provenance["chats"].By != "ext:curator" {
		t.Fatalf("removed chat: %+v", p)
	}
}
