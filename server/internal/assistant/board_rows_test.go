package assistant

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestBoardWaitReadsLikeAPerson(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	for _, c := range []struct {
		l    i18n.Lang
		d    time.Duration
		want string
	}{
		{i18n.ZhHans, 30 * time.Second, "不到 1 分钟"}, {i18n.ZhHans, 2 * time.Minute, "2 分钟"}, {i18n.ZhHans, 80 * time.Minute, "1 小时 20 分"},
		{i18n.ZhHans, 2 * time.Hour, "2 小时"}, {i18n.ZhHans, 3*24*time.Hour + 5*time.Hour, "3 天"},
		{i18n.En, 2 * time.Minute, "2 min"}, {i18n.En, 80 * time.Minute, "1 h 20 min"}, {i18n.En, 72 * time.Hour, "3 d"},
	} {
		if got := boardWait(c.l, c.d); got != c.want {
			t.Errorf("boardWait(%s, %v) = %q, want %q", c.l, c.d, got, c.want)
		}
	}
}
