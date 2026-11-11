package kb

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func trashFixture(t *testing.T) *Engine {
	t.Helper()
	e := testEngine(t, nil)
	for _, slug := range []string{"gone", "other"} {
		if _, err := e.Apply(KindProject, slug, Update{Name: slug}); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.Store.PutChat(&Chat{ID: "chat"})
	_ = e.Store.PutPerson(&Person{ID: "person"})
	_ = e.Store.PutItem(&Item{Kind: KindDoc, ID: "doc"})
	for _, ref := range [][2]string{{KindChat, "chat"}, {KindPerson, "person"}, {KindDoc, "doc"}} {
		if _, err := e.Apply(ref[0], ref[1], Update{Projects: []string{"gone", "other"}}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func trashLinks(t *testing.T, e *Engine) map[string][]string {
	t.Helper()
	c, _ := e.Store.Chat("chat")
	p, _ := e.Store.Person("person")
	d, _ := e.Store.Item(KindDoc, "doc")
	return map[string][]string{"chat": c.Projects, "person": p.Projects, "doc": d.Projects}
}

func TestDeleteProjectMovesItToTrashAndUnlinksIt(t *testing.T) {
	e := trashFixture(t)
	var unindexed []string
	e.Unindex = func(kind string, ids []string) { unindexed = append(unindexed, kind+"/"+ids[0]) }
	at := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	entry, err := e.DeleteProject("gone", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Store.Project("gone"); ok {
		t.Fatal("the deleted project is still readable")
	}
	if slices.Contains(e.Store.IDs(KindProject), "gone") {
		t.Fatal("the deleted project is still listed")
	}
	for name, list := range trashLinks(t, e) {
		if !slices.Equal(list, []string{"other"}) {
			t.Errorf("%s still names the deleted project: %v", name, list)
		}
	}
	if !slices.Equal(unindexed, []string{"projects/gone"}) {
		t.Errorf("unindexed = %v", unindexed)
	}
	trash := e.Trash()
	if len(trash) != 1 || trash[0].ID != entry.ID || trash[0].Slug != "gone" || !trash[0].DeletedAt.Equal(at) {
		t.Fatalf("trash = %+v", trash)
	}
	if want := []string{"chats/chat", "people/person", "docs/doc"}; !sameSet(trash[0].Unlinked, want) {
		t.Errorf("unlinked = %v, want %v", trash[0].Unlinked, want)
	}
}

func TestRestoreProjectPutsItAndItsLinksBack(t *testing.T) {
	e := trashFixture(t)
	entry, err := e.DeleteProject("gone", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var indexed []string
	e.Index = func(kind, id, name string, _ Tagging, one string, updated time.Time) {
		indexed = append(indexed, kind+"/"+id)
	}
	if _, err := e.RestoreProject(entry.ID); err != nil {
		t.Fatal(err)
	}
	if p, ok := e.Store.Project("gone"); !ok || p.Name != "gone" {
		t.Fatalf("restored project = %+v, %v", p, ok)
	}
	for name, list := range trashLinks(t, e) {
		if !sameSet(list, []string{"gone", "other"}) {
			t.Errorf("%s = %v after restore", name, list)
		}
	}
	if len(e.Trash()) != 0 {
		t.Errorf("trash after restore = %+v", e.Trash())
	}
	if !slices.Contains(indexed, "projects/gone") {
		t.Errorf("indexed = %v", indexed)
	}
	if _, err := e.RestoreProject(entry.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second restore err = %v", err)
	}
}

func TestRestoreProjectRefusesATakenSlug(t *testing.T) {
	e := trashFixture(t)
	entry, err := e.DeleteProject("gone", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// A project of the same name, created after the delete, takes the slug.
	if id, err := e.Apply(KindProject, "gone", Update{Name: "gone", Goal: "newer"}); err != nil || id != "gone" {
		t.Fatalf("recreate = %q, %v", id, err)
	}
	if _, err := e.RestoreProject(entry.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if p, _ := e.Store.Project("gone"); p.Goal != "newer" {
		t.Errorf("the newer project was replaced: %+v", p)
	}
	if len(e.Trash()) != 1 {
		t.Error("the trash entry is gone after a refused restore")
	}
}

func TestDeleteProjectRefusesMergedAndUnknownProjects(t *testing.T) {
	e := trashFixture(t)
	if _, err := e.Apply(KindProject, "gone", Update{MergeInto: "other"}); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"gone", "missing", "../missing"} {
		if _, err := e.DeleteProject(slug, time.Now()); !errors.Is(err, ErrNotFound) {
			t.Errorf("DeleteProject(%q) err = %v, want ErrNotFound", slug, err)
		}
	}
	if _, ok := e.Store.Project("other"); !ok {
		t.Error("a refused delete removed another project")
	}
	if _, err := e.RestoreProject("../projects"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RestoreProject outside the trash err = %v", err)
	}
}

func TestProjectTrashIsReadOnlyWhenTheKnowledgeBaseIs(t *testing.T) {
	e := trashFixture(t)
	entry, err := e.DeleteProject("gone", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r := &Engine{Store: ro, Logf: func(string, ...any) {}}
	if _, err := r.DeleteProject("other", time.Now()); !errors.Is(err, ErrReadOnly) {
		t.Errorf("delete err = %v", err)
	}
	if _, err := r.RestoreProject(entry.ID); !errors.Is(err, ErrReadOnly) {
		t.Errorf("restore err = %v", err)
	}
	if len(r.Trash()) != 1 {
		t.Error("a read-only knowledge base cannot list the trash")
	}
}

func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// Review fixes (#4412): search, forgetting a source, merged projects, long slugs and a stray timeline.

func TestDeletedProjectIsNotSearched(t *testing.T) {
	e := trashFixture(t)
	if err := e.Store.PutSummary(KindProject, "gone", "# gone\n\nzebrafishword\n"); err != nil {
		t.Fatal(err)
	}
	if len(e.Search("zebrafishword", false, 10)) == 0 {
		t.Fatal("the summary is not searchable before the delete")
	}
	if _, err := e.DeleteProject("gone", time.Now()); err != nil {
		t.Fatal(err)
	}
	if hits := e.Search("zebrafishword", false, 10); len(hits) != 0 {
		t.Fatalf("a deleted project is still searched: %+v", hits)
	}
}

func TestForgettingASourceEmptiesTheTrashEntriesThatHoldIt(t *testing.T) {
	e := trashFixture(t)
	_ = e.Store.PutChat(&Chat{ID: "src-chat", Connector: "src", Source: "src", Name: "Source chat"})
	if _, err := e.Apply(KindChat, "src-chat", Update{Projects: []string{"gone"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeleteProject("gone", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeleteProject("other", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ForgetSource("src"); err != nil {
		t.Fatal(err)
	}
	trash := e.Trash()
	if len(trash) != 1 || trash[0].Slug != "other" {
		t.Fatalf("trash after forgetting = %+v, want only the untouched project", trash)
	}
}

func TestDeletingAProjectTakesTheProjectsMergedIntoIt(t *testing.T) {
	e := trashFixture(t)
	if _, err := e.Apply(KindProject, "other", Update{MergeInto: "gone"}); err != nil {
		t.Fatal(err)
	}
	entry, err := e.DeleteProject("gone", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(entry.Merged, []string{"other"}) || slices.Contains(e.Store.IDs(KindProject), "other") {
		t.Fatalf("merged = %v, projects = %v", entry.Merged, e.Store.IDs(KindProject))
	}
	if n := len(e.Store.trashedProjects()[entry.ID]); n != 2 {
		t.Errorf("trashed projects in the entry = %d, want 2", n)
	}
	if _, err := e.RestoreProject(entry.ID); err != nil {
		t.Fatal(err)
	}
	if o, ok := e.Store.Project("other"); !ok || o.MergedInto != "gone" {
		t.Fatalf("merged project after restore = %+v, %v", o, ok)
	}
}

func TestLongSlugsStayListedAndDeleteTwice(t *testing.T) {
	e := testEngine(t, nil)
	name := strings.Repeat("项目名称", 10)
	slug, err := e.Apply(KindProject, name, Update{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			if _, err := e.Apply(KindProject, name, Update{Name: name}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.DeleteProject(slug, time.Now()); err != nil {
			t.Fatalf("delete %d: %v", i+1, err)
		}
	}
	if n := len(e.Trash()); n != 2 {
		t.Fatalf("listed trash entries = %d, want 2", n)
	}
}

func TestRestoreKeepsATimelineLineAppendedDuringTheDelete(t *testing.T) {
	e := trashFixture(t)
	entry, err := e.DeleteProject("gone", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// What a racing AppendLog leaves: the directory with only log.md.
	if err := e.Store.AppendLog("gone", time.Now(), "A chat", "late line"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreProject(entry.ID); err != nil {
		t.Fatalf("restore over a stray timeline: %v", err)
	}
	if lines := strings.Join(e.Store.timeline("gone"), "\n"); !strings.Contains(lines, "late line") {
		t.Fatalf("the late line is lost: %q", lines)
	}
}
