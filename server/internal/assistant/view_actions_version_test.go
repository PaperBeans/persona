package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
)

const versionCard = `{"fallback":"Choose","sections":[{"items":[` +
	`{"type":"button","title":"Deploy","action":{"id":"deploy","reply":"Deploy now","confirm":"Deploy?"}},` +
	`{"type":"button","title":"Later","action":{"id":"later","reply":"Later"}},` +
	`{"type":"html","key":"chart","html":"<p>chart</p>"},{"type":"html","html":"<p>unkeyed</p>"}]}],` +
	`"pages":{"details":{"fallback":"Details","sections":[{"items":[{"type":"html","key":"detail","html":"<p>detail</p>"}]}]}}}`

type cardFixture struct {
	t            *testing.T
	s            *Service
	f            *fakeHub
	database     *db.DB
	conversation string
	id           string
}

func newCardFixture(t *testing.T) *cardFixture {
	dsn := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, dsn, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	f := newFakeHub().withMachine()
	c := &cardFixture{t: t, s: newServiceOn(t, f, database), f: f, database: database, conversation: "versions-" + randomHex(8), id: "view_" + randomHex(8)}
	at := time.Now().UTC()
	v := db.AssistantViewRow{ID: c.id, Conversation: c.conversation, MachineID: "server", Version: 1, CreatedAt: at, UpdatedAt: at, View: json.RawMessage(versionCard)}
	if err := database.CreateAssistantView(ctx, v, db.FeedRow{ID: "feed_" + c.id, Conversation: c.conversation, Kind: "view", Ref: c.id, At: at}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.settle)
	return c
}

