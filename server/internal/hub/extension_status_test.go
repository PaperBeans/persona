package hub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestExtensionAuthLanguageAndReplay(t *testing.T) {
	h := newTestHub(t)
	clients := map[i18n.Lang]*client{}
	h.clients = map[*client]struct{}{}
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		c := &client{hub: h, out: make(chan []byte, 8), acceptLang: l}
		clients[l] = c
		addTestClient(h, c)
	}
	canonical, _ := json.Marshal(map[string]any{"type": "extension_auth", "extension": "example", "hub_auth_message": true,
		"auth": map[string]any{"title": "Author title", "state": "error", "message": "not answering"}})
	h.broadcastRaw("extension_auth", canonical)
	read := func(c *client, l i18n.Lang) {
		t.Helper()
		select {
		case raw := <-c.out:
			var ev struct {
				Type  string                                 `json:"type"`
				Seq   int64                                  `json:"event_seq"`
				Owned *bool                                  `json:"hub_auth_message"`
				Auth  struct{ Title, State, Message string } `json:"auth"`
			}
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Type != "extension_auth" || ev.Seq != 1 || ev.Owned != nil || ev.Auth.Title != "Author title" || ev.Auth.Message != i18n.T(l, "extension-status.not_answering") {
				t.Fatalf("%s: %s", l, raw)
			}
		case <-time.After(time.Second):
			t.Fatal("auth event was not delivered")
		}
	}
	for l, c := range clients {
		read(c, l)
	}
	for _, l := range []i18n.Lang{i18n.Ja, i18n.En, i18n.ZhHans} {
		resumed := &client{hub: h, out: make(chan []byte, 8), acceptLang: l}
		h.mu.Lock()
		h.helloLocked(resumed, wsHello{Type: "hello"}, h.events.stream, 0, true)
		h.mu.Unlock()
		<-resumed.out
		read(resumed, l)
	}
}
