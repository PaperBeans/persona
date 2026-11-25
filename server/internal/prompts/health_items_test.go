package prompts

import (
	"strings"
	"testing"
)

func TestHealthItemsPromptConsentAndSections(t *testing.T) {
	for _, lang := range []string{"", "zh-Hans", "en", "ja"} {
		for _, water := range []bool{false, true} {
			for _, protein := range []bool{false, true} {
				for _, strength := range []bool{false, true} {
					got := RenderAssistantHealthClaudeMD(AssistantHealthClaudeMD{Language: lang, Water: water, Protein: protein, Strength: strength, ItemsAvailable: true})
					if strings.Contains(got, "**喝水**") != water || strings.Contains(got, "**蛋白质**") != protein ||
						strings.Contains(got, "记下约 N 克") != protein {
						t.Fatalf("unexpected intake sections for %s water=%v protein=%v", lang, water, protein)
					}
					if !strings.Contains(got, "估算的数量先用 `ask_user` 确认，确认后才 `log`") {
						t.Errorf("%s lost estimate consent: water=%v protein=%v", lang, water, protein)
					}
					adding := !(water && protein && strength)
					if strings.Contains(got, "**添加健康项**") != adding || strings.Contains(got, "`add_item`") != adding {
						t.Errorf("%s addition guidance: water=%v protein=%v", lang, water, protein)
					}
					if adding && (!strings.Contains(got, "先用 `ask_user`") || !strings.Contains(got, "同意后才用")) {
						t.Errorf("%s lost addition consent", lang)
					}
					if strings.Contains(got, "\n\n  这些记忆") || strings.Contains(got, "\n\n- **单个关键指标**") {
						t.Errorf("%s introduced a blank line inside a list: water=%v protein=%v", lang, water, protein)
					}
					for _, old := range []string{"Persona intake", "Adding an item", "Added intake items also use"} {
						if strings.Contains(got, old) {
							t.Errorf("%s retained English intake guidance: %s", lang, old)
						}
					}
					for _, rule := range []string{"phone_health_summary", "phone_workouts_recent"} {
						if !strings.Contains(got, rule) {
							t.Errorf("%s lost %s", lang, rule)
						}
					}
				}
			}
		}
	}
}

func TestHealthStrengthPromptSection(t *testing.T) {
	for _, lang := range []string{"", "English", "日本語"} {
		for _, strength := range []bool{false, true} {
			got := RenderAssistantHealthClaudeMD(AssistantHealthClaudeMD{Language: lang, Strength: strength, ItemsAvailable: true})
			if strings.Contains(got, "**力量训练**") != strength {
				t.Fatalf("strength section=%v for %q", strength, lang)
			}
			if strings.Contains(got, "用户说开始力量训练") != strength || strings.Contains(got, "`draft` 的 `plan`") != strength {
				t.Fatalf("training-start plan guidance=%v for %q", strength, lang)
			}
			if strings.Contains(got, "\n\n- **力量训练**") || strings.Contains(got, "\n\n- **添加健康项**") || strings.Contains(got, "\n\n- **其他数据**") || strings.Contains(got, "\n\n  ") {
				t.Fatalf("extra blank line in list for %q", lang)
			}
			if strength {
				for _, rule := range []string{"`find`", "`draft`", "不编重量或次数", "不另存", "`undo`", "`history`"} {
					if !strings.Contains(got, rule) {
						t.Errorf("missing strength rule %s", rule)
					}
				}
			}
		}
	}
}
