package connector

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"
)

// runs counts how often a connector's Run started and ended.
type runs struct {
	fake
	started, ended chan struct{}
}

func (r *runs) Run(ctx context.Context) {
	r.started <- struct{}{}
	<-ctx.Done()
	r.ended <- struct{}{}
}

func waitOn(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)
	}
}

func TestStopWaitsForAReplacedWatcher(t *testing.T) {
	r := NewRegistry(nil)
	oldStarted, oldStopping, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(release) })
	defer finish()
	old := &fake{id: "example", watch: func(ctx context.Context, _ func(Scope)) error {
		close(oldStarted)
		<-ctx.Done()
		close(oldStopping)
		<-release
		return nil
	}}
	if err := r.Add(old); err != nil {
		t.Fatal(err)
	}
	r.Start(t.Context(), func(string, Scope) {}, t.Logf)
	waitOn(t, oldStarted, "first watcher")
	r.Remove(old.ID())
	waitOn(t, oldStopping, "retired watcher's cancellation")
	replacement := &runs{fake: fake{id: old.ID(), watch: func(ctx context.Context, _ func(Scope)) error {
		<-ctx.Done()
		return nil
	}}, started: make(chan struct{}, 1), ended: make(chan struct{}, 1)}
	if err := r.Add(replacement); err != nil {
		t.Fatal(err)
	}
	waitOn(t, replacement.started, "replacement runner")
	stopped := make(chan struct{})
	go func() { r.Stop(); close(stopped) }()
	waitOn(t, replacement.ended, "replacement runner's cancellation")
	select {
	case <-stopped:
		t.Fatal("Stop returned while the retired watcher still held the fixture")
	default:
	}
	finish()
	waitOn(t, stopped, "all workers to exit")
	r.Start(t.Context(), func(string, Scope) {}, t.Logf)
	waitOn(t, replacement.started, "restart of the enabled runner")
	r.Stop()
	waitOn(t, replacement.ended, "restarted runner to stop")
}

// Switched off, a connector is "disabled" to every pipe (no tools, its tools refuse), stops running, and lists as
// not enabled; switched on, it is itself again and starts again.
func TestSetEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &runs{fake: fake{id: "example", status: Status{State: StateOK}, watch: func(ctx context.Context, _ func(Scope)) error {
		<-ctx.Done()
		return nil
	}}, started: make(chan struct{}, 4), ended: make(chan struct{}, 4)}
	r := NewRegistry(nil)
	_ = r.Add(c)
	r.Start(ctx, func(string, Scope) {}, func(string, ...any) {})
	waitOn(t, c.started, "first start")

	if err := r.SetEnabled("example", false); err != nil {
		t.Fatal(err)
	}
	waitOn(t, c.ended, "stop when switched off")
	if st := StatusOf(ctx, c); st.State != StateDisabled {
		t.Fatalf("status %+v", st)
	}
	if len(r.Tools(ctx)) != 0 {
		t.Fatal("a disabled connector offers tools")
	}
	if res := r.Call(ctx, "example_read", json.RawMessage(`{}`), nil); !res.IsError {
		t.Fatal("a disabled connector's tool ran")
	}
	if infos := r.Infos(ctx); infos[0].Enabled || infos[0].Status != StateDisabled {
		t.Fatalf("infos %+v", infos)
	}

	if err := r.SetEnabled("example", true); err != nil {
		t.Fatal(err)
	}
	waitOn(t, c.started, "start when switched on")
	if st := StatusOf(ctx, c); st.State != StateOK || len(r.Tools(ctx)) == 0 || !r.Infos(ctx)[0].Enabled {
		t.Fatalf("back on: %+v", st)
	}
	_ = r.SetEnabled("example", true) // on twice: still one Run
	select {
	case <-c.started:
		t.Fatal("started twice")
	case <-time.After(50 * time.Millisecond):
	}
	if err := r.SetEnabled("nobody", false); err == nil {
		t.Fatal("switched off a connector that is not there")
	}
}

