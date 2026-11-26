package feishubot

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Begin asks for a personal-agent app with the owner's open id, and the page it gives carries the app's name and
// the scopes and card callback the bot needs.
func TestRegistrationBeginAsksForTheOwnerAndTheBotsScopes(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != registrationPath {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		form = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "dc_1", "user_code": "UC", "verification_uri": "https://accounts.example.com/verify",
			"expire_in": 600, "interval": 5})
	}))
	defer srv.Close()
	ch, err := NewRegistrar().WithHost(srv.URL).Begin(context.Background(), "feishu", "Persona")
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"action": "begin", "archetype": "PersonalAgent", "auth_method": "client_secret", "request_user_info": "open_id tenant_brand"} {
		if got := form.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if ch.DeviceCode != "dc_1" || ch.ExpiresIn != 600 || ch.Interval != 5 {
		t.Fatalf("challenge: %+v", ch)
	}
	u, err := url.Parse(ch.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("user_code") != "UC" || q.Get("name") != "Persona" || q.Get("from") != "sdk" {
		t.Fatalf("page: %s", ch.URL)
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Get("addons"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(zr)
	var addons struct {
		Scopes struct {
			Tenant []string `json:"tenant"`
		} `json:"scopes"`
		Callbacks struct {
			Items []string `json:"items"`
		} `json:"callbacks"`
	}
	if err := json.Unmarshal(payload, &addons); err != nil {
		t.Fatal(err)
	}
	if len(addons.Scopes.Tenant) != len(registerScopes) || len(addons.Callbacks.Items) != 1 || addons.Callbacks.Items[0] != "card.action.trigger" {
		t.Fatalf("addons: %s", payload)
	}
}

// Poll maps the platform's OAuth-style answers; a confirmation without the credentials yet is still pending.
func TestRegistrationPollStatuses(t *testing.T) {
	cases := []struct {
		body, want string
	}{
		{`{"error":"authorization_pending"}`, RegisterPending},
		{`{"error":"slow_down"}`, RegisterSlowDown},
		{`{"error":"expired_token"}`, RegisterExpired},
		{`{"error":"access_denied"}`, RegisterDenied},
		{`{"error":"tenant_mismatch","user_info":{"tenant_brand":"lark"}}`, RegisterFailed},
		{`{"user_info":{"tenant_brand":"feishu"}}`, RegisterPending},
		{`{"client_id":"cli_new","client_secret":"sec","user_info":{"open_id":"ou_me","tenant_brand":"feishu"}}`, RegisterDone},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			if r.PostForm.Get("action") != "poll" || r.PostForm.Get("device_code") != "dc_1" {
				t.Errorf("poll form: %v", r.PostForm)
			}
			w.WriteHeader(http.StatusBadRequest) // errors come with a 4xx; the JSON is read all the same
			_, _ = io.WriteString(w, c.body)
		}))
		got, err := NewRegistrar().WithHost(srv.URL).Poll(context.Background(), "feishu", "dc_1")
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		if got.Status != c.want {
			t.Errorf("%s: status %q, want %q", c.body, got.Status, c.want)
		}
		if c.want == RegisterDone && (got.AppID != "cli_new" || got.AppSecret != "sec" || got.OpenID != "ou_me") {
			t.Errorf("done: %+v", got)
		}
	}
}

func TestAccountsHost(t *testing.T) {
	if AccountsHost("lark") != AccountsLark || AccountsHost("feishu") != AccountsFeishu || AccountsHost("") != AccountsFeishu {
		t.Fatal("accounts hosts")
	}
}

// beginWith runs BeginWith against a fake accounts site and returns the begin form and the decoded addons.
func beginWith(t *testing.T, opts larksyncwire.RegisterOptions) (url.Values, map[string]any, []byte) {
	t.Helper()
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != registrationPath {
			http.NotFound(w, r) // also a local node's port probe (GET /)
			return
		}
		_ = r.ParseForm()
		form = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "dc_1", "verification_uri_complete": "https://accounts.example.com/verify?user_code=UC",
			"expire_in": 600, "interval": 5})
	}))
	defer srv.Close()
	ch, err := NewRegistrar().WithHost(srv.URL).BeginWith(context.Background(), "feishu", "Persona", opts)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(ch.URL)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(u.Query().Get("addons"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(zr)
	var addons map[string]any
	if err := json.Unmarshal(payload, &addons); err != nil {
		t.Fatal(err)
	}
	return form, addons, payload
}

func strs(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

// Both tiers' user scopes go into the addons next to the bot's tenant scopes, which stay as they were.
func TestRegistrationBeginWithAsksForTheTiersUserScopes(t *testing.T) {
	for _, tier := range []string{larksyncwire.TierDocs, larksyncwire.TierFull} {
		want := larksyncwire.ScopesFor(tier)
		form, addons, payload := beginWith(t, larksyncwire.RegisterOptions{UserScopes: want})
		scopes := addons["scopes"].(map[string]any)
		if got := strs(scopes["user"]); !slices.Equal(got, want) {
			t.Errorf("%s: user scopes %v, want %v", tier, got, want)
		}
		if got := strs(scopes["tenant"]); !slices.Equal(got, registerScopes) {
			t.Errorf("%s: tenant scopes %v, want %v", tier, got, registerScopes)
		}
		if _, ok := addons["callbacks"]; !ok {
			t.Errorf("%s: addons lost the card callback: %s", tier, payload)
		}
		if _, ok := addons["app_id"]; ok || form.Has("client_id") {
			t.Errorf("%s: a new app names no existing one: form %v, addons %s", tier, form, payload)
		}
	}
	full, docs := larksyncwire.ScopesFor(larksyncwire.TierFull), larksyncwire.ScopesFor(larksyncwire.TierDocs)
	if !slices.Contains(full, "im:message:readonly") || slices.Contains(docs, "im:message:readonly") {
		t.Fatalf("tiers: docs %v, full %v", docs, full)
	}
}

// Zero options ask for exactly what Begin asks for: the same form and the same addons, byte for byte.
func TestRegistrationBeginWithZeroOptionsIsBegin(t *testing.T) {
	form, addons, payload := beginWith(t, larksyncwire.RegisterOptions{})
	want, err := json.Marshal(map[string]any{
		"scopes":    map[string]any{"tenant": registerScopes},
		"callbacks": map[string]any{"items": []string{"card.action.trigger"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, want) {
		t.Fatalf("addons = %s, want %s", payload, want)
	}
	if _, ok := addons["scopes"].(map[string]any)["user"]; ok {
		t.Fatalf("addons ask for user scopes: %s", payload)
	}
	if len(form) != 4 || form.Has("client_id") {
		t.Fatalf("form = %v, want Begin's four fields", form)
	}
}

// Adding user scopes to an existing app names it in the form and in the addons.
func TestRegistrationBeginWithAddsToAnExistingApp(t *testing.T) {
	scopes := larksyncwire.ScopesFor(larksyncwire.TierDocs)
	form, addons, payload := beginWith(t, larksyncwire.RegisterOptions{UserScopes: scopes, AppID: " cli_existing "})
	if form.Get("client_id") != "cli_existing" || form.Get("action") != "begin" {
		t.Fatalf("form = %v, want client_id cli_existing", form)
	}
	if addons["app_id"] != "cli_existing" {
		t.Fatalf("addons = %s, want app_id cli_existing", payload)
	}
	if got := strs(addons["scopes"].(map[string]any)["user"]); !slices.Equal(got, scopes) {
		t.Fatalf("user scopes %v, want %v", got, scopes)
	}
}
