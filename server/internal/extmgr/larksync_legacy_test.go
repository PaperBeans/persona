package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Settings from before the sync have only bots.feishu (or only hub.json): they keep working unchanged, nothing is
// migrated or written for the sync until the owner uses it, and the sync's view lists the chat bot.

// The chat bot's view keys before the sync (bots.go feishuBotView): the sync adds none.
var legacyBotViewKeys = []string{"enabled", "app_id", "domain", "has_secret", "from_config", "features", "connected", "error",
	"last_event_at", "bot_name", "owner", "pairing", "topic_chats", "nudge_after", "source_report_delay_minutes",
	"source_report_full_reply", "target", "agents", "larkcli"}

func TestLarkSyncLegacySavedBotKeepsWorking(t *testing.T) {
	needsLarkModule(t) // the bot's client is the module's
	api := newFakeOpenAPI(t)
	// A bots.feishu as an older manager saved it: credentials typed in, no sync key anywhere.
	first := newTestManager(t)
	st := newLarkSyncMemStore(time.Now)
	first.store = st
	withAssistant(first)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	first.startBots(ctx)
	mux := http.NewServeMux()
	first.botRoutes(mux)
	w := &larkWorld{h: first, store: st, mux: mux}
	if code, out := w.do(t, "PUT", "/api/v1/bots/feishu",
		fmt.Sprintf(`{"enabled":true,"app_id":"cli_legacy","app_secret":"legacy-secret","domain":%q,"features":{"main":true}}`, api.srv.URL)); code != 200 {
		t.Fatalf("enable: %d %v", code, out)
	}
	saved := st.raw(botSettingsKey)

	// A manager started on those settings.
	h := newTestManager(t)
	h.secret = first.secret
	st2 := newLarkSyncMemStore(time.Now)
	st2.vals[botSettingsKey] = []byte(saved)
	h.store = st2
	withAssistant(h)
	h.startBots(ctx)
	mux2 := http.NewServeMux()
	h.botRoutes(mux2)
	w2 := &larkWorld{h: h, store: st2, mux: mux2}
	h.bots.mu.Lock()
	started := h.bots.api != nil
	h.bots.mu.Unlock()
	if !started {
		t.Fatal("the saved bot did not start")
	}
	code, view := w2.do(t, "GET", "/api/v1/bots/feishu", "")
	if code != 200 || view["app_id"] != "cli_legacy" || view["enabled"] != true || view["has_secret"] != true || view["from_config"] != false {
		t.Fatalf("view: %d %v", code, view)
	}
	for k := range view {
		if !slices.Contains(legacyBotViewKeys, k) {
			t.Errorf("the chat bot's view gained %q", k)
		}
	}
	code, sync := w2.do(t, "GET", "/api/v1/bots/feishu/sync", "")
	bots, _ := sync["bots"].([]any)
	if code != 200 || len(bots) != 1 || len(sync["bindings"].([]any)) != 0 || sync["schedule_hour"] != float64(larkSyncDefaultHour) {
		t.Fatalf("sync view: %d %v", code, sync)
	}
	if b := bots[0].(map[string]any); b["bot_id"] != larksyncwire.PrimaryBot || b["app_id"] != "cli_legacy" || b["primary"] != true ||
		b["has_secret"] != true || b["role"] != larksyncwire.RoleChatAndSync || b["tier"] != larksyncwire.TierDocs {
		t.Fatalf("primary: %v", b)
	}
	noSecretIn(t, "the sync view", larkJSON(t, sync), "legacy-secret")
	// Reading, and the scheduler's look, write nothing; bots.feishu is as saved.
	h.bots.sync.userAuth = &fakeUserAuth{now: time.Now}
	h.larkSyncTick(ctx)
	for _, k := range []string{larkSyncPrefsKey, larkSyncBotsKey, larkSyncBindingsKey, larkSyncAccountsKey} {
		if raw := st2.raw(k); raw != "" {
			t.Fatalf("%s written: %s", k, raw)
		}
	}
	if st2.raw(botSettingsKey) != saved {
		t.Fatalf("bots.feishu changed: %s -> %s", saved, st2.raw(botSettingsKey))
	}
}