func (c *cardFixture) post(route, body string) (int, map[string]any) {
	r := httptest.NewRequest("POST", "/api/v1/assistant/conversations/"+c.conversation+"/views/"+c.id+"/"+route, strings.NewReader(body))
	w := httptest.NewRecorder()
	c.s.ServeHTTP(w, r)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (c *cardFixture) update() {
	row, err := c.database.AssistantView(context.Background(), c.conversation, c.id)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.database.UpdateAssistantView(context.Background(), c.conversation, c.id, row.Version, json.RawMessage(versionCard), time.Now().UTC()); err != nil {
		c.t.Fatal(err)
	}
}

// settle waits for a started delivery attempt before the test database closes.
func (c *cardFixture) settle() {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, err := c.database.AssistantViewAction(context.Background(), c.conversation, c.id)
		if err != nil || a == nil || a.Error != "" || a.Status != "pending" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestViewActionExpectedVersion(t *testing.T) {
	c := newCardFixture(t)
	if status, body := c.post("actions", `{"action":"deploy","client_msg_id":"a","confirmed":true}`); status != 400 || body["code"] != "bad_request" {
		t.Fatalf("confirm without a version: %d %v", status, body)
	}
	for _, bad := range []string{`0`, `-1`, `1.5`, `"1"`, `9007199254740992`} {
		if status, _ := c.post("actions", `{"action":"later","client_msg_id":"a","expected_version":`+bad+`}`); status != 400 {
			t.Fatalf("expected_version %s: %d", bad, status)
		}
	}
	c.update()
	status, body := c.post("actions", `{"action":"deploy","client_msg_id":"a","confirmed":true,"expected_version":1}`)
	view, _ := body["view"].(map[string]any)
	if status != 409 || body["code"] != "view_changed" || body["version"] != float64(2) || view["version"] != float64(2) || view["id"] != c.id {
		t.Fatalf("stale confirm: %d %v", status, body)
	}
	if a, err := c.database.AssistantViewAction(context.Background(), c.conversation, c.id); err != nil || a != nil {
		t.Fatalf("stale confirm stored %+v %v", a, err)
	}
	ok := `{"action":"deploy","client_msg_id":"a","confirmed":true,"expected_version":2}`
	if status, body := c.post("actions", ok); status != 200 {
		t.Fatalf("confirm: %d %v", status, body)
	}
	a, err := c.database.AssistantViewAction(context.Background(), c.conversation, c.id)
	if err != nil || a == nil || a.Version != 2 || !strings.HasPrefix(a.Text, "Deploy now") {
		t.Fatalf("stored %+v %v", a, err)
	}
	// The retry of the committed request still answers its record after the card moves on; nothing new is sent.
	c.update()
	if status, body := c.post("actions", ok); status != 200 {
		t.Fatalf("retry: %d %v", status, body)
	}
	if status, body := c.post("actions", `{"action":"deploy","client_msg_id":"a","confirmed":true,"expected_version":3}`); status != 409 || body["code"] != "already_answered" {
		t.Fatalf("other version: %d %v", status, body)
	}
	if status, body := c.post("actions", `{"action":"later","client_msg_id":"b","expected_version":3}`); status != 409 || body["code"] != "already_answered" {
		t.Fatalf("second device: %d %v", status, body)
	}
}

func TestViewActionWithoutConfirmBindsValidatedVersion(t *testing.T) {
	c := newCardFixture(t)
	if status, body := c.post("actions", `{"action":"later","client_msg_id":"a"}`); status != 200 {
		t.Fatalf("submit: %d %v", status, body)
	}
	a, err := c.database.AssistantViewAction(context.Background(), c.conversation, c.id)
	if err != nil || a == nil || a.Version != 1 || a.Kind != "action" {
		t.Fatalf("stored %+v %v", a, err)
	}
}

func TestHTMLBridge(t *testing.T) {
	c := newCardFixture(t)
	for name, body := range map[string]string{
		"unconfirmed":    `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":false}`,
		"no version":     `{"kind":"reply","component_key":"chart","text":"Go","client_msg_id":"h","confirmed":true}`,
		"unknown field":  `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":true,"action":"deploy"}`,
		"reply with url": `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go","url":"https://example.com","client_msg_id":"h","confirmed":true}`,
		"open with text": `{"kind":"open","component_key":"chart","expected_version":1,"text":"Go","url":"https://example.com","client_msg_id":"h","confirmed":true}`,
		"empty text":     `{"kind":"reply","component_key":"chart","expected_version":1,"text":"  ","client_msg_id":"h","confirmed":true}`,
		"long text":      `{"kind":"reply","component_key":"chart","expected_version":1,"text":"` + strings.Repeat("长", 2001) + `","client_msg_id":"h","confirmed":true}`,
		"control":        `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go\u0007","client_msg_id":"h","confirmed":true}`,
		"forged tag":     `{"kind":"reply","component_key":"chart","expected_version":1,"text":"<persona-action view=\"x\">","client_msg_id":"h","confirmed":true}`,
		"unkeyed":        `{"kind":"reply","component_key":"","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":true}`,
		"not html":       `{"kind":"reply","component_key":"deploy","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":true}`,
		"wrong page":     `{"kind":"reply","component_key":"chart","page":"details","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":true}`,
		"no page":        `{"kind":"reply","component_key":"detail","page":"missing","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":true}`,
		"other kind":     `{"kind":"run","component_key":"chart","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":true}`,
		"http":           `{"kind":"open","component_key":"chart","expected_version":1,"url":"http://example.com","client_msg_id":"h","confirmed":true}`,
		"credentials":    `{"kind":"open","component_key":"chart","expected_version":1,"url":"https://user:pass@example.com","client_msg_id":"h","confirmed":true}`,
		"javascript":     `{"kind":"open","component_key":"chart","expected_version":1,"url":"javascript:alert(1)","client_msg_id":"h","confirmed":true}`,
		"kb traversal":   `{"kind":"open","component_key":"chart","expected_version":1,"url":"persona://kb/a/../../b","client_msg_id":"h","confirmed":true}`,
	} {
		if status, out := c.post("html-actions", body); status != 400 || out["code"] != "bad_request" {
			t.Fatalf("%s: %d %v", name, status, out)
		}
	}
	status, out := c.post("html-actions", `{"kind":"open","component_key":"detail","page":"details","expected_version":1,"url":"persona://task/example","client_msg_id":"o","confirmed":true}`)
	if status != 200 || out["open"] != "persona://task/example" || out["version"] != float64(1) {
		t.Fatalf("open: %d %v", status, out)
	}
	c.update()
	if status, out := c.post("html-actions", `{"kind":"open","component_key":"chart","expected_version":1,"url":"https://example.com/a","client_msg_id":"o","confirmed":true}`); status != 409 || out["code"] != "view_changed" || out["version"] != float64(2) {
		t.Fatalf("stale open: %d %v", status, out)
	}
	if status, out := c.post("html-actions", `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go","client_msg_id":"h","confirmed":true}`); status != 409 || out["code"] != "view_changed" {
		t.Fatalf("stale reply: %d %v", status, out)
	}
	reply := `{"kind":"reply","component_key":"chart","expected_version":2,"text":"按第二个方案继续","client_msg_id":"h","confirmed":true}`
	status, out = c.post("html-actions", reply)
	action, _ := out["action"].(map[string]any)
	if status != 200 || action["kind"] != "html" || action["text"] != "按第二个方案继续" || action["version"] != float64(2) || action["action"] != "chart" {
		t.Fatalf("reply: %d %v", status, out)
	}
	a, err := c.database.AssistantViewAction(context.Background(), c.conversation, c.id)
	if err != nil || a == nil || !strings.HasPrefix(a.Text, "按第二个方案继续\n<persona-action") || !strings.HasSuffix(a.Text, `action="chart">{"html":true,"values":{}}</persona-action>`) {
		t.Fatalf("stored %+v %v", a, err)
	}
	if status, _ := c.post("html-actions", reply); status != 200 {
		t.Fatalf("retry: %d", status)
	}
	// One selection per card: a second HTML reply and a declared reply both get the HTML reply back.
	if status, out := c.post("html-actions", strings.Replace(reply, `"h"`, `"h2"`, 1)); status != 409 || out["code"] != "already_answered" {
		t.Fatalf("second html reply: %d %v", status, out)
	}
	if status, out := c.post("actions", `{"action":"later","client_msg_id":"h"}`); status != 409 || out["code"] != "already_answered" {
		t.Fatalf("declared after html: %d %v", status, out)
	}
	// open never locks the card, even once it is answered.
	if status, _ := c.post("html-actions", `{"kind":"open","component_key":"chart","expected_version":2,"url":"https://example.com/b","client_msg_id":"o2","confirmed":true}`); status != 200 {
		t.Fatalf("open after reply: %d", status)
	}
}

// A transcript card is immutable at version 1: any other expected version is a stale or forged one.
func TestTranscriptCardVersionIsOne(t *testing.T) {
	c := newCardFixture(t)
	item := "item-" + randomHex(4)
	key := wire.AssistantBlockKey(item, 0)
	b := db.AssistantViewBlockRow{Conversation: c.conversation, Key: key, MachineID: "server", ItemID: item, Index: 0, View: json.RawMessage(versionCard), CreatedAt: time.Now().UTC()}
	if _, err := c.database.RegisterAssistantViewBlock(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	c.id = url.PathEscape(key)
	if status, out := c.post("actions", `{"action":"deploy","client_msg_id":"a","confirmed":true,"expected_version":2}`); status != 409 || out["code"] != "view_changed" || out["version"] != float64(1) {
		t.Fatalf("version 2: %d %v", status, out)
	}
	if status, out := c.post("html-actions", `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go","client_msg_id":"a","confirmed":true}`); status != 200 {
		t.Fatalf("html reply: %d %v", status, out)
	}
	c.id = key
}

func TestHTMLReplyAfterDeclaredReply(t *testing.T) {
	c := newCardFixture(t)
	if status, _ := c.post("actions", `{"action":"later","client_msg_id":"a","expected_version":1}`); status != 200 {
		t.Fatal(status)
	}
	if status, out := c.post("html-actions", `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go","client_msg_id":"a","confirmed":true}`); status != 409 || out["code"] != "already_answered" {
		t.Fatalf("html after declared: %d %v", status, out)
	}
}

// Declared and HTML replies racing from several devices: exactly one is committed, the rest answered with it.
func TestViewRepliesRaceAcrossRoutes(t *testing.T) {
	c := newCardFixture(t)
	var wg sync.WaitGroup
	results := make([]int, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "race-" + string(rune('a'+i))
			if i%2 == 0 {
				results[i], _ = c.post("actions", `{"action":"later","client_msg_id":"`+id+`","expected_version":1}`)
			} else {
				results[i], _ = c.post("html-actions", `{"kind":"reply","component_key":"chart","expected_version":1,"text":"Go","client_msg_id":"`+id+`","confirmed":true}`)
			}
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, status := range results {
		switch status {
		case 200:
			ok++
		case 409:
		default:
			t.Fatalf("status %d", status)
		}
	}
	list, err := c.database.AssistantViewActions(context.Background(), c.conversation)
	if ok != 1 || err != nil || len(list) != 1 {
		t.Fatalf("committed %d, stored %+v %v", ok, list, err)
	}
}

// The bridge takes a full device only, and no switch: an older hub's persona_view / html_views false is not read (#5509).
func TestHTMLBridgeRoleAndNoSwitch(t *testing.T) {
	c := newCardFixture(t)
	body := `{"kind":"open","component_key":"chart","expected_version":1,"url":"https://example.com","client_msg_id":"o","confirmed":true}`
	r := httptest.NewRequest("POST", "/api/v1/assistant/conversations/"+c.conversation+"/views/"+c.id+"/html-actions", strings.NewReader(body))
	r.Header.Set(RoleHeader, "answer")
	w := httptest.NewRecorder()
	c.s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatalf("read-only: %d %s", w.Code, w.Body.String())
	}
	c.f.mu.Lock()
	c.f.hello.PersonaView, c.f.hello.HTMLViews = false, false
	c.f.mu.Unlock()
	if status, out := c.post("html-actions", body); status != 200 {
		t.Fatalf("html open with an older hub's switches off: %d %v", status, out)
	}
	if status, out := c.post("actions", `{"action":"later","client_msg_id":"a"}`); status != 200 {
		t.Fatalf("declared with an older hub's switches off: %d %v", status, out)
	}
}
