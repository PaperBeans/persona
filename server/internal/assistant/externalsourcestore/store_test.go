package externalsourcestore_test

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding/externalbindingadapt"
	"github.com/nwverse/persona/server/internal/assistant/externalsourcestore"
	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/assistant/repositorystore"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type ref = assistantwire.ExternalRef
type node = assistantwire.ExternalRefNode

func open(t *testing.T) *repositorystore.Store {
	t.Helper()
	dsn := os.Getenv(dbtest.Env)
	if dsn == "" {
		t.Skip(dbtest.Env + " not set")
	}
	database, err := db.Open(t.Context(), dsn, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	return repositorystore.New(database)
}

func createTodo(t *testing.T, d *repositorystore.Store, id string) {
	t.Helper()
	now := time.Now()
	if err := d.CreateTodo(t.Context(), db.TodoRow{ID: id, Title: "Fixture", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func createBinding(t *testing.T, d *externalsourcestore.Store, in assistantwire.ExternalBindingInput) assistantwire.ExternalBinding {
	t.Helper()
	saved, err := d.CreateExternalBinding(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func lookup(t *testing.T, d *externalsourcestore.Store, r ref) assistantwire.ExternalBindingLookup {
	t.Helper()
	path, err := externalbindingadapt.Path(r)
	if err != nil {
		t.Fatal(err)
	}
	found, err := d.LookupExternalBindings(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestExternalBindingManyToManyPersistenceAndDeletion(t *testing.T) {
	d := open(t)
	ctx := t.Context()
	const todo = "binding_todo"
	createTodo(t, d, todo)
	group := ref{Provider: "lark", Container: node{Kind: "chat", ID: "group"}}
	topic := ref{Provider: "lark", Container: group.Container, Item: node{Kind: "thread", ID: "topic"}}
	mail := ref{Provider: "mail", Channel: "fixture-mail", Item: node{Kind: "thread", ID: "thread"}}
	bound := createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: group, TargetKind: "todo", TargetID: todo})
	createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: group, TargetKind: "direction", TargetID: "direction_group"})
	createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: topic, TargetKind: "direction", TargetID: "direction_topic"})
	createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: mail, TargetKind: "todo", TargetID: todo})
	createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: ref{Provider: "lark"}, TargetKind: "direction", TargetID: "direction_channel"})

	if _, err := d.CreateExternalBinding(ctx, bound.ExternalBindingInput); !errors.Is(err, externalsourcestore.ErrBindingExists) {
		t.Fatalf("duplicate binding: %v", err)
	}
	if _, err := d.CreateExternalBinding(ctx, assistantwire.ExternalBindingInput{ExternalRef: topic, TargetKind: "todo", TargetID: "missing"}); !errors.Is(err, db.ErrTodoNotFound) {
		t.Fatalf("binding to a missing to-do: %v", err)
	}

	next := open(t)
	got := lookup(t, next.Store, topic)
	if len(got.Matches) != 4 || got.Matches[0].TargetID != "direction_topic" || got.Matches[1].TargetID != "direction_group" ||
		got.Matches[2].TargetID != todo || got.Matches[3].TargetID != "direction_channel" || got.Matches[2].BindingID != bound.ID {
		t.Fatalf("many-to-many lookup order: %+v", got)
	}
	rows, err := next.ListExternalBindings(ctx, "todo", todo, "mail", nil)
	if err != nil || len(rows) != 1 || rows[0].ExternalRef != mail {
		t.Fatalf("combined filters: %+v, %v", rows, err)
	}
	rows, err = next.ListExternalBindings(ctx, "", "", "", &group)
	if err != nil || len(rows) != 2 {
		t.Fatalf("reference filter must be exact: %+v, %v", rows, err)
	}
	changed := bound.ExternalBindingInput
	changed.ExternalRef = topic
	changed.Policy.Mode = new(assistantwire.ExternalForwardMode("realtime"))
	updated, err := next.UpdateExternalBinding(ctx, bound.ID, changed)
	if err != nil || updated.ID != bound.ID || !reflect.DeepEqual(updated.ExternalBindingInput, changed) {
		t.Fatalf("update: %+v, %v", updated, err)
	}
	if err := next.DeleteExternalBinding(ctx, updated.ID); err != nil {
		t.Fatal(err)
	}
	if err := next.DeleteExternalBinding(ctx, updated.ID); !errors.Is(err, externalsourcestore.ErrBindingNotFound) {
		t.Fatalf("deleting absent binding: %v", err)
	}
	if err := next.DeleteTodo(ctx, todo); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, next.Store, mail); len(got.Matches) != 0 || got.Matches == nil {
		t.Fatalf("deleted to-do has a binding: %+v", got)
	}
}

