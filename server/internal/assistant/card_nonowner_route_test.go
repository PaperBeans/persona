package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

func hasRelayFrom(s *Service, to, from, needle string) bool {
	for _, m := range laterFor(s, to) {
		if m.From == from && (needle == "" || strings.Contains(m.Text, needle)) {
			return true
		}
	}
	return false
}

// Source 1 (agent_permission): non-six-class tool permissions from an unsupervised agent or from the
// main conversation itself go to the main conversation / background supervisor instead of pushing to
// the owner, while six-class permissions (secret/form, outward/delete approval) still push to the owner.
func TestAgentPermissionNonOwnerRoutedAndSixClassesStillPush(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	setSettings(t, e.s, func(v *Settings) { v.BackgroundSupervisor = "bg1" })
	now := time.Now().UTC()

	// Unsupervised agent permission -> main conversation (conv1), no owner push.
	unsupervised := proto.Interaction{
		ID: "ix_perm_unsupervised", MachineID: "mac", SessionID: "standalone-agent",
		Kind: proto.IxPermission, ToolName: "Bash", Summary: "git status",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	uDone := e.raise(unsupervised)
	waitFor(t, "unsupervised permission routed to main", func() bool {
		return hasRelayFrom(e.s, "conv1", cardAgentPermission, "interaction_id="+unsupervised.ID)
	})
	if !e.lastShown(unsupervised.ID).Delegating {
		t.Fatal("unsupervised permission was not marked delegating")
	}

	// Main conversation's own non-six-class tool permission -> background supervisor (bg1), no owner push.
	mainOwn := proto.Interaction{
		ID: "ix_perm_main_own", MachineID: "server", SessionID: "conv1",
		Kind: proto.IxPermission, ToolName: "Read", Summary: "/tmp/report.md",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	mDone := e.raise(mainOwn)
	waitFor(t, "main permission routed to background supervisor", func() bool {
		return hasRelayFrom(e.s, "bg1", cardAgentPermission, "interaction_id="+mainOwn.ID)
	})

	// Six-class permission (delete/outward action) -> still pushed to the owner.
	outward := proto.Interaction{
		ID: "ix_perm_outward", MachineID: "mac", SessionID: "standalone-agent",
		Kind: proto.IxPermission, ToolName: "Bash", Summary: "delete user records permanently",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	oDone := e.raise(outward)
	waitFor(t, "six-class permission pushed to owner", func() bool { return e.pushesOf(outward.ID) == 1 })

	// Six-class secret/form permission -> still pushed to the owner.
	secretIx := proto.Interaction{
		ID: "ix_perm_secret", MachineID: "mac", SessionID: "standalone-agent",
		Kind: proto.IxPermission, ToolName: "Bash", Summary: "enter password", Secret: true,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	sDone := e.raise(secretIx)
	waitFor(t, "secret permission pushed to owner", func() bool { return e.pushesOf(secretIx.ID) == 1 })

	e.settle()
	if n := e.pushesOf(unsupervised.ID); n != 0 {
		t.Fatalf("unsupervised non-owner permission pushed %d times", n)
	}
	if n := e.pushesOf(mainOwn.ID); n != 0 {
		t.Fatalf("main own non-owner permission pushed %d times", n)
	}
	if c := cardsToday(t, e.s)[cardAgentPermission]; c.N != 1 { // only outward counted under agent_permission; secret is agent_secret_form
		t.Fatalf("agent_permission ledger = %+v, want only the 1 six-class outward permission", c)
	}

	allow := true
	for _, pair := range []struct {
		id string
		ch <-chan proto.Resolution
	}{
		{unsupervised.ID, uDone}, {mainOwn.ID, mDone}, {outward.ID, oDone}, {secretIx.ID, sDone},
	} {
		if err := e.s.resolve(pair.id, proto.Resolution{Allow: &allow}); err != nil {
			t.Fatal(err)
		}
		answerOf(t, pair.ch)
	}
}

// Antigravity's ACP permission title and Skill instruction arguments are local data, not evidence
// of an outward action. Their sanitized production shapes take the non-owner route without a push
// or an agent_permission ledger entry; form/secret/owner-proposal precedence is pinned in TestUserApproval.
func TestLocalNonActionPermissionsUseNonOwnerRoute(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name string
		ix   proto.Interaction
	}{
		{"antigravity draft mentioning deletion", proto.Interaction{
			ID: "ix_local_agy_delete", MachineID: "mac", SessionID: "agy-local-draft", Kind: proto.IxPermission,
			ToolName: "File /work/draft.md", ToolInput: []byte(`{"TargetFile":"/work/draft.md","CodeContent":"删除该字段"}`), // i18n-ignore: synthetic Antigravity file payload
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}},
		{"antigravity draft mentioning a comment", proto.Interaction{
			ID: "ix_local_agy_comment", MachineID: "mac", SessionID: "agy-local-review", Kind: proto.IxPermission,
			ToolName: "File /work/review.md", ToolInput: []byte(`{"Path":"/work/review.md","Content":"Replay the comment"}`),
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}},
		{"Skill instructions with a negated send", proto.Interaction{
			ID: "ix_local_skill", MachineID: "mac", SessionID: "standalone-skill", Kind: proto.IxPermission,
			ToolName: "Skill", ToolInput: []byte(`{"skill":"agent-browser","args":"只读检查、不发送邮件、不改设置"}`), // i18n-ignore: synthetic skill arguments
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRouteEnv(t, nil, true)
			if got := userApproval(tc.ix); got != "" {
				t.Fatalf("local non-action is owner-only: %s", got)
			}
			answered := e.raise(tc.ix)
			waitFor(t, "local non-action routed to main", func() bool {
				return hasRelayFrom(e.s, "conv1", cardAgentPermission, "interaction_id="+tc.ix.ID)
			})
			e.settle()
			if n := e.pushesOf(tc.ix.ID); n != 0 {
				t.Fatalf("local non-action pushed to the owner %d times", n)
			}
			if c := cardsToday(t, e.s)[cardAgentPermission]; c.N != 0 {
				t.Fatalf("agent_permission ledger = %+v, want 0", c)
			}
			allow := true
			if err := e.s.resolve(tc.ix.ID, proto.Resolution{Allow: &allow}); err != nil {
				t.Fatal(err)
			}
			answerOf(t, answered)
		})
	}
}

// Source 2 (agent_question): non-six-class questions and parsed non-user terminal dialogs from an
// unsupervised agent go to the main conversation without pushing to the owner, while six-class cards
// (elicitation/form, folder trust, delete risk) still push to the owner.
func TestAgentQuestionNonOwnerRoutedAndSixClassesStillPush(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	now := time.Now().UTC()

	// Unsupervised ordinary IxQuestion -> main conversation, no push.
	q := askQuestion("ix_q_unsupervised", "standalone-q")
	qDone := e.raise(q)
	waitFor(t, "unsupervised question routed to main", func() bool {
		return hasRelayFrom(e.s, "conv1", cardAgentQuestion, "interaction_id="+q.ID)
	})

	// Unsupervised ordinary parsed IxScreen (e.g. default permission mode prompt) -> main conversation, no push.
	scr := proto.Interaction{
		ID: "scr_auto_mode", MachineID: "mac", SessionID: "standalone-scr",
		Kind: proto.IxScreen, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		Dialog: &screen.Dialog{
			Question: "Make auto mode your default permission mode?",
			Rev:      "r1",
			Options: []screen.Option{
				{Key: "1", Label: "Yes, set auto mode", Keys: []string{"enter"}},
				{Key: "2", Label: "No, keep manual mode", Keys: []string{"down", "enter"}},
			},
		},
	}
	scrDone := e.raise(scr)
	waitFor(t, "unsupervised screen routed to main", func() bool {
		return hasRelayFrom(e.s, "conv1", cardAgentQuestion, "interaction_id="+scr.ID)
	})

	// Six-class IxScreen (folder trust) -> still pushed to the owner.
	trust := proto.Interaction{
		ID: "scr_trust_folder", MachineID: "mac", SessionID: "standalone-trust",
		Kind: proto.IxScreen, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		Dialog: &screen.Dialog{
			Question: "Quick safety check: Is this a project you trust?",
			Rev:      "r2",
			Options: []screen.Option{
				{Key: "1", Label: "Yes, I trust this folder", Keys: []string{"enter"}},
				{Key: "2", Label: "No, exit", Keys: []string{"down", "enter"}},
			},
		},
	}
	trustDone := e.raise(trust)
	waitFor(t, "folder trust screen pushed to owner", func() bool { return e.pushesOf(trust.ID) == 1 })

	// Six-class IxElicitation -> still pushed to the owner when elicitation clients are reachable.
	e.s.fleet.mu.Lock()
	e.s.fleet.clients.Elicitation = true
	e.s.fleet.mu.Unlock()
	elicit := proto.Interaction{
		ID: "ix_elicit", MachineID: "mac", SessionID: "standalone-elicit",
		Kind: proto.IxElicitation, Summary: "Sign in form",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	elicitDone := e.raise(elicit)
	waitFor(t, "elicitation pushed to owner", func() bool { return e.pushesOf(elicit.ID) == 1 })

	e.settle()
	if n := e.pushesOf(q.ID); n != 0 {
		t.Fatalf("unsupervised non-owner question pushed %d times", n)
	}
	if n := e.pushesOf(scr.ID); n != 0 {
		t.Fatalf("unsupervised non-owner screen pushed %d times", n)
	}

	for _, pair := range []struct {
		id  string
		res proto.Resolution
		ch  <-chan proto.Resolution
	}{
		{q.ID, proto.Resolution{Answers: map[string]string{"用哪个分支？": "main"}}, qDone},
		{scr.ID, proto.Resolution{Option: "2"}, scrDone},
		{trust.ID, proto.Resolution{Option: "2"}, trustDone},
		{elicit.ID, proto.Resolution{Action: "decline"}, elicitDone},
	} {
		if err := e.s.resolve(pair.id, pair.res); err != nil {
			t.Fatal(err)
		}
		answerOf(t, pair.ch)
	}
}

// Source 3 (agent_reply): reply reminders go to the task's supervisor (when supervised) or to the
// main conversation (when unsupervised), and never push to the owner.
func TestAgentReplyGoesToSupervisorOrMainNotOwner(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	ended := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)

	supervised := proto.Session{
		SessionID: "worker-1", Title: "受督办任务", Cwd: "/code/app",
		Status: proto.StatusDone, State: proto.StateIdle, UpdatedAt: ended,
		Asks: "要继续跑全量吗？", AwaitsReply: true,
	}
	unsupervised := proto.Session{
		SessionID: "standalone-reply", Title: "独立对话", Cwd: "/code/other",
		Status: proto.StatusDone, State: proto.StateIdle, UpdatedAt: ended,
		Asks: "选方案 A 还是 B？", AwaitsReply: true,
	}
	if err := e.s.remindReply("mac", supervised); err != nil {
		t.Fatal(err)
	}
	if err := e.s.remindReply("mac", unsupervised); err != nil {
		t.Fatal(err)
	}

	if !hasRelayFrom(e.s, "lead1", cardAgentReply, "session_id=worker-1") {
		t.Fatalf("supervised reply reminder did not go to supervisor lead1: %+v", laterFor(e.s, "lead1"))
	}
	if !hasRelayFrom(e.s, "conv1", cardAgentReply, "session_id=standalone-reply") {
		t.Fatalf("unsupervised reply reminder did not go to main conv1: %+v", laterFor(e.s, "conv1"))
	}
	for _, n := range e.f.pushed() {
		if n.Kind == assistantwire.NotifyReply {
			t.Fatalf("agent_reply was still pushed to the owner: %+v", n)
		}
	}
	if c := cardsToday(t, e.s)[cardAgentReply]; c.N != 0 {
		t.Fatalf("agent_reply ledger = %+v, want 0", c)
	}
}

// Source 4 (extension_approval): non-six-class extension approvals (such as internal maintenance tools)
// go to the main conversation without pushing to the owner, while six-class extension approvals
// (outbound writes such as mail_send, or secret/form cards) still push to the owner.
func TestExtensionApprovalNonOwnerRoutedAndSixClassesStillPush(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	now := time.Now().UTC()

	// Non-six-class extension approval (like enkeep_rerun) -> main conversation, no owner push.
	extIx := proto.Interaction{
		ID: "ix_ext_rerun", MachineID: "mac", SessionID: "",
		Kind: proto.IxPermission, ToolName: "enkeep_rerun", SessionTitle: "Persona",
		Summary: "Rerun extraction for 2026-10-27", ToolInput: json.RawMessage(`{"from":"2026-10-27"}`),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	extDone := make(chan proto.Resolution, 1)
	go func() {
		r, _ := e.s.askHub(context.Background(), extIx)
		extDone <- r
	}()
	waitFor(t, "non-owner extension approval routed to main", func() bool {
		return hasRelayFrom(e.s, "conv1", cardExtensionApproval, "interaction_id="+extIx.ID)
	})

	// Six-class outbound extension approval (mail_send, kind 3) -> still pushed to the owner.
	mailIx := proto.Interaction{
		ID: "ix_ext_mail", MachineID: "mac", SessionID: "",
		Kind: proto.IxPermission, ToolName: "mail_send", SessionTitle: "Persona",
		Summary: "Send email to external recipient", ToolInput: json.RawMessage(`{"to":["a@example.com"]}`),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	mailDone := make(chan proto.Resolution, 1)
	go func() {
		r, _ := e.s.askHub(context.Background(), mailIx)
		mailDone <- r
	}()
	waitFor(t, "outbound mail_send pushed to owner", func() bool { return e.pushesOf(mailIx.ID) == 1 })

	e.settle()
	if n := e.pushesOf(extIx.ID); n != 0 {
		t.Fatalf("non-owner extension approval pushed %d times", n)
	}
	if c := cardsToday(t, e.s)[cardExtensionApproval]; c.N != 0 {
		t.Fatalf("extension_approval ledger = %+v, want 0", c)
	}
	if c := cardsToday(t, e.s)[cardOutboundWrite]; c.N != 1 {
		t.Fatalf("outbound_write ledger = %+v, want 1", c)
	}

	allow := true
	if err := e.s.resolve(extIx.ID, proto.Resolution{Allow: &allow}); err != nil {
		t.Fatal(err)
	}
	answerOf(t, extDone)
	if err := e.s.resolve(mailIx.ID, proto.Resolution{Allow: &allow}); err != nil {
		t.Fatal(err)
	}
	answerOf(t, mailDone)
}

// Source 5 (schedule_failed): failed non-briefing scheduled runs go to their originating supervisor
// or the main conversation instead of pushing to the owner, while a briefing fallback (a six-class
// owner-subscribed briefing) still pushes to the owner.
func TestScheduleFailedGoesToSupervisorOrMainAndBriefFallbackStillPushes(t *testing.T) {
	s, f := newQuietService(t)
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	ctx := context.Background()

	// Unowned one-off follow-up fails -> main conversation (conv1), no owner push.
	s.oneOffFailed(ctx,
		Task{ID: "fu_main", Title: "Check batch", Repeat: repeatOnce},
		ScheduleRun{ID: "run_1", Error: "machine offline"},
	)
	if !hasRelayFrom(s, "conv1", cardScheduleFailed, "fu_main") {
		t.Fatalf("unowned schedule_failed did not go to main conv1: %+v", laterFor(s, "conv1"))
	}

	// Leader-owned one-off follow-up fails -> leader (lead1), no owner push.
	s.oneOffFailed(ctx,
		Task{ID: "fu_lead", Title: "Leader patrol", Repeat: repeatOnce, SessionID: "lead1"},
		ScheduleRun{ID: "run_2", Error: "upstream 503"},
	)
	if !hasRelayFrom(s, "lead1", cardScheduleFailed, "fu_lead") {
		t.Fatalf("leader schedule_failed did not go to lead1: %+v", laterFor(s, "lead1"))
	}

	// Verify no owner push and card_ledger has 0 schedule_failed.
	if p := f.pushed(); len(p) != 0 {
		t.Fatalf("schedule_failed still pushed to the owner: %+v", p)
	}
	if c := cardsToday(t, s)[cardScheduleFailed]; c.N != 0 {
		t.Fatalf("schedule_failed ledger = %+v, want 0", c)
	}

	// Six-class briefing fallback (owner-subscribed briefing failed to start) still pushes.
	briefTask := Task{ID: "brief_1", Title: "Morning briefing", Repeat: repeatDaily, Facts: true}
	now := time.Now()
	s.briefReportStart(ctx, briefTask, now)
	s.briefNotStarted(ctx, briefTask,
		ScheduleRun{ID: "run_3", RefID: briefTask.ID, Due: now, Error: "machine offline"},
		false,
	)
	waitFor(t, "brief_fallback pushed to owner", func() bool {
		return cardsToday(t, s)[cardBriefFallback].N == 1
	})
}
