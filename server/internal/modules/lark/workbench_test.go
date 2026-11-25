package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// A card names where an item came from in words: a connector's label for it, else its source's name (a subscription's
// one row has no label, #5671), never the source's id.
func TestWorkbenchSourceIsAName(t *testing.T) {
	for _, c := range []struct {
		it   assistantwire.WorkbenchItem
		want string
	}{
		{assistantwire.WorkbenchItem{Source: "github", AccountName: "GitHub", Connector: true, ConnectorLabel: "Review requested"}, "Review requested"},
		{assistantwire.WorkbenchItem{Source: "sub_example", AccountName: "Example group", Connector: true}, "Example group"},
		{assistantwire.WorkbenchItem{Source: "sub_example", Connector: true}, "sub_example"},
	} {
		if got := wbSource(i18n.En, c.it); got != c.want {
			t.Errorf("%+v: %q, want %q", c.it, got, c.want)
		}
	}
}

// Sending a draft goes out under the owner's name: its button asks first in Feishu's dialog, Redraft does not.
func TestDraftCardSendAsksFirst(t *testing.T) {
	s := newTestService(t, newFakeHub())
	it := assistantwire.WorkbenchItem{ID: "i1", Source: "lark", Title: "项目群", Sender: "张三", Preview: "明天能发吗？"}
	js := s.sink().s.draftCard(context.Background(), it, "可以，明早发。", nil, "").JSON()
	if n := strings.Count(js, `"confirm":`); n != 1 || !strings.Contains(js, `"op":"send"`) || !strings.Contains(js, `"op":"redraft"`) {
		t.Fatalf("the draft card asks first %d times: %s", n, js)
	}
}