// Switched off before Start (the hub restores the user's choice first), it never runs.
func TestDisabledBeforeStartDoesNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &runs{fake: fake{id: "example", watch: func(ctx context.Context, _ func(Scope)) error { <-ctx.Done(); return nil }},
		started: make(chan struct{}, 4), ended: make(chan struct{}, 4)}
	r := NewRegistry(nil)
	_ = r.Add(c)
	_ = r.SetEnabled("example", false)
	r.Start(ctx, func(string, Scope) {}, func(string, ...any) {})
	select {
	case <-c.started:
		t.Fatal("a disabled connector ran")
	case <-time.After(50 * time.Millisecond):
	}
}

// signIn is a connector with Login; proxy implements every capability and declares only some (a program's).
type signIn struct{ plain }

func (signIn) Manifest() Manifest { return Manifest{Capabilities: []Capability{CapLogin}} }
func (signIn) LoginStep(context.Context, string, map[string]string) (Prompt, error) {
	return Prompt{}, nil
}
func (signIn) Logout(context.Context) error { return nil }

type proxy struct {
	fake
	declared []Capability
}

func (p *proxy) Manifest() Manifest { return Manifest{Capabilities: p.declared, Proxy: true} }

func TestCapabilitiesGoByTheManifest(t *testing.T) {
	if got := Capabilities(signIn{}); !slices.Equal(got, []string{"login"}) {
		t.Fatalf("login: %v", got)
	}
	// It implements tools and actions, but declares only a status: the list shown, the tools offered and As go by
	// what it declares.
	p := &proxy{fake: fake{id: "example", status: Status{State: StateOK}}, declared: []Capability{CapStatus}}
	if got := Capabilities(p); !slices.Equal(got, []string{"status"}) {
		t.Fatalf("proxy: %v", got)
	}
	if _, ok := As[ReadTools](p); ok || len(ToolNames(p)) != 0 {
		t.Fatalf("an undeclared capability was used: %v", ToolNames(p))
	}
	if err := Check(p); err != nil {
		t.Fatalf("a proxy declaring less than it implements: %v", err)
	}
	// Not declared, a proxy's status is still its own (it answers for a program that does not run).
	p.declared, p.status = nil, Status{State: StateError, Error: "进程还没启动"}
	if st := StatusOf(context.Background(), p); st.State != StateError {
		t.Fatalf("proxy status: %+v", st)
	}
}

// odd declares what it pleases over fake's capabilities (status, tools, actions, events).
type odd struct {
	fake
	m Manifest
}

func (o *odd) Manifest() Manifest { return o.m }

func TestRegistryChecksTheManifest(t *testing.T) {
	all := []Capability{CapStatus, CapTools, CapActions, CapEvents}
	for name, m := range map[string]Manifest{
		"declared, not implemented": {Capabilities: append(slices.Clone(all), CapInbox)},
		"implemented, not declared": {Capabilities: all[:3]},
		"unknown":                   {Capabilities: append(slices.Clone(all), "telepathy")},
		"twice":                     {Capabilities: append(slices.Clone(all), CapTools)},
		"kinds without an inbox":    {Capabilities: all, Kinds: []string{"dm"}},
	} {
		if err := NewRegistry(nil).Add(&odd{fake: fake{id: "example"}, m: m}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := NewRegistry(nil).Add(&odd{fake: fake{id: "example"}, m: Manifest{Capabilities: all}}); err != nil {
		t.Fatal(err)
	}
}

func TestManifestForAgents(t *testing.T) {
	m := Manifest{AgentTools: []string{"mail_*", "x_read"}}
	for name, want := range map[string]bool{"mail_send": true, "mail_read": true, "x_read": true, "x_write": false, "lark_read": false} {
		if m.ForAgents(name) != want {
			t.Errorf("%s: %v", name, !want)
		}
	}
}

func TestCheckKinds(t *testing.T) {
	describe := func(kind string) string {
		switch kind {
		case "mention":
			return "@ 了你"
		case "review":
			return "请你 review"
		}
		return "私聊" // the template's else
	}
	if err := CheckKinds([]string{"dm", "mention", "review"}, describe); err != nil {
		t.Fatal(err)
	}
	// "group" is not in the template: it reads like "dm", the fallback.
	if err := CheckKinds([]string{"dm", "mention", "group"}, describe); err == nil {
		t.Fatal("an undescribed kind passed")
	}
}
