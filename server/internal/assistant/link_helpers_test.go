package assistant

import (
	"net/url"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

// A link's button says what it opens: a page of the hub's web app by the page, a Feishu document as one, any other
// site by its name.
func TestLinkNamer(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	name := linkNamer(i18n.ZhHans, "https://hub.example.com")
	for _, c := range []struct{ u, want string }{
		{"https://hub.example.com/persona/c1", "打开会话"},
		{"https://hub.example.com/s/server/a1", "打开会话"},
		{"https://hub.example.com/todos/t1", "打开待办"},
		{"https://hub.example.com/inbox", "打开收件箱"},
		{"https://hub.example.com/kb/people/p1", "打开知识库"},
		{"https://hub.example.com/machines", "打开网页版"},
		{"https://example.feishu.cn/docx/AbC", "打开飞书文档"},
		{"https://example.feishu.cn/calendar", "打开 example.feishu.cn"},
		{"https://www.github.com/nwverse/persona", "打开 github.com"},
	} {
		u, _ := url.Parse(c.u)
		if got := name(u); got != c.want {
			t.Errorf("%s: %q, want %q", c.u, got, c.want)
		}
	}
}
