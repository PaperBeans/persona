package assistant

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func paceAt(now time.Time, label string, minutes int, elapsed time.Duration, used float64, model string) proto.UsageWindow {
	reset := now.Add(time.Duration(minutes)*time.Minute - elapsed)
	return proto.UsageWindow{Label: label, WindowMinutes: minutes, UsedPercent: used, ResetsAt: &reset, Model: model}
}

func TestHeadroomPace(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	short := func(elapsed time.Duration, used float64) proto.UsageWindow {
		return paceAt(now, "5h", 300, elapsed, used, "")
	}
	long := func(elapsed time.Duration, used float64) proto.UsageWindow {
		return paceAt(now, "7d", 10080, elapsed, used, "")
	}
	group := func(elapsed time.Duration, used float64) proto.UsageWindow {
		return paceAt(now, "5h", 300, elapsed, used, "Alpha and Beta models")
	}
	acct := func(id string, ws ...proto.UsageWindow) proto.UsageAccount {
		return proto.UsageAccount{ID: id, Provider: "claude", Windows: ws, SampledAt: now}
	}
	past := now.Add(-time.Minute)
	overStart := now.Add(-time.Hour) // the 5 h window below started an hour ago
	share := 30.0 / 85
	overRecovers := overStart.Add(time.Duration(share * float64(5*time.Hour)))
	cases := []struct {
		name      string
		accounts  []proto.UsageAccount
		provider  string
		model     string
		known     bool
		stale     bool
		h         float64
		recovers  *time.Time
		nAccounts int
	}{
		{name: "one hour into 5h: the elapsed share less the reserve", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 0))},
			provider: "claude", known: true, h: 0.17, nAccounts: 1},
		{name: "start share before it is reached", accounts: []proto.UsageAccount{acct("a", short(30*time.Minute, 10))},
			provider: "claude", known: true, h: 0.07, nAccounts: 1},
		{name: "weekly window past its start share", accounts: []proto.UsageAccount{acct("a", long(36*time.Hour, 5))},
			provider: "claude", known: true, h: (1.5/7*90 - 5) / 100, nAccounts: 1},
		{name: "weekly start share right after a reset", accounts: []proto.UsageAccount{acct("a", long(time.Hour, 0))},
			provider: "claude", known: true, h: 0.9 / 7, nAccounts: 1},
		{name: "an account is its tightest window", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 0), long(36*time.Hour, 5))},
			provider: "claude", known: true, h: (1.5/7*90 - 5) / 100, nAccounts: 1},
		{name: "over pace: no headroom, recovers when elapsed catches up", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 30))},
			provider: "claude", known: true, h: 0, recovers: &overRecovers, nAccounts: 1},
		{name: "over pace past the window's end recovers at the reset", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 90))},
			provider: "claude", known: true, h: 0, recovers: func() *time.Time { r := now.Add(4 * time.Hour); return &r }(), nAccounts: 1},
		{name: "accounts sum, one over pace adds nothing", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 30)), acct("b", short(time.Hour, 0))},
			provider: "claude", known: true, h: 0.17, nAccounts: 2},
		{name: "an account seen twice counts once", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 0)), acct("a", short(time.Hour, 0))},
			provider: "claude", known: true, h: 0.17, nAccounts: 1},
		{name: "stale is unknown, never full", accounts: []proto.UsageAccount{func() proto.UsageAccount { a := acct("a", short(time.Hour, 0)); a.Stale = true; return a }()},
			provider: "claude", stale: true},
		{name: "past stale_at is stale", accounts: []proto.UsageAccount{func() proto.UsageAccount { a := acct("a", short(time.Hour, 0)); a.StaleAt = &past; return a }()},
			provider: "claude", stale: true},
		{name: "a stale account next to a fresh one is left out", accounts: []proto.UsageAccount{func() proto.UsageAccount { a := acct("a", short(time.Hour, 0)); a.Stale = true; return a }(), acct("b", short(time.Hour, 7))},
			provider: "claude", known: true, h: 0.10, nAccounts: 1},
		{name: "no reading for the provider is unknown", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 0))}, provider: "codex"},
		{name: "unknown provider is unknown", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 0))}, provider: ""},
		{name: "a window without a reset time is unknown", accounts: []proto.UsageAccount{acct("a", proto.UsageWindow{Label: "5h", WindowMinutes: 300, UsedPercent: 1})}, provider: "claude"},
		{name: "a balance-only account is unknown", accounts: []proto.UsageAccount{{ID: "a", Provider: "claude", Balance: &proto.UsageBalance{Amount: "1.00", Currency: "USD"}}}, provider: "claude"},
		{name: "a per-model window applies to its group", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 0), group(time.Hour, 10))},
			provider: "claude", model: "alpha-large-2", known: true, h: 0.07, nAccounts: 1},
		{name: "a per-model window of another group does not", accounts: []proto.UsageAccount{acct("a", short(time.Hour, 0), group(time.Hour, 10))},
			provider: "claude", model: "gamma-1", known: true, h: 0.17, nAccounts: 1},
		{name: "only per-model windows and no model is unknown", accounts: []proto.UsageAccount{acct("a", group(time.Hour, 0))}, provider: "claude"},
		{name: "agy reads the antigravity accounts", accounts: []proto.UsageAccount{{ID: "g", Provider: "antigravity", Windows: []proto.UsageWindow{short(time.Hour, 0)}}},
			provider: "agy", known: true, h: 0.17, nAccounts: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := headroomOf(c.accounts, c.provider, c.model, now)
			if got.Known != c.known || got.Stale != c.stale || got.Accounts != c.nAccounts {
				t.Fatalf("known/stale/accounts = %v/%v/%d, want %v/%v/%d (%+v)", got.Known, got.Stale, got.Accounts, c.known, c.stale, c.nAccounts, got)
			}
			if math.Abs(got.H-c.h) > 1e-9 || math.Abs(got.Percent-c.h*100) > 1e-7 {
				t.Fatalf("H = %v (%v%%), want %v", got.H, got.Percent, c.h)
			}
			switch {
			case c.recovers == nil && got.RecoversAt != nil:
				t.Fatalf("recovers at %v, want none", got.RecoversAt)
			case c.recovers != nil && (got.RecoversAt == nil || got.RecoversAt.Sub(*c.recovers).Abs() > time.Second):
				t.Fatalf("recovers at %v, want %v", got.RecoversAt, c.recovers)
			}
		})
	}
}

