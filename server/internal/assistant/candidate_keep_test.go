package assistant

import (
	"testing"
	"time"
)

func TestKeepSwitch(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	cases := []struct {
		name string
		f    keepFacts
		want string
	}{
		{name: "nothing known keeps", f: keepFacts{}, want: ""},
		{name: "headroom keeps", f: keepFacts{Headroom: candidateHeadroom{Known: true, H: 0.2}}, want: ""},
		{name: "stale or unknown headroom keeps", f: keepFacts{Headroom: candidateHeadroom{Stale: true}}, want: ""},
		{name: "no headroom but back within two hours keeps", f: keepFacts{Headroom: candidateHeadroom{Known: true, RecoversAt: at(90 * time.Minute)}}, want: ""},
		{name: "no headroom for longer than two hours switches", f: keepFacts{Headroom: candidateHeadroom{Known: true, RecoversAt: at(3 * time.Hour)}}, want: switchQuota},
		{name: "no headroom and no recovery known switches", f: keepFacts{Headroom: candidateHeadroom{Known: true}}, want: switchQuota},
		{name: "one rework keeps", f: keepFacts{ReworkRounds: 1}, want: ""},
		{name: "two reworks switch", f: keepFacts{ReworkRounds: 2}, want: switchRework},
		{name: "two failed starts keep", f: keepFacts{FailStreak: 2}, want: ""},
		{name: "three failed starts switch", f: keepFacts{FailStreak: 3}, want: switchFailing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, args := keepSwitch(c.f, now)
			if got != c.want {
				t.Fatalf("keepSwitch = %q, want %q", got, c.want)
			}
			if got != "" && args["rule"] != got {
				t.Fatalf("args %v do not name the rule %q", args, got)
			}
			if got == "" && args != nil {
				t.Fatalf("a kept candidate has no args, got %v", args)
			}
		})
	}
}
