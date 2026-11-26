package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func secretRunWatchCard(id, delivery string) proto.Interaction {
	input, _ := json.Marshal(map[string]any{
		"machine":      "mac",
		"machine_name": "Mac mini",
		"purpose":      "Authenticate the one-off LAN CLI",
		"commands":     []string{"printf 'ready\\n'"},
		"delivery":     delivery,
	})
	summary := "Run with the password you type.\n$ printf 'ready\\n'"
	if delivery == "env" {
		summary = "Every listed command receives PERSONA_SECRET.\n$ printf 'ready\\n'"
	}
	return proto.Interaction{
		ID:        id,
		MachineID: "server",
		SessionID: "s1",
		Kind:      proto.IxPermission,
		ToolName:  proto.SecretRunTool,
		ToolInput: input,
		Summary:   summary,
		Fields:    []proto.Field{{Key: "password", Label: "Password", Secret: true}},
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().Add(10 * time.Minute).UTC(),
	}
}

func secretRunWatchResolve(t *testing.T, h http.Handler, token, id, body, features string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/interactions/"+id+"/resolve", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+token)
	if features != "" {
		req.Header.Set("X-Persona-Features", features)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// An answer-only Watch credential can decline env delivery, never approve it. An approval without the env card's
// per-request feature is refused even with a value; a full client that declares it is relayed.
func TestSecretRunEnvWatchCanOnlyDecline(t *testing.T) {
	h := newTestHub(t)
	h.store = &tokenStore{memStore: *newMemStore(), rows: map[string]db.DeviceToken{}, rev: map[string]bool{}}
	mux := h.routes()
	_, phone := call(t, mux, http.MethodPost, "/api/v1/auth/tokens", h.cfg.Token, `{"name":"iPhone","kind":"phone"}`)
	_, watch := call(t, mux, http.MethodPost, "/api/v1/auth/tokens", phone["token"].(string), `{"name":"Watch","kind":"watch"}`)
	watchToken := watch["token"].(string)
	connectAssistant(t, h)

	const watchPassword = "watch-SECRET-must-not-be-relayed"
	for _, tc := range []struct {
		name, id, delivery, body, token, features string
		want                                      int
	}{
		{name: "quick env approval without the feature", id: "ix_env_watch_quick", delivery: "env", body: `{"allow":true}`, want: http.StatusForbidden},
		{name: "typed env approval without the feature", id: "ix_env_watch_typed", delivery: "env", body: `{"allow":true,"values":{"password":"` + watchPassword + `"}}`, want: http.StatusForbidden},
		{name: "env decline", id: "ix_env_watch_decline", delivery: "env", body: `{"allow":false}`, want: http.StatusGone},
		{name: "legacy stdin approval", id: "ix_stdin_watch", delivery: "stdin", body: `{"allow":true}`, want: http.StatusGone},
		{name: "full client with the env feature", id: "ix_env_full", delivery: "env", body: `{"allow":true}`, token: phone["token"].(string), features: "secret_run_env", want: http.StatusGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reportInteraction(t, h, secretRunWatchCard(tc.id, tc.delivery))
			token := tc.token
			if token == "" {
				token = watchToken
			}
			code, body := secretRunWatchResolve(t, mux, token, tc.id, tc.body, tc.features)
			if code != tc.want {
				t.Errorf("Watch answer %s: %d %v, want %d", tc.body, code, body, tc.want)
			}
			if tc.want == http.StatusForbidden {
				if body["code"] != "secret_run_env_elsewhere" {
					t.Errorf("unfeatured env approval code = %v, want secret_run_env_elsewhere", body["code"])
				}
				if _, pending := h.pendingInteraction(tc.id); !pending {
					t.Error("a refused Watch approval consumed the env card")
				}
			}
			if strings.Contains(tc.body, watchPassword) {
				if response, _ := json.Marshal(body); strings.Contains(string(response), watchPassword) {
					t.Errorf("the Watch response contains the password: %s", response)
				}
			}
		})
	}
}

// After a hub restart, before the manager re-reports the card, its proof still reaches the manager: an unknown
// env card's approval keeps the vouched device instead of being validated only as secret_handoff.
func TestSecretRunEnvUnknownCardKeepsItsApprover(t *testing.T) {
	h, m := handoffBusHub(t)
	mux := h.routes()
	_, phone := call(t, mux, http.MethodPost, "/api/v1/auth/tokens", h.cfg.Token, `{"name":"iPhone","kind":"phone"}`)
	code, body := secretRunWatchResolve(t, mux, phone["token"].(string), "ix_env_after_restart", `{"allow":true}`, "secret_run_env")
	if code != http.StatusOK {
		t.Fatalf("an approval after the hub forgot the env card: %d %v", code, body)
	}
	relayed, _, _ := m.took()
	if len(relayed) != 1 || relayed[0].HandoffApprover == nil || relayed[0].HandoffApprover.DeviceID != phone["id"] {
		t.Fatalf("the manager got approver %+v, want device %s", relayed, phone["id"])
	}
}
