package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

type fakeHerdrSessions struct {
	blocked  map[string][]string
	detected map[string][]proto.HerdrNamedSession
}

func (f *fakeHerdrSessions) HerdrSessions(_ context.Context, id string) (proto.HerdrSessionsState, error) {
	b := slices.Clone(f.blocked[id])
	if b == nil {
		b = []string{}
	}
	var sessions []proto.HerdrNamedSession
	for _, s := range f.detected[id] {
		s.Blocked = slices.Contains(b, s.Name)
		sessions = append(sessions, s)
	}
	return proto.HerdrSessionsState{Sessions: sessions, Blocked: b, Live: true}, nil
}

func (f *fakeHerdrSessions) EditBlockedHerdrSessions(_ context.Context, id string, edit func([]string) ([]string, error)) ([]string, error) {
	next, err := edit(slices.Clone(f.blocked[id]))
	if err != nil {
		return nil, err
	}
	next, err = proto.ValidateBlockedHerdrSessions(next)
	if err != nil {
		return nil, err
	}
	if f.blocked == nil {
		f.blocked = map[string][]string{}
	}
	f.blocked[id] = next
	return next, nil
}

func TestBlockedHerdrSessionsSetting(t *testing.T) {
	ft := newFleetTest(t)
	hs := &fakeHerdrSessions{
		detected: map[string][]proto.HerdrNamedSession{
			"mac": {{Name: "agent", Running: true}, {Name: "review", Running: false}},
		},
	}
	ft.s.herdrSessions = hs

	approve := func() {
		go func() {
			q, ok := awaitCard(ft.s)
			if !ok {
				t.Error("missing phone confirmation")
				return
			}
			yes := true
			raw, _ := json.Marshal(proto.Resolution{Allow: &yes})
			_, _ = ft.s.Action(context.Background(), Action{Op: "answer", InteractionID: q.ID, Answer: raw})
		}()
	}

	// get shows none blocked and detected sessions.
	got := settingsTool(ft.s, `{"action":"get","key":"agents.blocked_herdr_sessions"}`)
	if got.IsError || !strings.Contains(got.Content[0].Text, "agent (running, listed)") {
		t.Fatalf("get = %+v", got)
	}

	// add requires approval and blocks "agent".
	approve()
	res := settingsTool(ft.s, `{"action":"add","key":"agents.blocked_herdr_sessions","value":{"machine":"mac","session":"agent"}}`)
	if res.IsError || !slices.Equal(hs.blocked["mac"], []string{"agent"}) {
		t.Fatalf("add = %+v, blocked=%v", res, hs.blocked["mac"])
	}

	// Duplicate add is a no-op without error.
	if dup := settingsTool(ft.s, `{"action":"add","key":"agents.blocked_herdr_sessions","value":{"machine":"mac","session":"agent"}}`); dup.IsError || !strings.Contains(dup.Content[0].Text, "already") {
		t.Fatalf("dup add = %+v", dup)
	}

	// remove unblocks "agent".
	approve()
	rem := settingsTool(ft.s, `{"action":"remove","key":"agents.blocked_herdr_sessions","id":"mac:agent"}`)
	if rem.IsError || len(hs.blocked["mac"]) != 0 {
		t.Fatalf("remove = %+v, blocked=%v", rem, hs.blocked["mac"])
	}

	// Invalid inputs fail cleanly.
	for _, bad := range []string{
		`{"action":"add","key":"agents.blocked_herdr_sessions","value":{"machine":"mac","session":"default"}}`,
		`{"action":"add","key":"agents.blocked_herdr_sessions","value":{"machine":"mac","session":"persona-abc"}}`,
		`{"action":"add","key":"agents.blocked_herdr_sessions","value":{"machine":"nope","session":"agent"}}`,
		`{"action":"remove","key":"agents.blocked_herdr_sessions","id":"mac:agent"}`,
	} {
		if r := settingsTool(ft.s, bad); !r.IsError {
			t.Fatalf("bad call %s succeeded: %+v", bad, r)
		}
	}
}