func TestExternalLookupProjectsLegacyLarkTopicWithoutCopyingIt(t *testing.T) {
	d := open(t)
	ctx := t.Context()
	const todo = "legacy_topic_todo"
	createTodo(t, d, todo)
	group := ref{Provider: "lark", Container: node{Kind: "chat", ID: "oc_example"}}
	topic := ref{Provider: "lark", Container: group.Container, Item: node{Kind: "thread", ID: "omt_example"}}
	thread := db.TodoThread{Channel: "feishu", ChatID: "oc_example", ThreadID: "omt_example"}
	if err := d.SetTodoThread(ctx, todo, thread, time.Now()); err != nil {
		t.Fatal(err)
	}
	exact := func(got assistantwire.ExternalBindingLookup) []assistantwire.ExternalBindingMatch {
		return slices.DeleteFunc(got.Matches, func(m assistantwire.ExternalBindingMatch) bool { return m.ExternalRef != topic })
	}
	explicit := createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: topic, TargetKind: "todo", TargetID: todo})
	for _, store := range []*externalsourcestore.Store{d.Store, open(t).Store} {
		got := exact(lookup(t, store, topic))
		if len(got) != 2 || got[0].BindingID != explicit.ID ||
			got[1].Source != "lark_topic" || got[1].TargetID != todo || got[1].BindingID != "" {
			t.Fatalf("legacy topic matches: %+v", got)
		}
		path, err := externalbindingadapt.Path(topic)
		if err != nil {
			t.Fatal(err)
		}
		if configured, err := store.ConfiguredExternalRefs(ctx, path); err != nil || len(configured) != 0 {
			t.Fatalf("a projection enabled forwarding: %+v, %v", configured, err)
		}
		if routes, err := store.ExternalRoutingConfiguration(ctx, path); err != nil || len(exact(routes.Lookup)) != 2 {
			t.Fatalf("routing lookup: %+v, %v", routes, err)
		}
	}
	other := topic
	other.Channel = "2"
	if got := lookup(t, d.Store, other); len(got.Matches) != 0 {
		t.Fatalf("legacy thread matched another account: %+v", got)
	}
	if err := d.DeleteExternalBinding(ctx, explicit.ID); err != nil {
		t.Fatal(err)
	}
	if got := exact(lookup(t, d.Store, topic)); len(got) != 1 || got[0].TargetID != todo {
		t.Fatalf("removing an explicit link removed the legacy binding: %+v", got)
	}
	if _, err := d.ClearTodoThread(ctx, todo, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := exact(lookup(t, d.Store, topic)); len(got) != 0 {
		t.Fatalf("thread projection outlived the native link: %+v", got)
	}
}

