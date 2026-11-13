package extmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// The pages (#2041): every device draws them, unlike an extension page; only a full-access device may act on them.
func TestSubscriptionPagesRoute(t *testing.T) {
	h := newTestManager(t)
	mux := http.NewServeMux()
	h.subscriptionRoutes(mux)
	call := func(p principal, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/subscriptions/-/ui", strings.NewReader(body))
		mux.ServeHTTP(rec, req.WithContext(withPrincipal(req.Context(), p)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	full, watch := principal{ID: "dev_phone", Role: roleFull}, principal{ID: "dev_watch", Role: roleAnswer}
	if code, out := call(full, `{}`); code != http.StatusServiceUnavailable || out["code"] != "unavailable" {
		t.Fatalf("before the service runs: %d %v", code, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.startSubscriptions(ctx)
	code, out := call(watch, `{"context":{"ui_version":6,"locale":"en"}}`)
	view, _ := out["view"].(map[string]any)
	if code != http.StatusOK || view == nil || view["title"] != "Inbox Sources" {
		t.Fatalf("an answer-only device draws the list: %d %v", code, out)
	}
	if b, _ := json.Marshal(view); !strings.Contains(string(b), "can only view subscriptions") {
		t.Errorf("the read-only line is missing: %s", b)
	}
	if code, out := call(watch, `{"state":"{\"p\":\"source\",\"id\":\"sub_x\"}","action":"run_now"}`); code != http.StatusForbidden || out["code"] != "read_only_device" {
		t.Fatalf("an action from an answer-only device: %d %v", code, out)
	}
	code, out = call(full, `{"state":"{\"p\":\"source\",\"id\":\"sub_x\"}","action":"run_now","context":{"ui_version":6,"locale":"en"}}`)
	if code != http.StatusOK || out["toast"] == nil {
		t.Fatalf("an action on a subscription that isn't there says so: %d %v", code, out)
	}
}

// The pages are drawn for the app's UI version, view included (#2041): a destination's switch reaches an app of
// version 6 as one, and one of version 4 (iOS) as a row it taps, showing On.
func TestSubscriptionPagesFitTheAppsVersion(t *testing.T) {
	ctx := context.Background()
	h := newTestManager(t)
	h.connectors = connector.NewRegistry(nil)
	store := subscriptions.NewMemStore()
	sub := subscriptions.Subscription{ID: "sub_example", Connector: "example", Source: "notices", Label: "Notices",
		Recipe: subscriptions.Recipe{Mode: "items", Kind: "sub_notice", GroupBy: "source"}, ToInbox: true, ToKB: true,
		Revision: 1, CreatedAt: time.Now().UTC().Add(-time.Hour)}
	if err := store.CreateSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	h.subs = subscriptions.New(h.subscriptionOptions(store))
	mux := http.NewServeMux()
	h.subscriptionRoutes(mux)
	draw := func(version int) map[string]any {
		t.Helper()
		body := `{"state":"{\"p\":\"source\",\"id\":\"sub_example\"}","context":{"locale":"en","ui_version":` + strconv.Itoa(version) + `}}`
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/subscriptions/-/ui", strings.NewReader(body))
		mux.ServeHTTP(rec, req.WithContext(withPrincipal(req.Context(), principal{ID: "dev_phone", Role: roleFull})))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("v%d: %d %s", version, rec.Code, rec.Body)
		}
		return out
	}
	inbox := func(out map[string]any) map[string]any {
		view, _ := out["view"].(map[string]any)
		sections, _ := view["sections"].([]any)
		for _, s := range sections {
			items, _ := s.(map[string]any)["items"].([]any)
			for _, it := range items {
				if row, _ := it.(map[string]any); row["key"] == "d.to_inbox" {
					return row
				}
			}
		}
		t.Fatalf("no Add to Inbox row: %v", out)
		return nil
	}
	if row := inbox(draw(6)); row["control"] == nil {
		t.Errorf("version 6 gets no switch: %v", row)
	}
	row := inbox(draw(4))
	action, _ := row["action"].(map[string]any)
	if row["control"] != nil || row["value"] != "On" || action["id"] != "dest" {
		t.Errorf("version 4 gets the switch as a row it taps: %v", row)
	}
}
