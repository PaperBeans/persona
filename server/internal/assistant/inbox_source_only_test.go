package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestSourceOnlyInboxAgentSkipsKnowledgeContext(t *testing.T) {
	assertKnowledgeContextSkipped(t, "source_only", "source_only")
}

func TestUnknownInboxReadProfileSkipsKnowledgeContext(t *testing.T) {
	assertKnowledgeContextSkipped(t, "misspelled", "misspelled")
}

func TestSourceOnlyInboxAgentUsesFreshReadProfile(t *testing.T) {
	// The queued pass deliberately receives the old empty snapshot; its fresh config says source_only.
	assertKnowledgeContextSkipped(t, "source_only", "")
}

func assertKnowledgeContextSkipped(t *testing.T, freshProfile, queuedProfile string) {
	t.Helper()
	ctx := context.Background()
	h, e := newInboxHubKB(t)
	if err := h.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	waitNeedsYouIdle(t, h.Service)
	goldenKB(t, e)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.ReadProfile = freshProfile
		c.Agent.ConnectorTools = []string{"lark_messages"}
	})
	it := *goldenItems(t)["mention"]
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = &it
	h.startInboxAgents(ctx, InboxAgentConfig{Enabled: true, ReadProfile: queuedProfile, ConnectorTools: []string{"lark_messages"}})
	if it.Agent.SessionID == "" {
		t.Fatalf("the read-profile agent did not start: %+v", it.Agent)
	}
	text := h.f.lastSend(t)["text"].(string)
	if strings.Contains(text, "## 知识库里的背景") {
		t.Fatalf("read profile %q injected knowledge-base context: %q", freshProfile, text)
	}
	if !strings.Contains(text, it.Context) || it.Context == "" {
		t.Fatalf("read profile %q lost the current item context: %q", freshProfile, text)
	}
}
