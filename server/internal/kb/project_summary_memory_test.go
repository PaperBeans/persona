package kb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

func appendSummaryMemoryBatch(t *testing.T, e *Engine, slug, base, result, choice, reason, at string) {
	t.Helper()
	identity := ProjectMemoryRecord{TodoID: "todo-shared", Agent: "session-shared", Turn: 7, ItemIndex: 2,
		Verification: ProjectMemoryAgentReported, RecordedAt: at}
	if err := e.AppendProjectMemoryBatch(slug, []ProjectMemoryRecord{
		{ID: base + "/decision/1", TodoID: identity.TodoID, Agent: identity.Agent, Turn: identity.Turn, ItemIndex: identity.ItemIndex,
			Type: ProjectMemoryDecision, Question: "Which approach?", Decision: choice, Reason: reason,
			Verification: identity.Verification, RecordedAt: at},
		{ID: base, TodoID: identity.TodoID, Agent: identity.Agent, Turn: identity.Turn, ItemIndex: identity.ItemIndex,
			Type: ProjectMemoryOutcome, Conclusion: result, Verification: identity.Verification, RecordedAt: at},
	}); err != nil {
		t.Fatal(err)
	}
}

func summaryInputPrompt(e *Engine, p *Project) string {
	in := e.projectInput(p)
	in.Language = "English"
	return prompts.RenderKBProjectInput(in.KBProjectInput)
}

func TestProjectSummaryInputLinksMemoryByRecordID(t *testing.T) {
	e := projectMemoryEngine(t, t.TempDir())
	putMemoryProject(t, e, "persona")
	p, _ := e.Store.Project("persona")
	// Both batches deliberately share the legacy todo/agent/turn/item tuple. Only their durable record IDs differ.
	appendSummaryMemoryBatch(t, e, "persona", "todo/machine/session/7/2/item-a", "Result A", "Choice A", "Reason A", "2026-11-20T01:00:00Z")
	appendSummaryMemoryBatch(t, e, "persona", "todo/machine/session/7/2/item-b", "Result B", "Choice B", "Reason B", "2026-11-20T02:00:00Z")
	if err := e.AppendProjectMemory("persona", ProjectMemoryRecord{ID: "legacy-outcome", Conclusion: "Legacy result",
		Decision: "Legacy choice", Reason: "Legacy reason"}); err != nil {
		t.Fatal(err)
	}

	got := summaryInputPrompt(e, p)
	for _, want := range []string{
		"[todo/machine/session/7/2/item-a] Result A · 2026-11-20T01:00:00Z · agent_reported · not supervisor-accepted",
		"[todo/machine/session/7/2/item-a/decision/1] Which approach? → Choice A; reason: Reason A; result: Result A; source time: 2026-11-20T01:00:00Z · agent_reported · not supervisor-accepted",
		"[todo/machine/session/7/2/item-b/decision/1] Which approach? → Choice B; reason: Reason B; result: Result B; source time: 2026-11-20T02:00:00Z · agent_reported · not supervisor-accepted",
		"[legacy-outcome:legacy-decision]  → Legacy choice; reason: Legacy reason; result: Legacy result; source time: not recorded · agent_reported · not supervisor-accepted",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary input missing or mislinking memory:\nwant %q\n--- input ---\n%s", want, got)
		}
	}
}

func TestProjectSummaryInputBoundsMemoryAndKeepsEffectiveDecisions(t *testing.T) {
	e := projectMemoryEngine(t, t.TempDir())
	putMemoryProject(t, e, "persona")
	p, _ := e.Store.Project("persona")
	for i := 0; i < 7; i++ {
		appendSummaryMemoryBatch(t, e, "persona", fmt.Sprintf("base-%d", i), fmt.Sprintf("result-%d", i),
			fmt.Sprintf("choice-%d", i), fmt.Sprintf("reason-%d", i), fmt.Sprintf("2026-11-20T0%d:00:00Z", i))
	}
	for i := 0; i < 4; i++ {
		if err := e.AppendProjectMemory("persona", ProjectMemoryRecord{ID: fmt.Sprintf("pitfall-%d", i),
			Type: ProjectMemoryPitfall, Conclusion: fmt.Sprintf("pit-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.AppendProjectMemory("persona", ProjectMemoryRecord{ID: "replacement", Type: ProjectMemoryDecision,
		Question: "Which approach?", Decision: "replacement-choice", SupersedesID: "base-6/decision/1"}); err != nil {
		t.Fatal(err)
	}

	got := summaryInputPrompt(e, p)
	for i := 2; i < 7; i++ {
		if i == 6 {
			continue // explicitly superseded by replacement
		}
		for _, want := range []string{fmt.Sprintf("choice-%d", i), fmt.Sprintf("result-%d", i)} {
			if !strings.Contains(got, want) {
				t.Errorf("summary input lost %q:\n%s", want, got)
			}
		}
	}
	for _, unwanted := range []string{"choice-0", "choice-1", "choice-6", "result-0", "result-1", "pit-0"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("summary input exceeded its bound or kept a superseded record %q:\n%s", unwanted, got)
		}
	}
	for _, want := range []string{
		"[replacement] Which approach? → replacement-choice; reason: not recorded; result: not recorded; source time: not recorded · agent_reported · not supervisor-accepted",
		"pit-3", "pit-2", "pit-1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary input missing %q:\n%s", want, got)
		}
	}
}

