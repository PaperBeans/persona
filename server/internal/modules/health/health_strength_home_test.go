package health

import (
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func strengthHomeChartInView(t *testing.T, view wire.View) wire.Component {
	t.Helper()
	var charts []wire.Component
	for _, section := range view.Sections {
		for _, item := range section.Items {
			if item.Type == "chart" {
				if len(section.Items) != 1 || section.Title == item.Title {
					t.Fatal("recent days section must contain only its chart, titled apart from the section", section)
				}
				charts = append(charts, item)
			}
		}
	}
	if len(charts) != 1 || charts[0].Kind != "bar" || len(charts[0].Series) != 1 || len(charts[0].Series[0].Points) != 7 {
		t.Fatal("expected one seven-day bar chart", charts)
	}
	return charts[0]
}

func TestStrengthHomeChartLocalDaysAndWorkingSets(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		zone   string
		labels []string
	}{
		{"America/Los_Angeles", []string{"10-26", "10-27", "10-28", "10-29", "10-30", "10-31", "11-01"}},
		{"Pacific/Kiritimati", []string{"10-27", "10-28", "10-29", "10-30", "10-31", "11-01", "11-02"}},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			today := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
			if tc.zone == "America/Los_Angeles" {
				today = time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
			}
			from := today.AddDate(0, 0, -6)
			excluded := []assistantwire.StrengthSet{
				{At: from.Add(-time.Nanosecond)},
				{At: today, Warmup: true},
				{At: today, DeletedAt: &now},
				{At: now.Add(time.Minute)},
				{At: today.AddDate(0, 0, 1)},
			}
			working := []assistantwire.StrengthSet{
				{At: from},
				{At: from.AddDate(0, 0, 1).Add(12 * time.Hour)},
				{At: from.AddDate(0, 0, 1).Add(13 * time.Hour)},
				{At: today.Add(-time.Nanosecond)},
				{At: today},
				{At: now},
			}
			for _, lang := range []i18n.Lang{"en", "zh-Hans", "ja"} {
				for _, empty := range []bool{false, true} {
					rows := append([]assistantwire.StrengthSet{}, excluded...)
					want := []float64{0, 0, 0, 0, 0, 0, 0}
					if !empty {
						rows = append(rows, working...)
						want = []float64{1, 2, 0, 0, 0, 1, 2}
					}
					view := drawStrengthPage(strengthPageState{Page: "strength.home"}, nil, rows, nil, nil, now, loc, lang, 11)
					if err := wire.CheckView(&view); err != nil {
						t.Fatal(err)
					}
					chart := strengthHomeChartInView(t, view)
					for _, section := range view.Sections {
						if len(section.Items) == 1 && section.Items[0].Type == "chart" && section.Title != i18n.T(lang, "assistant-health_strength.recent_days") {
							t.Fatal("recent days section title ignored the language", section.Title)
						}
					}
					if chart.Title != i18n.T(lang, "assistant-health_strength.sets") || chart.Unit != i18n.T(lang, "assistant-health_strength.sets_unit") || chart.Series[0].Name != i18n.T(lang, "assistant-health_strength.sets") {
						t.Fatal("chart copy ignored the language", chart)
					}
					for i, point := range chart.Series[0].Points {
						if point.Label != tc.labels[i] || point.Value != want[i] {
							t.Fatalf("%s empty=%v point %d: %+v, want %s=%g", lang, empty, i, point, tc.labels[i], want[i])
						}
					}
				}
			}
			// A new owner gets the same chart, with seven zero values and no extra empty-state text in it.
			chart := strengthHomeChartInView(t, drawStrengthPage(strengthPageState{Page: "strength.home"}, nil, nil, nil, nil, now, loc, "en", 11))
			for _, point := range chart.Series[0].Points {
				if point.Value != 0 {
					t.Fatal("empty history produced a nonzero bar", point)
				}
			}
		})
	}
}

func TestStrengthHomeChartHTTPVersionsAndUndo(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	now := time.Now().UTC().Add(-time.Minute)
	var rows []assistantwire.StrengthSet
	for i := range 3 {
		rows = append(rows, assistantwire.StrengthSet{ID: healthIntakeUUID(fmt.Sprintf("home-chart-%d", i)), ExerciseID: e.ID, At: now, Weight: 40, Reps: 10, Warmup: i == 2})
	}
	if _, err := d.HealthStrengthSetAdd(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.HealthStrengthSetUndo(ctx, rows[1].ID); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2, 5, 6, 11} {
		res := strengthPageHTTP(t, s, strengthPageState{Page: "strength.home"}, "", nil, version)
		if version == 1 {
			// The real core's version fitting is covered by TestHealthStrengthHomeChartFitsVersions.
			continue
		}
		chart := strengthHomeChartInView(t, *res.View)
		total := 0.0
		for _, point := range chart.Series[0].Points {
			total += point.Value
		}
		if total != 1 {
			t.Fatalf("v%d counted warm-up or undone set: %g", version, total)
		}
	}
}
