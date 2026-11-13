package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// deliveryReviewService is a supervised task with a GitHub PR, the hub running oldHub and the forge as given, with
// done_needs_delivery on unless off.
func deliveryReviewService(t *testing.T, forge *fakeForge, hub *runningHub, off bool) (*Service, Todo) {
	t.Helper()
	s, _, todo := newSupervisedService(t)
	s.deploy.forge = forge
	if hub != nil {
		s.deploy.hubBuild = hub.build
	} else { // a hub that does not say its commit: nothing to check the deploy against
		s.deploy.hubBuild = func(context.Context) (string, string, error) { return "dev", "", nil }
	}
	s.deploy.listPaths = []string{}
	if !off {
		deliveryOn(t, s)
	}
	s.setTaskStage(context.Background(), todo.ID, "review", "https://github.com/o/r/pull/9")
	return s, todo
}

func acceptReview(s *Service, todo string, extra map[string]any) (string, error) {
	args := map[string]any{"todo_id": todo, "verdict": "accepted", "note": "做完了",
		"checks": []ReviewCheck{{Criterion: "PR 合并到 master", Met: true, Evidence: "draft PR"}}}
	for k, v := range extra {
		args[k] = v
	}
	raw, _ := json.Marshal(args)
	return s.runTool(context.Background(), "server", "conv1", "task_review", raw)
}

// With done_needs_delivery on, accepted closes a task only once its PR is live (#6428): an unmerged PR, a merge the
// running hub does not contain yet and a check that fails all refuse it, and the to-do stays open; a deployed PR, the
// switch off, a merged PR where the hub cannot be checked against, and outcome cannot are accepted.
func TestAcceptedNeedsTheTasksPRLive(t *testing.T) {
	for _, tc := range []struct {
		name   string
		forge  *fakeForge
		hub    string // "" : the hub does not say its commit
		off    bool
		extra  map[string]any
		refuse string
	}{
		{name: "unmerged", forge: &fakeForge{}, hub: oldHub, refuse: "not merged"},
		{name: "unmerged, unverifiable hub", forge: &fakeForge{}, refuse: "not merged"},
		{name: "merged, not deployed", forge: &fakeForge{merged: true, sha: mergeSHA, contains: map[string]bool{oldHub: false}}, hub: oldHub, refuse: "does not contain it yet"},
		{name: "forge error", forge: &fakeForge{err: errors.New("403")}, hub: oldHub, refuse: "could not be checked"},
		{name: "forge error, unverifiable hub", forge: &fakeForge{err: errors.New("403")}, refuse: "could not be checked"},
		{name: "the review's link names the unmerged PR", forge: &fakeForge{}, hub: oldHub, extra: map[string]any{"link": "https://github.com/o/r/pull/9"}, refuse: "not merged"},
		{name: "deployed", forge: &fakeForge{merged: true, sha: mergeSHA, contains: map[string]bool{oldHub: true}}, hub: oldHub},
		{name: "merged, unverifiable hub", forge: &fakeForge{merged: true, sha: mergeSHA}},
		{name: "switch off", forge: &fakeForge{}, hub: oldHub, off: true},
		{name: "outcome cannot", forge: &fakeForge{}, hub: oldHub, extra: map[string]any{"outcome": "cannot", "reason": "做不了"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hub *runningHub
			if tc.hub != "" {
				hub = &runningHub{commit: tc.hub}
			}
			s, todo := deliveryReviewService(t, tc.forge, hub, tc.off)
			res, err := acceptReview(s, todo.ID, tc.extra)
			got := todoNow(s, todo.ID)
			if tc.refuse == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if got.DoneAt == nil {
					t.Fatalf("not closed: %s", res)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.refuse) || !strings.Contains(err.Error(), "external_event") {
				t.Fatalf("accepted (%v): %s", err, res)
			}
			if got.DoneAt != nil {
				t.Fatalf("closed at %v", got.DoneAt)
			}
			if v := got.Supervision; v != nil && v.State == reviewAccepted {
				t.Fatalf("the ledger says accepted: %+v", v)
			}
		})
	}
}