func TestUsageWindowMinutes(t *testing.T) {
	for _, c := range []struct {
		w    proto.UsageWindow
		want int
	}{
		{proto.UsageWindow{Label: "5h"}, 300},
		{proto.UsageWindow{Label: "7d", WindowMinutes: 1}, 10080},
		{proto.UsageWindow{Label: "45m"}, 45},
		{proto.UsageWindow{Label: "weekly", WindowMinutes: 10080}, 10080},
		{proto.UsageWindow{}, 0},
	} {
		if got := usageWindowMinutes(c.w); got != c.want {
			t.Errorf("%+v: %d, want %d", c.w, got, c.want)
		}
	}
	if paceFor(300) != paceShort || paceFor(10080) != paceLong || paceFor(60) != paceShort {
		t.Fatal("pace parameters by window length")
	}
}

func TestUsageAccountsComeFromTheDevices(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	devs := &fleetDevs{accounts: []proto.UsageAccount{{ID: "a", Provider: "codex", Windows: []proto.UsageWindow{paceAt(now, "5h", 300, time.Hour, 0, "")}}}}
	s := &Service{devs: devs}
	got := headroomOf(s.usageAccounts(context.Background()), "codex", "", now)
	if !got.Known || math.Abs(got.H-0.17) > 1e-9 {
		t.Fatalf("headroom from the devices = %+v", got)
	}
	if (&Service{}).usageAccounts(context.Background()) != nil {
		t.Fatal("no fleet is no accounts")
	}
}
