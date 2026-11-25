package health

import (
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func TestHealthIntakePageGoalKeepsCategoryColor(t *testing.T) {
	now := waterTestTime("2026-11-16T10:00:00Z")
	c := defaultHealthIntakeSettings()
	for _, tc := range []struct {
		kind, color string
		goal        int
	}{{"water", "blue", c.Water.Goal}, {"protein", "amber", c.Protein.Goal}} {
		for _, factor := range []float64{0.5, 1, 1.5} {
			t.Run(fmt.Sprintf("%s/%g", tc.kind, factor), func(t *testing.T) {
				amount := float64(tc.goal) * factor
				v := sumHealthIntake([]assistantwire.HealthIntakeRow{{Kind: tc.kind, Amount: amount, At: now}}, c, now, time.UTC, 7)
				page := drawIntakePage(intakePageState{Kind: tc.kind, Days: 7}, c, v, waterReminderState{}, now, time.UTC, "en", 11, false, true)
				tile := page.Sections[0].Items[0].Tiles[0]
				if tile.Color != tc.color || tile.Gauge.Color != tc.color || *tile.Gauge.Ratio != min(1.0, factor) || tile.Value != intakeNumber(amount) {
					t.Fatalf("goal progress: %+v", tile)
				}
				if factor >= 1 && tile.Subtitle != "Today's goal is met." {
					t.Fatalf("goal confirmation missing: %+v", tile)
				}
			})
		}
	}
}

func TestHealthIntakePageTimezoneCopyByKind(t *testing.T) {
	now := waterTestTime("2026-11-16T10:00:00Z")
	c := defaultHealthIntakeSettings()
	v := sumHealthIntake(nil, c, now, time.UTC, 7)
	for _, tc := range []struct {
		lang           i18n.Lang
		water, protein string
	}{
		{"en", "Recorded and reminded in UTC time", "Recorded in UTC time"},
		{"zh-Hans", "按 UTC 时间提醒和记录", "按 UTC 时间记录"},  // i18n-ignore: localized view contract
		{"ja", "UTC の時刻で記録・通知します", "UTC の時刻で記録します"}, // i18n-ignore: localized view contract
	} {
		for _, kind := range []string{"water", "protein"} {
			t.Run(string(tc.lang)+"/"+kind, func(t *testing.T) {
				page := drawIntakePage(intakePageState{Kind: kind, Days: 7}, c, v, waterReminderState{}, now, time.UTC, tc.lang, 11, false, true)
				want := tc.water
				if kind == "protein" {
					want = tc.protein
				}
				if got := page.Sections[0].Items[1].Text; got != want {
					t.Fatalf("%s timezone copy: got %q, want %q", kind, got, want)
				}
			})
		}
	}
}
