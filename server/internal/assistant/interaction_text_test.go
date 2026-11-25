package assistant

import (
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestResolutionText(t *testing.T) {
	yes, no := true, false
	q := proto.Interaction{Kind: proto.IxQuestion, Questions: []proto.Question{{Question: "a"}, {Question: "b"}}}
	for _, c := range []struct {
		ix   proto.Interaction
		r    proto.Resolution
		want string
	}{
		{q, proto.Resolution{Answers: map[string]string{"a": "1", "b": "2"}}, "已回答：1；2"},
		{proto.Interaction{Kind: proto.IxPlan}, proto.Resolution{Approve: &yes, Mode: "acceptEdits"}, "已批准，自动接受编辑"},
		{proto.Interaction{Kind: proto.IxPlan}, proto.Resolution{Approve: &no, Feedback: "拆小点"}, "已打回：拆小点"},
		{proto.Interaction{Kind: proto.IxPermission}, proto.Resolution{Allow: &yes, Always: true}, "已允许（本会话不再问）"},
		{proto.Interaction{Kind: proto.IxPermission}, proto.Resolution{Allow: &no}, "已拒绝"},
		{proto.Interaction{}, proto.Resolution{Dismiss: true}, "已忽略"},
	} {
		if got := resolutionText("", c.ix, c.r); got != c.want {
			t.Errorf("%+v: %q, want %q", c.r, got, c.want)
		}
	}
}