func TestExternalScopedPoliciesPersistAndReset(t *testing.T) {
	d := open(t)
	ctx := t.Context()
	channel := ref{Provider: "subscription", Channel: "fixture"}
	container := ref{Provider: channel.Provider, Channel: channel.Channel, Container: node{Kind: "source", ID: "feed"}}
	item := ref{Provider: channel.Provider, Channel: channel.Channel, Container: container.Container, Item: node{Kind: "item", ID: "post"}}
	id := createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: item, TargetKind: "direction", TargetID: "direction_policy",
		Policy: assistantwire.ExternalPolicy{ActionRole: new(assistantwire.ExternalActionRole("owner"))}}).ID
	for _, row := range []assistantwire.ExternalScopedPolicy{
		{ExternalRef: channel, Policy: assistantwire.ExternalPolicy{Mode: new(assistantwire.ExternalForwardMode("digest")), DigestIntervalMinutes: new(60)}},
		{ExternalRef: container, Policy: assistantwire.ExternalPolicy{DigestIntervalMinutes: new(20)}},
		{ExternalRef: item, Policy: assistantwire.ExternalPolicy{NewItemPolicy: new(assistantwire.ExternalNewItemPolicy("auto_todo"))}},
	} {
		if err := d.PutExternalSourcePolicy(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	next := open(t)
	resolve := func() assistantwire.ExternalEffectivePolicy {
		t.Helper()
		rows, binding, err := next.ExternalSourcePolicies(ctx, []ref{item, container, channel}, id)
		if err != nil || binding == nil {
			t.Fatalf("read policy and binding: %+v, %v", rows, err)
		}
		resolved, err := externalbindingadapt.Resolve(item, rows, binding)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	result := resolve()
	if *result.Policy.DigestIntervalMinutes != 20 || *result.Policy.NewItemPolicy != "auto_todo" ||
		result.Provenance.Mode.Source != "channel" || result.Provenance.DigestIntervalMinutes.Source != "container" ||
		result.Provenance.NewItemPolicy.Source != "item" || result.Provenance.ActionRole.BindingID != id {
		t.Fatalf("incorrect inherited policy: %+v", result)
	}
	if err := next.PutExternalSourcePolicy(ctx, assistantwire.ExternalScopedPolicy{ExternalRef: container}); err != nil {
		t.Fatal(err)
	}
	if result = resolve(); *result.Policy.DigestIntervalMinutes != 60 || result.Provenance.DigestIntervalMinutes.Source != "channel" {
		t.Fatalf("reset failed to restore inheritance: %+v", result)
	}
}

func TestExternalLookupProjectsExistingRepositoryLinksAndLiveEdits(t *testing.T) {
	d := open(t)
	ctx := t.Context()
	const (
		delivery   = "projection_delivery"
		referenced = "projection_reference"
		enterprise = "projection_enterprise"
		url        = "https://github.com/example/web/pull/17"
		issue      = "https://github.com/example/web/issues/17"
	)
	for _, id := range []string{delivery, referenced, enterprise} {
		createTodo(t, d, id)
	}
	seed, _, err := d.ImportTodoRepositoryLinks(ctx, delivery, []string{url, issue}, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.ImportTodoRepositoryLinkDetails(ctx, referenced, []repository.LinkInput{{URL: url, Role: "reference"}}, "explicit"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.ImportTodoRepositoryLinks(ctx, enterprise, []string{"https://git.example.com/example/web/pull/17"}, "explicit"); err != nil {
		t.Fatal(err)
	}
	github := ref{Provider: "github", Container: node{Kind: "repository", ID: "example/web"}}
	pr := ref{Provider: "github", Container: github.Container, Item: node{Kind: "pull_request", ID: "17"}}
	explicit := createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: pr, TargetKind: "todo", TargetID: delivery})
	repoBinding := createBinding(t, d.Store, assistantwire.ExternalBindingInput{ExternalRef: github, TargetKind: "direction", TargetID: "direction_repo"})

	got := lookup(t, d.Store, ref{Provider: "github", Channel: "http://www.github.com", Container: node{Kind: "REPOSITORY", ID: "Example/Web.git"}, Item: pr.Item})
	want := []assistantwire.ExternalBindingMatch{
		{ExternalRef: pr, TargetKind: "todo", TargetID: delivery, Source: "explicit", BindingID: explicit.ID},
		{ExternalRef: pr, TargetKind: "todo", TargetID: delivery, Source: "repository_link", RepositoryRole: "delivery"},
		{ExternalRef: pr, TargetKind: "todo", TargetID: referenced, Source: "repository_link", RepositoryRole: "reference"},
		{ExternalRef: github, TargetKind: "direction", TargetID: "direction_repo", Source: "explicit", BindingID: repoBinding.ID},
	}
	if !reflect.DeepEqual(got.Matches, want) || got.Path[0] != pr {
		t.Fatalf("projected and explicit lookup: %+v, want %+v", got, want)
	}
	if after, err := d.TodoRepositories(ctx, delivery); err != nil || !reflect.DeepEqual(seed, after) {
		t.Fatalf("projection changed repository state: %+v, %v", after, err)
	}
	issueRef := pr
	issueRef.Item.Kind = "issue"
	if matches := lookup(t, d.Store, issueRef).Matches; len(matches) != 2 || matches[0].TargetID != delivery || matches[0].RepositoryRole != "delivery" {
		t.Fatalf("issue and pull request collided: %+v", matches)
	}
	if _, err := d.SetTodoRepositoryLinkRole(ctx, delivery, url, "reference"); err != nil {
		t.Fatal(err)
	}
	if matches := lookup(t, open(t).Store, pr).Matches; len(matches) != 4 || matches[1].RepositoryRole != "reference" {
		t.Fatalf("stale role after restart: %+v", matches)
	}
	current, err := d.TodoRepositories(ctx, delivery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetTodoRepositoryLinks(ctx, delivery, current.Repositories[0].RepoID, repository.LinksInput{
		ExpectedRevision: &current.Revision, Links: []repository.LinkInput{{URL: issue}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteTodo(ctx, referenced); err != nil {
		t.Fatal(err)
	}
	if matches := lookup(t, d.Store, pr).Matches; len(matches) != 2 || matches[0].Source != "explicit" || matches[1].TargetID != "direction_repo" {
		t.Fatalf("removed links stayed in projection: %+v", matches)
	}
	summary, err := d.ExternalSourceSummary(ctx, "github", new(assistantwire.ExternalChannelID("")))
	if err != nil || len(summary.Bindings) != 3 {
		t.Fatalf("summary for the public GitHub channel: %+v, %v", summary, err)
	}
	summary, err = d.ExternalSourceSummary(ctx, "github", new(assistantwire.ExternalChannelID("https://git.example.com")))
	if err != nil || len(summary.Bindings) != 1 || summary.Bindings[0].TargetID != enterprise {
		t.Fatalf("summary for the enterprise GitHub channel: %+v, %v", summary, err)
	}
}
