package assistant

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// agentStopped leaves task agent s1 on box stopped with reply as its last words, without telling the assistant its turn
// ended: a turn end the hub missed (it restarted in the middle of the turn).
func agentStopped(f *fakeHub, reply string) {
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusDone,
		State: proto.StateUnread, LastText: reply, UpdatedAt: time.Now()})
}

type doneCheckAnswer struct {
	Verdict    string `json:"verdict"`
	WouldClose bool   `json:"would_close"`
	Skip       string `json:"skip"`
	Applied    *bool  `json:"applied"`
	Stage      string `json:"stage"`
}

func doneCheck(t *testing.T, s *Service, id, body string) doneCheckAnswer {
	t.Helper()
	w := serveAs(s, "full", "POST", "/api/v1/todos/"+id+"/done-check", body)
	var out doneCheckAnswer
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("done-check %s: %d %s", body, w.Code, w.Body.String())
	}
	return out
}

// Re-check (#4867): without apply it only answers, as before; with apply it moves the stage from the agent's last reply
// (a turn end the hub missed) and writes the verdict: an unsupervised task the judge finds done is closed.
func TestDoneCheckApplyClosesAFinishedTask(t *testing.T) {
	s, f := newTaskService(t)
	f.judge = `{"verdict":"done","reason":"修好了"}`
	todo := startedTask(t, s, f)
	if todo.Supervision != nil {
		t.Fatalf("supervised: %+v", todo.Supervision)
	}
	agentStopped(f, "修好了，PR https://github.com/o/r/pull/9 已合并")

	got := doneCheck(t, s, todo.ID, "")
	if got.Verdict != verdictDone || !got.WouldClose || got.Applied != nil {
		t.Fatalf("answer: %+v", got)
	}
	if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Check != nil || stageIndex(now.Stage) >= stageIndex("review") {
		t.Fatalf("written without apply: done %v, check %+v, stage %q", now.DoneAt, now.Check, now.Stage)
	}

	got = doneCheck(t, s, todo.ID, `{"apply":true}`)
	if got.Verdict != verdictDone || got.Applied == nil || !*got.Applied || got.Stage != "merge" {
		t.Fatalf("applied: %+v", got)
	}
	now := todoNow(s, todo.ID)
	if now.DoneAt == nil || now.Check == nil || now.Check.Verdict != verdictDone || now.Stage != "merge" || now.PRURL != "https://github.com/o/r/pull/9" {
		t.Fatalf("after apply: done %v, check %+v, stage %q, pr %q", now.DoneAt, now.Check, now.Stage, now.PRURL)
	}
	if again := doneCheck(t, s, todo.ID, `{"apply":true}`); again.Applied == nil || *again.Applied || again.Skip != "done" {
		t.Fatalf("a closed task: %+v", again)
	}
}

// A supervised task the judge finds done is not closed by Re-check: it waits for its supervisor's review, which is told.
// A working agent's task, or a body that is not JSON, is not applied.
func TestDoneCheckApplySupervisedWaitsForReview(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	if got := doneCheck(t, s, todo.ID, `{"apply":true}`); got.Applied == nil || *got.Applied {
		t.Fatalf("applied while its agent works: %+v", got)
	}
	agentStopped(f, "修好了，提了 PR")
	if w := serveAs(s, "full", "POST", "/api/v1/todos/"+todo.ID+"/done-check", `{"apply":`); w.Code != http.StatusBadRequest {
		t.Fatalf("a broken body: %d", w.Code)
	}
	got := doneCheck(t, s, todo.ID, `{"apply":true}`)
	if got.Applied == nil || !*got.Applied {
		t.Fatalf("not applied: %+v", got)
	}
	waitFor(t, "waiting for review", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	if now := todoNow(s, todo.ID); now.DoneAt != nil {
		t.Fatalf("a supervised task closed by Re-check: %+v", now)
	}
}
