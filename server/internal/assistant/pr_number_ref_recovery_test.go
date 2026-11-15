package assistant

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestRecoveredNumberedPRUsesReplyRepository(t *testing.T) {
	for _, saved := range []string{"https://github.com/o/r/pull/4", "https://github.com/o/r/pull/5"} {
		t.Run(strings.TrimPrefix(saved, "https://github.com/"), func(t *testing.T) {
			s, todo := deliveryReviewService(t, &fakeForge{}, nil, false)
			f := s.hub.(*fakeHub)
			ctx := context.Background()
			s.setTaskStage(ctx, todo.ID, "review", saved)
			f.setItems(proto.Item{Role: "assistant", Kind: proto.KindText, Text: "Delivery repository: https://github.com/o/actual/issues/5\n" +
				"<closeout status=\"not_done\" merged=\"no\" deployed=\"no\">\nartifact: pr #5\n</closeout>"})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/o/r/issues/5":
					_, _ = w.Write([]byte(`{"number":5,"pull_request":{}}`))
				case "/repos/o/r/pulls/5":
					_, _ = w.Write([]byte(fmt.Sprintf(`{"number":5,"merged":true,"merge_commit_sha":%q}`, mergeSHA)))
				case "/repos/o/actual/issues/5":
					_, _ = w.Write([]byte(`{"number":5,"pull_request":{}}`))
				case "/repos/o/actual/pulls/5":
					_, _ = w.Write([]byte(`{"number":5,"merged":false}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			s.deploy.forge = githubForge{base: server.URL, client: server.Client()}

			_, err := acceptReview(s, todo.ID, nil)
			if err == nil || !strings.Contains(err.Error(), "not merged") {
				got := todoNow(s, todo.ID)
				t.Fatalf("accepted the wrong repository's merged PR: err=%v, task PR=%s, done=%v", err, got.PRURL, got.DoneAt != nil)
			}
			if got := todoNow(s, todo.ID).PRURL; got != "https://github.com/o/actual/pull/5" {
				t.Fatalf("recovery selected %q, want the repository named by the agent", got)
			}
		})
	}
}
