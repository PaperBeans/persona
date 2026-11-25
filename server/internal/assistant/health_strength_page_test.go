package assistant

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHealthStrengthPageCoreFitsAndModuleAbsent(t *testing.T) {
	s, f, d, _, key := strengthCardHarness(t)
	draft := strings.TrimPrefix(key, "health-strength-")
	for _, version := range []int{1, 5, 6, 11} {
		for _, page := range []string{"strength.edit", "strength.overview", "strength.home"} {
			raw, _ := json.Marshal(map[string]any{"page": page, "draft": draft})
			res := intakePageResult(t, intakePageHTTP(s, "", "", nil, string(raw), version, "full"))
			b, _ := json.Marshal(res.View)
			if version < 6 && strings.Contains(string(b), `"control"`) {
				t.Fatal("old app got controls")
			}
		}
	}
	off := New(Options{Hub: f, Store: func() Store { return d }, ModulesOff: []string{"health"}})
	if w := intakePageHTTP(off, "", "", nil, `{"page":"strength.overview"}`, 11, "full"); w.Code != 404 {
		t.Fatal("absent module served page", w.Code)
	}
}

func TestHealthStrengthHomeChartFitsVersions(t *testing.T) {
	s, _, _, _, _ := strengthCardHarness(t)
	for _, version := range []int{1, 2, 5, 6, 11} {
		res := intakePageResult(t, intakePageHTTP(s, "", "", nil, `{"page":"strength.home"}`, version, "full"))
		charts := 0
		for _, section := range res.View.Sections {
			for _, item := range section.Items {
				if item.Type == "chart" {
					charts++
					if item.Kind != "bar" || len(item.Series) != 1 || len(item.Series[0].Points) != 7 {
						t.Fatalf("v%d: invalid recent-days chart: %+v", version, item)
					}
				}
			}
		}
		if version == 1 && charts != 0 || version >= 2 && charts != 1 {
			t.Fatalf("v%d: got %d charts after fitting", version, charts)
		}
	}
}
