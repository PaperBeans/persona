package projection

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func parkedFact(id string, todosV2 bool, ev *ParkEvidence) Facts {
	return Facts{
		ID:        id,
		Title:     id,
		CreatedAt: hours(-5),
		UpdatedAt: hours(-2),
		Origin:    "conv_main",
		TodosV2:   todosV2,
		Supervision: &Supervision{
			State:     reviewParked,
			UpdatedAt: hours(-2),
			Parked: &Parked{
				Reason:    "Waiting on external blocker",
				Until:     testNow.Add(4 * time.Hour),
				WaitingOn: parkExternalEvent,
				Evidence:  ev,
			},
			Reviews: []Review{{At: hours(-2), Verdict: reviewParked}},
		},
	}
}

func prWaitingFact(id, prURL string, todosV2 bool) Facts {
	return Facts{
		ID:        id,
		Title:     id,
		CreatedAt: hours(-4),
		UpdatedAt: hours(-1),
		Origin:    "conv_main",
		TodosV2:   todosV2,
		PRURL:     prURL,
		PRStatus: &PRStatus{
			URL:       prURL,
			State:     prOpen,
			CheckedAt: testNow.Add(-5 * time.Minute),
		},
	}
}

func TestSharedBlockersGatedByTodosV2(t *testing.T) {
	list := []Facts{
		parkedFact("todo_a", false, &ParkEvidence{Kind: parkExternalEvent, URL: "https://github.com/Acme/App/pull/42"}),
		parkedFact("todo_b", false, &ParkEvidence{Kind: parkExternalEvent, URL: "https://github.com/acme/app/pull/42/?w=1"}),
	}
	views := Views(list, testNow)
	if got := SharedBlockers(list, views); len(got) != 0 {
		t.Fatalf("todos_v2=false must omit shared blockers, got: %+v", got)
	}

	for i := range list {
		list[i].TodosV2 = true
	}
	viewsV2 := Views(list, testNow)
	got := SharedBlockers(list, viewsV2)
	want := []SharedBlocker{{
		Key:     "pull_request:https://github.com/acme/app/pull/42",
		Kind:    SharedBlockerKindPullRequest,
		Label:   "acme/app#42",
		URL:     "https://github.com/acme/app/pull/42",
		TodoIDs: []string{"todo_a", "todo_b"},
		Count:   2,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SharedBlockers mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestSharedBlockersNormalizationFilteringAndDeterministicOrder(t *testing.T) {
	doneAt := hours(-1)
	cancelledAt := hours(-1)
	somedayUntil := testNow.Add(48 * time.Hour)

	// 3 open tasks share PR #100 (trailing slash, query, fragment, casing, PRURL vs Parked.Evidence.URL).
	pr1 := parkedFact("todo_pr_3", true, &ParkEvidence{
		Kind:   "merge_review",
		Target: "Upstream PR 100",
		URL:    "https://GitHub.com/NWVerse/Persona/pull/100/?w=1#discussion_r1",
	})
	pr2 := prWaitingFact("todo_pr_1", "https://github.com/nwverse/persona/pull/100/", true)
	pr3 := prWaitingFact("todo_pr_2", "https://github.com/nwverse/persona/pull/100#top", true)

	// Closed (Done), Cancelled, and Someday tasks on PR #100 must be excluded.
	prDone := prWaitingFact("todo_pr_done", "https://github.com/nwverse/persona/pull/100", true)
	prDone.DoneAt = &doneAt
	prCancelled := parkedFact("todo_pr_cancelled", true, &ParkEvidence{
		Kind: parkExternalEvent,
		URL:  "https://github.com/nwverse/persona/pull/100",
	})
	prCancelled.DoneAt = &cancelledAt
	prCancelled.CancelledAt = &cancelledAt
	prSomeday := Facts{
		ID:           "todo_pr_someday",
		Title:        "Deferred PR task",
		CreatedAt:    hours(-6),
		UpdatedAt:    hours(-6),
		TodosV2:      true,
		PRURL:        "https://github.com/nwverse/persona/pull/100",
		SomedayUntil: &somedayUntil,
	}

	// 2 open tasks share GitHub Issue #77 (trailing slash + query + casing).
	issue1 := parkedFact("todo_iss_b", true, &ParkEvidence{
		Kind: parkExternalEvent,
		URL:  "https://GITHUB.COM/Org/Service/issues/77/?from=alert#event-9",
	})
	issue2 := parkedFact("todo_iss_a", true, &ParkEvidence{
		Kind: parkExternalEvent,
		URL:  "https://github.com/org/service/issues/77",
	})

	// 2 open tasks share a pure external target (whitespace + casing normalization).
	target1 := parkedFact("todo_tgt_2", true, &ParkEvidence{
		Kind:   "person_reply",
		Target: "  Alice   Chen  ",
	})
	target2 := parkedFact("todo_tgt_1", true, &ParkEvidence{
		Kind:   "person_reply",
		Target: "alice chen",
	})

	// Singletons must not be aggregated (including 1 open + 1 closed on the same PR).
	singletonPR := parkedFact("todo_single_pr", true, &ParkEvidence{
		Kind: parkExternalEvent,
		URL:  "https://github.com/org/service/pull/999",
	})
	singletonPRClosed := parkedFact("todo_single_pr_done", true, &ParkEvidence{
		Kind: parkExternalEvent,
		URL:  "https://github.com/org/service/pull/999",
	})
	singletonPRClosed.DoneAt = &doneAt
	singletonTarget := parkedFact("todo_single_tgt", true, &ParkEvidence{
		Kind:   "person_reply",
		Target: "Bob Only",
	})

	list := []Facts{
		pr1, pr2, pr3, prDone, prCancelled, prSomeday,
		issue1, issue2,
		target1, target2,
		singletonPR, singletonPRClosed, singletonTarget,
	}
	views := Views(list, testNow)
	got := SharedBlockers(list, views)

	// Expected order: Count desc (3 -> 2 -> 2), then Key asc ("issue:..." < "target:...").
	want := []SharedBlocker{
		{
			Key:     "pull_request:https://github.com/nwverse/persona/pull/100",
			Kind:    SharedBlockerKindPullRequest,
			Label:   "nwverse/persona#100",
			URL:     "https://github.com/nwverse/persona/pull/100",
			TodoIDs: []string{"todo_pr_1", "todo_pr_2", "todo_pr_3"},
			Count:   3,
		},
		{
			Key:     "issue:https://github.com/org/service/issues/77",
			Kind:    SharedBlockerKindIssue,
			Label:   "org/service#77",
			URL:     "https://github.com/org/service/issues/77",
			TodoIDs: []string{"todo_iss_a", "todo_iss_b"},
			Count:   2,
		},
		{
			Key:     "target:alice chen",
			Kind:    SharedBlockerKindExternalTarget,
			Label:   "Alice Chen",
			TodoIDs: []string{"todo_tgt_1", "todo_tgt_2"},
			Count:   2,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SharedBlockers mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}

	// Reversing input order must yield byte-identical output.
	rev := slices.Clone(list)
	slices.Reverse(rev)
	revViews := Views(rev, testNow)
	gotRev := SharedBlockers(rev, revViews)
	if !reflect.DeepEqual(gotRev, want) {
		t.Fatalf("SharedBlockers not deterministic on reversed input:\ngot:  %+v\nwant: %+v", gotRev, want)
	}
}
