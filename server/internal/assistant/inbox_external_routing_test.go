package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding/externalbindingadapt"
	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/assistant/repositorystore"
	w "github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type externalRoutingFixture struct {
	*Service
	store *repositorystore.Store
	hub   *fakeHub
}

func newExternalRoutingFixture(t *testing.T) externalRoutingFixture {
	t.Helper()
	s, store := externalSourceHTTPFixture(t)
	h := s.hub.(*fakeHub).withMachine()
	h.inbox = newFakeInbox()
	h.inbox.svc = s
	return externalRoutingFixture{Service: s, store: store, hub: h}
}

func (f externalRoutingFixture) todo(t *testing.T, id, agent string) externalTarget {
	t.Helper()
	if _, err := f.createTaskTodoAs(t.Context(), id, "Work "+id, "A compact scope", "", "", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if agent != "" {
		f.hub.mu.Lock()
		f.hub.sessions["server"] = append(f.hub.sessions["server"], proto.Session{SessionID: agent, Status: proto.StatusWorking})
		f.hub.mu.Unlock()
		if err := f.linkTodoAgent(t.Context(), id, "server", agent, agent, "", "claude"); err != nil {
			t.Fatal(err)
		}
	}
	return externalTarget{ID: w.ExternalTargetID(id), Kind: "todo"}
}

func (f externalRoutingFixture) bind(t *testing.T, ref w.ExternalRef, target externalTarget, policy w.ExternalPolicy) {
	t.Helper()
	if _, err := f.store.CreateExternalBinding(t.Context(), w.ExternalBindingInput{ExternalRef: ref,
		TargetID: target.ID, TargetKind: target.Kind, Policy: policy}); err != nil {
		t.Fatal(err)
	}
}

func (f externalRoutingFixture) policy(t *testing.T, ref w.ExternalRef, policy w.ExternalPolicy) {
	t.Helper()
	if err := f.store.PutExternalSourcePolicy(t.Context(), w.ExternalScopedPolicy{ExternalRef: ref, Policy: policy}); err != nil {
		t.Fatal(err)
	}
}

func (f externalRoutingFixture) collect(t *testing.T, sources ...w.InboxSource) time.Time {
	t.Helper()
	if err := f.InboxCollected(t.Context(), w.InboxRound{Sources: sources}); err != nil {
		t.Fatal(err)
	}
	return time.Now()
}

func (f externalRoutingFixture) sweep(t *testing.T, at time.Time) {
	t.Helper()
	if err := f.sweepExternalInbox(t.Context(), at); err != nil {
		t.Fatal(err)
	}
}

func (f externalRoutingFixture) feed(t *testing.T, target externalTarget) []db.FeedRow {
	t.Helper()
	rows, err := f.store.FeedByRef(t.Context(), string(target.ID), []string{feedRouted}, 20)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f externalRoutingFixture) routingTodos(t *testing.T) []db.TodoRow {
	t.Helper()
	rows, err := f.store.ListTodos(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f externalRoutingFixture) sent() []string {
	f.hub.mu.Lock()
	defer f.hub.mu.Unlock()
	var sent []string
	for _, args := range f.hub.agentSent {
		sent = append(sent, args["session_id"].(string))
	}
	return sent
}

func externalPushRef() w.ExternalRef {
	return w.ExternalRef{Provider: "push", Container: externalNode("source", "push")}
}

func externalPush(thread, cursor, summary string) connector.InboxThing {
	return connector.InboxThing{Thread: thread, Last: cursor, Title: "Update " + thread,
		Preview: summary, Sender: "Reviewer", At: time.Now(), Context: "Private historical context that is outside the summary"}
}

func externalRealtime() w.ExternalPolicy {
	return w.ExternalPolicy{Mode: new(w.ExternalForwardMode("realtime")), ActionRole: new(w.ExternalActionRole("owner"))}
}

func TestExternalInboxLegacyForwardCutoverAndRollback(t *testing.T) {
	f := newExternalRoutingFixture(t)
	// The regular Inbox handling pass also calls legacy forwarding. Exercise
	// the collection-triggered pass on its own so its lock fences delivery.
	f.inbox.handing.Lock()
	defer f.inbox.handing.Unlock()
	target := f.todo(t, "todo_legacy_forward", "legacy_agent")
	const link = "https://github.com/example/repo/pull/83"
	if _, _, err := f.store.ImportTodoRepositoryLinkDetails(t.Context(), string(target.ID),
		[]repository.LinkInput{{URL: link, Role: "delivery"}}, "explicit"); err != nil {
		t.Fatal(err)
	}
	turnInboxTodoForwardOn(t, f.Service)
	thing := connector.InboxThing{
		Thread: "example/repo#83", Last: "comment:1", URL: link, Title: "Review", Sender: "Reviewer", Preview: "Review feedback",
	}
	path, err := externalbindingadapt.Path(githubExternalRef(thing))
	if err != nil {
		t.Fatal(err)
	}
	collect := func(source, id string, wantSends, wantScoped int) {
		t.Helper()
		thing.Last, thing.At = id, time.Now()
		now := f.collect(t, w.InboxSource{ID: source, Via: "github", Things: []connector.InboxThing{thing}})
		// The collector starts a legacy pass after it commits the batch.
		f.inbox.forwarding.Lock()
		f.inbox.forwarding.Unlock()
		f.sweep(t, now)
		activity := f.feed(t, target)
		scoped := slices.DeleteFunc(activity, func(row db.FeedRow) bool {
			return strings.Contains(string(row.Body), `"todo_forward_status"`)
		})
		if got := f.sent(); len(got) != wantSends || len(scoped) != wantScoped {
			t.Fatalf("%s %s sent to %v with %d scoped deliveries, want %d and %d", source, id, got, len(scoped), wantSends, wantScoped)
		}
	}
	collect("github", "comment:1", 1, 0)
	f.policy(t, path[0], externalRealtime())
	collect("mirror", "comment:1", 1, 0) // legacy already owns this provider event
	collect("github", "comment:2", 2, 1)
	f.policy(t, path[0], w.ExternalPolicy{Mode: new(w.ExternalForwardMode("off"))})
	collect("github", "comment:3", 2, 1) // explicit off also owns the routing decision
	f.policy(t, path[0], w.ExternalPolicy{})
	collect("another", "comment:2", 2, 1) // rollback does not send an already scoped event via legacy
	collect("github", "comment:4", 3, 1)
	if !f.inboxTodoForwardOn(t.Context()) {
		t.Fatal("cutover or rollback changed the legacy setting")
	}
}

func TestExternalInboxLarkTopicKeepsNativeOwnerMessageAndRoutesNewUpdatesOnce(t *testing.T) {
	needsLarkLayer(t)
	f := newExternalRoutingFixture(t)
	target := f.todo(t, "todo_topic", "topic_agent")
	ref := w.ExternalRef{Provider: "lark", Container: externalNode("chat", testChat), Item: externalNode("thread", testThread)}
	thread := TodoThread{Channel: "feishu", ChatID: testChat, ThreadID: testThread}
	if _, err := f.bindThread(t.Context(), string(target.ID), thread, nil); err != nil {
		t.Fatal(err)
	}
	f.hub.mu.Lock()
	f.hub.hello.Owner = "ou_owner"
	f.hub.mu.Unlock()
	f.inbox.externalTriage = func(context.Context, externalTriageInput) ([]externalVerdict, error) {
		t.Fatal("the legacy projection and explicit binding duplicated the same candidate")
		return nil, nil
	}
	f.policy(t, ref, externalRealtime())
	f.bind(t, ref, target, w.ExternalPolicy{})
	collect := func(id, sender string) time.Time {
		return f.collect(t, w.InboxSource{ID: "larkcli", Things: []connector.InboxThing{{
			Thread: "larkcli:chat:" + testChat + ":thread:" + testThread, Last: id,
			Title: "Topic update", Preview: "Update from the topic", At: time.Now(),
			Tags: []connector.Tag{{Key: connector.TagSender, Value: sender}},
		}}})
	}
	now := collect("om_owner", "ou_owner")
	f.sweep(t, now)
	if len(f.sent()) != 0 || len(f.feed(t, target)) != 0 {
		t.Fatal("the owner's message reached the scoped Inbox delivery path")
	}
	feishuSay(t, f.Service, f.hub, &FeishuStatus{Ready: true, Enabled: true, Owner: "ou_owner",
		Features: FeishuFeatures{Main: true, Agents: true}}, topicEvent("om_owner", "Proceed with the work"), "")
	decisions, err := f.store.ListTodoDecisions(t.Context(), string(target.ID), true)
	if err != nil || len(decisions) != 1 || decisions[0].SourceMessage != "feishu-om_owner" {
		t.Fatalf("native topic decision: %+v, %v", decisions, err)
	}
	now = collect("om_update", "ou_other")
	f.sweep(t, now)
	if got := f.feed(t, target); len(got) != 1 {
		t.Fatalf("incoming updates should have one scoped delivery to the native to-do: %+v", got)
	}
}

func TestExternalInboxMostSpecificAndConservativeDefaults(t *testing.T) {
	f := newExternalRoutingFixture(t)
	a := f.todo(t, "todo_container", "")
	b := f.todo(t, "todo_item", "item_agent")
	container := externalPushRef()
	item := container
	item.Item = externalNode("item", "alert")
	// The binding without a forwarding mode is inert.
	f.bind(t, item, b, w.ExternalPolicy{})
	f.inbox.externalTriage = func(context.Context, externalTriageInput) ([]externalVerdict, error) {
		t.Fatal("a default-off or deterministic route called triage")
		return nil, nil
	}
	const source = "push"
	now := f.collect(t, w.InboxSource{ID: source, Things: []connector.InboxThing{externalPush("alert", "one", "New warning")}})
	f.sweep(t, now)
	if got := f.sent(); len(got) != 0 || len(f.feed(t, b)) != 0 {
		t.Fatalf("default-off forwarded: %v", got)
	}

	f.bind(t, container, a, w.ExternalPolicy{})
	f.policy(t, w.ExternalRef{Provider: "push"}, externalRealtime())
	f.policy(t, container, w.ExternalPolicy{PreFilter: &w.ExternalPreFilter{Keywords: []string{"warning"},
		Mentions: []string{"me"}, Senders: []string{"Reviewer"}}})
	now = f.collect(t, w.InboxSource{ID: source, Things: []connector.InboxThing{externalPush("alert", "two", "Warning for @megan")}})
	f.sweep(t, now)
	if len(f.sent()) != 0 {
		t.Fatal("mention filter matched a partial mention")
	}
	now = f.collect(t, w.InboxSource{ID: source, Extension: true, Things: []connector.InboxThing{externalPush("alert", "three", "Warning for @me")}})
	f.sweep(t, now)
	if got := f.sent(); !slices.Equal(got, []string{"item_agent"}) || len(f.feed(t, a)) != 0 || len(f.feed(t, b)) != 1 {
		t.Fatalf("most-specific route sent to %v", got)
	}
	for i, mode := range []w.ExternalForwardMode{"on_demand", "off"} {
		f.policy(t, item, w.ExternalPolicy{Mode: &mode})
		now = f.collect(t, w.InboxSource{ID: source, Things: []connector.InboxThing{externalPush("alert", string(mode), "Warning for @me")}})
		f.sweep(t, now)
		if len(f.sent()) != 1 || len(f.feed(t, a)) != 0 {
			t.Fatalf("case %d: item %s fell through to a parent", i, mode)
		}
	}
}

func TestExternalInboxGitHubRolesDedupAndRestart(t *testing.T) {
	f := newExternalRoutingFixture(t)
	delivery := f.todo(t, "todo_delivery", "delivery_agent")
	reference := f.todo(t, "todo_reference_work", "reference_agent")
	closed := f.todo(t, "todo_closed_delivery", "")
	if _, err := f.editTodo(t.Context(), string(closed.ID), db.TodoEdit{Done: new(true)}); err != nil {
		t.Fatal(err)
	}
	f.inbox.externalTriage = func(context.Context, externalTriageInput) ([]externalVerdict, error) {
		t.Fatal("PR or issue with one open delivery task called triage")
		return nil, nil
	}
	for _, suffix := range []string{"/pull/17", "/issues/17"} {
		link := "https://github.com/example/repo" + suffix
		for _, target := range []externalTarget{delivery, reference, closed} {
			role := "delivery"
			if target == reference {
				role = "reference"
			}
			if _, _, err := f.store.ImportTodoRepositoryLinkDetails(t.Context(), string(target.ID),
				[]repository.LinkInput{{URL: link, Role: role}}, "explicit"); err != nil {
				t.Fatal(err)
			}
		}
		thing := connector.InboxThing{Thread: "example/repo#17" + suffix, Last: "comment:1", URL: link, Title: "Review", Preview: "Review feedback", At: time.Now()}
		path, err := externalbindingadapt.Path(githubExternalRef(thing))
		if err != nil {
			t.Fatal(err)
		}
		ref := path[0]
		// An explicit owner overlay cannot promote a projected reference.
		f.bind(t, ref, reference, externalRealtime())
		f.policy(t, ref, externalRealtime())
		if suffix == "/pull/17" {
			f.bind(t, w.ExternalRef{Provider: "github", Container: ref.Container}, delivery, externalRealtime())
		}
		now := f.collect(t, w.InboxSource{ID: "github", Things: []connector.InboxThing{thing}},
			w.InboxSource{ID: "mirror", Via: "github", Things: []connector.InboxThing{thing}})
		f.sweep(t, now)
	}

	// A fresh manager reads the same rows and receives the same provider event through a third Inbox source.
	next := New(Options{Hub: f.hub, Store: func() Store { return f.store }, Logf: testLogf(t)})
	f.Service = next
	now := f.collect(t, w.InboxSource{ID: "third", Via: "github", Things: []connector.InboxThing{{
		Thread: "example/repo#17", Last: "comment:1", URL: "https://github.com/example/repo/pull/17",
		Title: "Review", Preview: "Cross-source copy", At: time.Now(),
	}}})
	f.sweep(t, now)
	if got := f.sent(); !slices.Equal(got, []string{"delivery_agent", "delivery_agent"}) {
		t.Fatalf("delivery owner should get one update per object; sent to %v", got)
	}
	fyiCount, ownerCount, closedCount := len(f.feed(t, reference)), len(f.feed(t, delivery)), len(f.feed(t, closed))
	if fyiCount != 2 || ownerCount != 2 || closedCount != 0 {
		t.Fatalf("FYI = %d, owner delivery = %d, closed = %d", fyiCount, ownerCount, closedCount)
	}
}

func TestExternalInboxTriageCoalescesWithLiveOwner(t *testing.T) {
	f := newExternalRoutingFixture(t)
	a := f.todo(t, "todo_first", "first_agent")
	b := f.todo(t, "todo_second", "second_agent")
	fyi := f.todo(t, "todo_fyi", "fyi_agent")
	scope := externalPushRef()
	f.bind(t, scope, a, externalRealtime())
	f.bind(t, scope, b, externalRealtime())
	f.bind(t, scope, fyi, w.ExternalPolicy{Mode: new(w.ExternalForwardMode("realtime"))})

	calls := 0
	f.inbox.externalTriage = func(_ context.Context, input externalTriageInput) ([]externalVerdict, error) {
		calls++
		raw, err := json.Marshal(input)
		if err != nil || len(input.Candidates) != 3 || len(input.Summaries) != 1 ||
			strings.Contains(string(raw), "Private historical context") || strings.Contains(string(raw), "external_ref") {
			t.Fatalf("non-compact triage input: %s (%v)", raw, err)
		}
		for _, c := range input.Candidates {
			if c.ID == "" || c.Kind != "todo" || c.Title == "" || c.Scope != "A compact scope" || c.Status != "open" {
				t.Fatalf("invalid candidate: %+v", c)
			}
		}
		target := a
		if calls > 1 {
			target = b
		}
		return []externalVerdict{{Disposition: "assign", Owner: &target, FYI: []externalTarget{fyi, fyi}}}, nil
	}
	for _, cursor := range []string{"first", "second"} {
		now := f.collect(t, w.InboxSource{ID: "push", Things: []connector.InboxThing{externalPush("alerts", cursor, "Build failed")}})
		f.sweep(t, now)
	}
	if got := f.sent(); calls != 2 || !slices.Equal(got, []string{"first_agent", "first_agent"}) {
		t.Fatalf("calls = %d; two different events must use the same live owner, got %v", calls, got)
	}
	if len(f.feed(t, b)) != 0 || len(f.feed(t, fyi)) != 2 {
		t.Fatal("second owner received a dispatch, or the read-only FYI was duplicated or lost")
	}

	// A model attempting to make an FYI-only target the owner cannot authorize an action.
	f.inbox.externalTriage = func(context.Context, externalTriageInput) ([]externalVerdict, error) {
		return []externalVerdict{{Disposition: "assign", Owner: &fyi}}, nil
	}
	now := f.collect(t, w.InboxSource{ID: "push", Things: []connector.InboxThing{externalPush("different", "third", "Build failed")}})
	if err := f.sweepExternalInbox(t.Context(), now); err == nil || len(f.sent()) != 2 {
		t.Fatalf("invalid triage verdict reached an Agent: %v, %v", err, f.sent())
	}
}

func TestExternalInboxDigestNewWorkAndChatter(t *testing.T) {
	f := newExternalRoutingFixture(t)
	scope := externalPushRef()
	direction := externalTarget{ID: "dir_source", Kind: "direction"}
	todo := externalTarget{ID: "todo_source", Kind: "todo"}
	f.bind(t, scope, direction, w.ExternalPolicy{})
	f.bind(t, scope, todo, w.ExternalPolicy{})
	f.policy(t, scope, w.ExternalPolicy{Mode: new(w.ExternalForwardMode("digest")), DigestIntervalMinutes: new(60),
		ActionRole: new(w.ExternalActionRole("owner"))})
	calls := 0
	f.inbox.externalTriage = func(_ context.Context, input externalTriageInput) ([]externalVerdict, error) {
		calls++
		var out []externalVerdict
		for _, summary := range input.Summaries {
			if strings.Contains(summary, "chatter") {
				out = append(out, externalVerdict{Disposition: "ignore"})
			} else {
				out = append(out, externalVerdict{Disposition: "new"})
			}
		}
		if len(input.Candidates) != 2 || calls == 1 && len(out) != 2 || calls == 2 && len(out) != 1 {
			t.Fatalf("unexpected digest input: %+v", input)
		}
		return out, nil
	}
	now := f.collect(t, w.InboxSource{ID: "push", Things: []connector.InboxThing{
		externalPush("new request", "one", "A new request"), externalPush("chat", "two", "Unrelated chatter"),
	}})
	work, err := f.store.ReadyExternalInboxEvents(t.Context(), now, 10)
	if err != nil || len(work) != 2 {
		t.Fatalf("awaiting digest: %v %v", work, err)
	}
	cutoff := work[0].ReceivedAt.Truncate(time.Hour).Add(time.Hour)
	f.sweep(t, cutoff.Add(-time.Microsecond))
	if calls != 0 {
		t.Fatal("digest triage ran before its interval")
	}
	f.sweep(t, cutoff)
	if calls != 1 || len(f.routingTodos(t)) != 1 {
		t.Fatalf("calls = %d, default new policy created a to-do", calls)
	}
	f.inbox.mu.Lock()
	var ask *InboxItem
	for _, it := range f.inbox.items {
		if it.Suggestion != nil {
			if ask != nil {
				f.inbox.mu.Unlock()
				t.Fatal("chatter also produced a confirmation")
			}
			ask = it
		}
	}
	if ask == nil || ask.nowKey() != NowConfirmTodo {
		f.inbox.mu.Unlock()
		t.Fatal("new work did not produce an Inbox confirmation")
	}
	id, version := ask.ID, ask.Suggestion.Version
	f.inbox.mu.Unlock()
	if _, err := f.actOnSuggestion(t.Context(), id, suggestionReq{Action: suggestConfirm, Version: version}, true); err != nil {
		t.Fatalf("confirm without the Inbox twin: %v", err)
	}

	f.policy(t, scope, w.ExternalPolicy{Mode: new(w.ExternalForwardMode("digest")), DigestIntervalMinutes: new(60),
		ActionRole: new(w.ExternalActionRole("owner")), NewItemPolicy: new(w.ExternalNewItemPolicy("auto_todo"))})
	now = f.collect(t, w.InboxSource{ID: "push", Things: []connector.InboxThing{externalPush("trusted alert", "three", "Action is required")}})
	f.sweep(t, now.Add(time.Hour))
	f.sweep(t, now.Add(2*time.Hour))
	todos := f.routingTodos(t)
	if calls != 2 || len(todos) != 3 || !slices.ContainsFunc(todos, func(t db.TodoRow) bool {
		return t.Title == "Update trusted alert" && t.DirectionID == "dir_source"
	}) || len(f.sent()) > 0 {
		t.Fatalf("opt-in must create exactly one task in the direction, calls = %d, tasks = %+v", calls, todos)
	}
}

func TestExternalInboxWorkerFindsAnItemCommittedByAnotherManager(t *testing.T) {
	f := newExternalRoutingFixture(t)
	target := f.todo(t, "todo_delivery", "delivery_agent")
	f.bind(t, externalPushRef(), target, externalRealtime())
	next := New(Options{Hub: f.hub, Store: func() Store { return f.store }, Logf: testLogf(t)})
	if err := next.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	thing := externalPush("alert", "one", "A source update")
	now := f.collect(t, w.InboxSource{ID: "push", Things: []connector.InboxThing{thing}})
	if err := next.sweepExternalInbox(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	f.sweep(t, now)
	if got := f.sent(); !slices.Equal(got, []string{"delivery_agent"}) {
		t.Fatalf("a warm manager must recover a committed Inbox row and deliver it once, got %v", got)
	}
}

func TestExternalInboxDirectionReusesTheSupervisorConversation(t *testing.T) {
	f := newExternalRoutingFixture(t)
	target := externalTarget{Kind: "direction", ID: "dir_source"}
	f.bind(t, externalPushRef(), target, externalRealtime())
	f.hub.mu.Lock()
	f.hub.sessions["server"] = []proto.Session{{SessionID: "dir_source", Assistant: true, Status: proto.StatusWorking}}
	f.hub.mu.Unlock()
	now := f.collect(t, w.InboxSource{ID: "push", Things: []connector.InboxThing{
		externalPush("alert", "one", "This goes to the direction"),
	}})
	f.sweep(t, now)
	queued, ok := f.queuedRelays()
	if !ok || len(queued["dir_source"]) != 1 || len(f.sent()) != 0 || len(f.feed(t, target)) != 1 {
		t.Fatalf("direction update did not append once to its supervisor: %+v", queued)
	}
}

func TestExternalInboxGroupedEventsWithASharedCursor(t *testing.T) {
	f := newExternalRoutingFixture(t)
	todo := f.todo(t, "todo_delivery", "delivery_agent")
	f.bind(t, externalPushRef(), todo, externalRealtime())
	f.collect(t)
	source := w.InboxSource{ID: "push", Group: true}
	batch := externalPush("group", "shared_cursor", "A grouped message")
	batch.Group = &connector.ThingGroup{Batch: "batch", New: []string{"one", "two"}}
	f.writeGroup(t.Context(), source, batch, &groupPlan{events: []plannedEvent{
		{messages: []string{"one"}, title: "First request"},
		{messages: []string{"two"}, title: "Second request"},
	}})
	f.sweep(t, time.Now())
	if got := f.sent(); !slices.Equal(got, []string{"delivery_agent", "delivery_agent"}) {
		t.Fatalf("two source events with a shared Inbox cursor went to %v", got)
	}
}
