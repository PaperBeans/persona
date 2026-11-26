package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

func TestHealthClaudeMDUsesItems(t *testing.T) {
	needsHealthModule(t)
	ctx := context.Background()
	st := &intakeTestStore{}
	s := newServiceOn(t, healthHub(), st)
	for _, tc := range []struct {
		items                    []string
		water, protein, strength bool
	}{
		{[]string{}, false, false, false},
		{[]string{"water"}, true, false, false},
		{[]string{"protein"}, false, true, false},
		{[]string{"water", "protein"}, true, true, false},
		{[]string{"strength"}, false, false, true},
		{[]string{"water", "strength"}, true, false, true},
		{[]string{"protein", "strength"}, false, true, true},
		{[]string{"water", "protein", "strength"}, true, true, true},
	} {
		if err := st.PutSetting(ctx, "health.items", map[string]any{"items": tc.items, "revision": 1}); err != nil {
			t.Fatal(err)
		}
		got := s.healthClaudeMD(ctx, "server")
		if strings.Contains(got, "**喝水**") != tc.water || strings.Contains(got, "**蛋白质**") != tc.protein ||
			strings.Contains(got, "记下约 N 克") != tc.protein || strings.Contains(got, "喝水提醒在喝水详情页") != tc.water {
			t.Fatalf("instructions ignored items %v: %s", tc.items, got)
		}
		if strings.Contains(got, "`add_item`") != !(tc.water && tc.protein && tc.strength) {
			t.Errorf("addition guidance ignored items %v", tc.items)
		}
		for _, unchanged := range []string{"phone_health_summary", "phone_workouts_recent", "phone_workout_detail", "ask_user"} {
			if !strings.Contains(got, unchanged) {
				t.Errorf("items %v lost %s", tc.items, unchanged)
			}
		}
	}
}

func TestGoldenHealthClaudeMDItems(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en", "ja"} {
		d := prompts.AssistantHealthClaudeMD{Owner: "Example user", Language: answerLanguage(lang), Water: true, Protein: true, Strength: false, ItemsAvailable: true}
		golden(t, "health_claude_md.items."+lang, prompts.RenderAssistantHealthClaudeMD(d))
	}
}

func TestGoldenHealthClaudeMDStrength(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en", "ja"} {
		d := prompts.AssistantHealthClaudeMD{Owner: "Example user", Language: answerLanguage(lang), Water: true, Protein: true, Strength: true, ItemsAvailable: true}
		golden(t, "health_claude_md.strength."+lang, prompts.RenderAssistantHealthClaudeMD(d))
	}
}
