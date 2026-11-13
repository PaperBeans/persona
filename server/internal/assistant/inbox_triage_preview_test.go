package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

// Trying triage on a dry run's items (#5278): each is asked about as the Inbox would be handed it, under its rules
// and its kind's words, at most three; nothing becomes an item or is written.
func TestInboxTriagePreviewKeepsNothing(t *testing.T) {
	h := newInboxHub(t)
	model, client := newTriageModel(t, "ok", `{"tier":"noise","summary":"A newsletter","reason":"bulk"}`)
	h.in.setModel(client)
	thing := func(title string) connector.InboxThing {
		return connector.InboxThing{Thread: "sub:x:" + title, Kind: "sub_notice", Title: title, Context: title + "\nnews@example.com · 11-12 09:00",
			At: time.Now(), Last: title, Rules: "Newsletters are noise"}
	}
	v := h.InboxTriagePreview(context.Background(), assistantwire.InboxTriagePreview{Name: "News",
		Kinds:  map[string]string{"sub_notice": "A notice from the News subscription"},
		Things: []connector.InboxThing{thing("one"), thing("two"), thing("three"), thing("four")}})
	if len(v) != 3 || v[0].Tier != "noise" || v[0].Summary != "A newsletter" || v[2].Reason != "bulk" {
		t.Fatalf("verdicts: %+v", v)
	}
	calls := model.asked()
	if len(calls) != 3 {
		t.Fatalf("%d model calls, want 3", len(calls))
	}
	for _, c := range calls {
		if !strings.Contains(c.System, "Newsletters are noise") || !strings.Contains(c.User, "A notice from the News subscription") {
			t.Errorf("asked without its rules or its kind's words: %+v", c)
		}
	}
	h.inbox.mu.Lock()
	n := len(h.inbox.items)
	h.inbox.mu.Unlock()
	h.st.imu.Lock()
	stored := len(h.st.rows)
	h.st.imu.Unlock()
	if n != 0 || stored != 0 {
		t.Errorf("a preview left %d items, %d stored", n, stored)
	}
	model.mode.Store("quota")
	if v := h.InboxTriagePreview(context.Background(), assistantwire.InboxTriagePreview{Things: []connector.InboxThing{thing("x")}}); len(v) != 1 ||
		v[0].Tier != "" || v[0].State != triagePaused || v[0].Why == "" {
		t.Errorf("a model out of quota says why: %+v", v)
	}
}

// The Inbox Sources pages read a source's mutes and unmute one through the bus, as the Muted list does.
func TestInboxSourceMutes(t *testing.T) {
	h := newInboxHub(t)
	ctx := context.Background()
	m, _, err := h.addMute(ctx, InboxMute{Source: "mail", Tag: connector.TagSender, Value: "shop@example.com", Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.addMute(ctx, InboxMute{Source: "github", Tag: connector.TagRepo, Value: "example/site", Name: "example/site"}); err != nil {
		t.Fatal(err)
	}
	mail, err := h.InboxSourceMutes(ctx, "mail")
	if err != nil || len(mail) != 1 || mail[0].ID != m.ID || mail[0].Name != "Shop" {
		t.Fatalf("mail's mutes: %+v %v", mail, err)
	}
	if all, _ := h.InboxSourceMutes(ctx, ""); len(all) != 2 {
		t.Errorf("every source's: %+v", all)
	}
	if err := h.InboxUnmute(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if mail, _ := h.InboxSourceMutes(ctx, "mail"); len(mail) != 0 {
		t.Errorf("unmuted, still: %+v", mail)
	}
}
