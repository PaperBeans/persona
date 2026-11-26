package kb

import (
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The language a recording's minutes, summary and recap are written in: the recording's, else the owner's on the hub
// (Engine.OwnerLanguage), else Chinese.
func TestOutputLanguage(t *testing.T) {
	owner := func(l i18n.Lang) func() i18n.Lang { return func() i18n.Lang { return l } }
	for _, tc := range []struct {
		recording string
		owner     func() i18n.Lang
		want      i18n.Lang
	}{
		{"en-US", nil, i18n.En},
		{"ja-JP", owner(i18n.En), i18n.Ja},
		{"zh-CN", owner(i18n.Ja), i18n.ZhHans},
		{" ja ", nil, i18n.Ja},
		{"", owner(i18n.Ja), i18n.Ja},
		{"", owner(i18n.En), i18n.En},
		{"", owner("fr"), i18n.ZhHans}, // not one of the hub's
		{"", owner(""), i18n.ZhHans},
		{"", nil, i18n.ZhHans},
		// A language the hub has no catalog for is not said: the owner's, not en (#5906).
		{"ko-KR", owner(i18n.Ja), i18n.Ja},
		{"fr-FR", owner(i18n.ZhHans), i18n.ZhHans},
		{"yue-CN", nil, i18n.ZhHans},
	} {
		e := &Engine{OwnerLanguage: tc.owner}
		if got := e.outputLang(&Recording{Language: tc.recording}); got != tc.want {
			t.Errorf("recording %q: %q, want %q", tc.recording, got, tc.want)
		}
	}
}