// hub.json's bots.feishu alone (no settings at all) is the primary bot too.
func TestLarkSyncHubJSONOnlyBot(t *testing.T) {
	w := newLarkWorld(t) // its primary app is hub.json's
	code, view := w.do(t, "GET", "/api/v1/bots/feishu", "")
	if code != 200 || view["app_id"] != "cli_primary" || view["from_config"] != true {
		t.Fatalf("view: %d %v", code, view)
	}
	_, sync := w.do(t, "GET", "/api/v1/bots/feishu/sync", "")
	bots := sync["bots"].([]any)
	if len(bots) != 1 || bots[0].(map[string]any)["app_id"] != "cli_primary" || bots[0].(map[string]any)["has_secret"] != true {
		t.Fatalf("sync view: %v", sync)
	}
	// The sync authorizes hub.json's app as it is.
	w.ua.configure = larksyncwire.RedirectReport{Configured: true, Published: true}
	if code, out := w.do(t, "POST", "/api/v1/bots/feishu/sync/bots/primary/authorize", `{"redirect_uri":"`+larkRedirect+`"}`); code != 200 || out["mode"] != larkSyncModeBrowser {
		t.Fatalf("authorize: %d %v", code, out)
	}
	if req := w.ua.authorized[0]; req.App.AppID != "cli_primary" || req.App.AppSecret != "primary-secret" {
		t.Fatalf("app: %+v", req.App)
	}
	if raw := w.store.raw(botSettingsKey); raw != "" {
		t.Fatalf("bots.feishu written: %s", raw)
	}

	// No app anywhere: the view lists no bot, and authorizing the primary one says there is no app.
	w.h.cfg.Bots.Feishu = FeishuBotConfig{}
	_, sync = w.do(t, "GET", "/api/v1/bots/feishu/sync", "")
	if len(sync["bots"].([]any)) != 0 {
		t.Fatalf("no app: %v", sync)
	}
	if code, _ := w.do(t, "POST", "/api/v1/bots/feishu/sync/bots/primary/authorize", `{"redirect_uri":"`+larkRedirect+`"}`); code != http.StatusConflict {
		t.Fatalf("no app: %d", code)
	}
}

// Every sealed field of the sync's settings is one the key rotation re-seals (db.sealedSettings), at the top level of
// the setting (an object) or of each element of a list, and every field listed there exists.
func TestLarkSyncSealedFieldsAreReSealed(t *testing.T) {
	for _, c := range []struct {
		key  string
		list bool
		typ  reflect.Type
	}{
		{larkSyncPrefsKey, false, reflect.TypeOf(larkSyncPrefs{})},
		{larkSyncBotsKey, true, reflect.TypeOf(larkSyncBot{})},
		{larkSyncBindingsKey, true, reflect.TypeOf(larkSyncBinding{})},
	} {
		i := slices.IndexFunc(db.SealedSettings(), func(s db.SealedSetting) bool { return s.Key == c.key })
		if i < 0 || db.SealedSettings()[i].List != c.list {
			t.Errorf("%s is not in db.SealedSettings (list %v)", c.key, c.list)
			continue
		}
		listed := db.SealedSettings()[i].Fields
		var sealed []string
		for f := range c.typ.NumField() {
			name, _, _ := strings.Cut(c.typ.Field(f).Tag.Get("json"), ",")
			if strings.HasSuffix(name, "_enc") || strings.HasSuffix(name, "_sealed") {
				sealed = append(sealed, name)
				if !slices.Contains(listed, name) {
					t.Errorf("%s.%s is sealed and not re-sealed by the key rotation", c.key, name)
				}
			}
		}
		for _, name := range listed {
			if !slices.Contains(sealed, name) {
				t.Errorf("db.SealedSettings lists %s.%s, which %s does not have", c.key, name, c.typ)
			}
		}
	}
	// The accounts hold nothing secret.
	if slices.ContainsFunc(db.SealedSettings(), func(s db.SealedSetting) bool { return s.Key == larkSyncAccountsKey }) {
		t.Error("bots.feishu.sync.accounts is listed as sealed")
	}
	typ := reflect.TypeOf(larkSyncAccount{})
	for f := range typ.NumField() {
		if name := typ.Field(f).Tag.Get("json"); strings.Contains(name, "_enc") || strings.Contains(name, "token") {
			t.Errorf("an account keeps %s", name)
		}
	}
	// What is sealed is really there, sealed, where the rotation looks.
	w := newLarkWorld(t)
	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierDocs)
	w.adopt(t, "bsync", "ou_a", "plain-access", "plain-refresh", time.Hour)
	var bindings []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(w.store.raw(larkSyncBindingsKey)), &bindings); err != nil || len(bindings) != 1 {
		t.Fatalf("bindings: %v %s", err, w.store.raw(larkSyncBindingsKey))
	}
	for _, f := range []string{"access_token_enc", "refresh_token_enc"} {
		var v string
		_ = json.Unmarshal(bindings[0][f], &v)
		if plain, err := w.h.larkSyncOpen(v); err != nil || !strings.HasPrefix(plain, "plain-") {
			t.Fatalf("%s: %q %v", f, plain, err)
		}
	}
	noSecretIn(t, "bindings", w.store.raw(larkSyncBindingsKey), "plain-access", "plain-refresh")
	noSecretIn(t, "bots", w.store.raw(larkSyncBotsKey), "cli_sync-secret")
}