func TestProjectSummaryInputWithoutUsableMemoryMatchesLegacy(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "malformed", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			e := projectMemoryEngine(t, t.TempDir())
			putMemoryProject(t, e, "persona")
			p, _ := e.Store.Project("persona")
			if err := e.Store.AppendLog("persona", time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC), "legacy", "Old timeline"); err != nil {
				t.Fatal(err)
			}
			path := e.Store.Path(KindProject, "persona", "memory.jsonl")
			switch kind {
			case "empty", "malformed":
				raw := []byte{}
				if kind == "malformed" {
					raw = []byte("not-json\n")
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			in := e.projectInput(p)
			in.Language = "English"
			got := prompts.RenderKBProjectInput(in.KBProjectInput)
			want := prompts.RenderKBProjectInput(prompts.KBProjectInput{Language: "English", Pinned: in.Pinned,
				Name: in.Name, Kind: in.Kind, Aliases: in.Aliases, Tags: in.Tags, Summary: in.Summary,
				Log: in.Log, Chats: in.Chats, Moments: in.Moments})
			if got != want {
				t.Fatalf("legacy project changed with %s memory:\n--- got ---\n%s\n--- want ---\n%s", kind, got, want)
			}
			if kind == "empty" {
				raw, err := os.ReadFile(path)
				if err != nil || len(raw) != 0 {
					t.Fatalf("reading summary input migrated the empty file: %q, %v", raw, err)
				}
			}
		})
	}
}

func TestProjectCommitRejectsMemoryChangedAfterInput(t *testing.T) {
	e := projectMemoryEngine(t, t.TempDir())
	putMemoryProject(t, e, "persona")
	p, _ := e.Store.Project("persona")
	p.Dirty = true
	if err := e.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	appendSummaryMemoryBatch(t, e, "persona", "base-before", "Before result", "Before choice", "Before reason", "2026-11-20T01:00:00Z")
	frozen := e.projectInput(p)
	appendSummaryMemoryBatch(t, e, "persona", "base-after", "After result", "After choice", "After reason", "2026-11-20T02:00:00Z")
	if err := e.commitProject(p, frozen, projectDigest{Summary: "STALE SUMMARY", OneLine: "STALE ONE LINE"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(e.Store.Path(KindProject, "persona", "summary.md"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "STALE SUMMARY") {
		t.Fatalf("a summary computed from old memory was committed: %q", raw)
	}
	current, _ := e.Store.Project("persona")
	if !current.Dirty || current.OneLine == "STALE ONE LINE" {
		t.Fatalf("stale memory input changed project metadata: %+v", current)
	}
}

func TestProjectMemoryMarksSummaryStale(t *testing.T) {
	e := testEngine(t, &scriptModel{replies: []string{`{"summary":"Current","one_line":"Now"}`}})
	p := &Project{Slug: "persona", Name: "persona", Status: "active", Dirty: true}
	if err := e.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if n := e.RefreshProjects(context.Background(), 1); n != 1 {
		t.Fatalf("initial refresh = %d", n)
	}
	_, before := projectViews(t, e, "persona")
	fieldReview(t, before.Freshness.Fields["summary"], "", 0)

	appendSummaryMemoryBatch(t, e, "persona", "new-base", "New result", "New choice", "New reason", "2026-11-20T01:00:00Z")
	_, after := projectViews(t, e, "persona")
	summary := after.Freshness.Fields["summary"]
	if !summary.NeedsReview || summary.ReviewReason != "new_evidence" || summary.UncoveredEvidenceCount == nil || *summary.UncoveredEvidenceCount != 2 {
		t.Fatalf("new memory did not mark the summary stale: %+v", summary)
	}

	// The established to-do acceptance path marks the project dirty through Update.Log; no new M1 write semantics are needed.
	projectUpdateOK(t, e, "persona", Update{Log: "Accepted outcome"})
	if n := e.RefreshProjects(context.Background(), 1); n != 1 {
		t.Fatalf("memory refresh = %d", n)
	}
	_, refreshed := projectViews(t, e, "persona")
	fieldReview(t, refreshed.Freshness.Fields["summary"], "", 0)
}
