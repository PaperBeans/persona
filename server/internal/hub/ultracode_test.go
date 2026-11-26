package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestSessionModesSplitsUltracode(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		methods    []string
		params     []map[string]any
	}{
		{"speed", `{"service_tier":"priority"}`, []string{"session.speed"}, []map[string]any{{"session_id": "s1", "service_tier": "priority"}}},
		{"on", `{"ultracode":true}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "ultracode": true}}},
		{"off", `{"ultracode":false}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "ultracode": false}}},
		{"after model and effort", `{"model":"opus","effort":"low","permission_mode":"auto","ultracode":true,"session_id":"wrong"}`,
			[]string{"session.modes", "session.ultracode"}, []map[string]any{
				{"session_id": "s1", "model": "opus", "effort": "low", "permission_mode": "auto"},
				{"session_id": "s1", "ultracode": true}}},
		{"legacy", `{"effort":"high"}`, []string{"session.modes"}, []map[string]any{{"session_id": "s1", "effort": "high"}}},
		{"dismiss", `{"ultracode_dismiss_lost":true}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "dismiss_lost": true}}},
		{"reopen and dismiss", `{"ultracode":true,"ultracode_dismiss_lost":true}`, []string{"session.ultracode"}, []map[string]any{{"session_id": "s1", "ultracode": true, "dismiss_lost": true}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHub(t)
			var methods []string
			var params []map[string]any
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
				methods, params = append(methods, method), append(params, p)
				return map[string]any{"ok": true, "method": method}, nil
			})
			w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", tt.body)
			if w.Code != http.StatusOK || !reflect.DeepEqual(methods, tt.methods) || !reflect.DeepEqual(params, tt.params) {
				t.Fatalf("%d %s; methods=%v params=%v", w.Code, w.Body, methods, params)
			}
			if !strings.Contains(w.Body.String(), tt.methods[len(tt.methods)-1]) {
				t.Fatalf("last response was dropped: %s", w.Body)
			}
		})
	}
}

func TestSessionModesUltracodeErrors(t *testing.T) {
	for _, tt := range []struct {
		name, body, failMethod, code string
		err                          error
		wantStatus, wantCalls        int
	}{
		{"old node", `{"ultracode":true}`, "session.ultracode", "node_outdated", errors.New("unknown method session.ultracode"), 409, 1},
		{"old node dismissal", `{"ultracode_dismiss_lost":true}`, "session.ultracode", "node_outdated", errors.New("unknown method session.ultracode"), 409, 1},
		{"refused", `{"ultracode":true}`, "session.ultracode", "ultracode_unsupported", &rpc.Error{Code: "ultracode_unsupported", Message: "模型不支持"}, 409, 1},
		{"effort failed", `{"effort":"high","ultracode":true}`, "session.modes", "input_draft", &rpc.Error{Code: "input_draft", Message: "输入框有草稿"}, 409, 1},
		{"string", `{"ultracode":"on"}`, "", "", nil, 400, 0},
		{"null", `{"ultracode":null}`, "", "", nil, 400, 0},
		{"invalid dismissal", `{"effort":"high","ultracode_dismiss_lost":1}`, "", "", nil, 400, 0},
		{"null body", `null`, "", "", nil, 400, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHub(t)
			calls := 0
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, _ map[string]any) (any, error) {
				calls++
				if method == tt.failMethod {
					return nil, tt.err
				}
				return map[string]any{"ok": true}, nil
			})
			w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", tt.body)
			if w.Code != tt.wantStatus || calls != tt.wantCalls || (tt.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tt.code+`"`)) {
				t.Fatalf("%d %s; %d calls", w.Code, w.Body, calls)
			}
		})
	}
}

func TestSessionStartForwardsUltracode(t *testing.T) {
	for _, value := range []string{"true", "false", ""} {
		t.Run("value="+value, func(t *testing.T) {
			h := newTestHub(t)
			var got map[string]any
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
				if method == "session.start" {
					got = p
					return map[string]any{"session": proto.Session{SessionID: "s1", Starting: true}}, nil
				}
				return map[string]any{}, nil
			})
			connectAssistant(t, h)
			body := `{"machine_id":"box","cwd":"/code","launcher":"claude","tab_name":"work","async":true`
			if value != "" {
				body += `,"ultracode":` + value
			}
			w := modelsRequest(h, "POST", "/api/v1/sessions", body+`}`)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"starting":true`) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			v, exists := got["ultracode"]
			if (value != "") != exists || (exists && v != (value == "true")) {
				t.Fatalf("node got %v", got)
			}
		})
	}
}

func TestSessionSpeedValidation(t *testing.T) {
	for _, body := range []string{`{"service_tier":true}`, `{"service_tier":""}`, `{"service_tier":"priority","effort":"low"}`} {
		h := newTestHub(t)
		calls := 0
		modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) { calls++; return nil, nil })
		w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", body)
		if w.Code != http.StatusBadRequest || calls != 0 {
			t.Fatalf("%s: %d calls=%d", body, w.Code, calls)
		}
	}
	h := newTestHub(t)
	modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
		return nil, errors.New("unknown method session.speed")
	})
	w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", `{"service_tier":"priority"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "node_outdated") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// When a combined /modes request succeeds on session.modes and then fails on session.ultracode, the hub preserves
// delivered="unknown" and partial=true so client_ops records "unknown" (not "failed") and users are asked to check
// current settings instead of being told nothing changed.
func TestSessionModesPartialUltracodeRefusalPreservesUnknownReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, opID, wantCode, wantKey string
		err                           error
	}{
		{
			name:     "dialog_open",
			opID:     "00000000-0000-4000-8000-000000000601",
			wantCode: "dialog_open",
			wantKey:  "rpc-errors.modes_partial_dialog_open",
			err:      &rpc.Error{Code: "dialog_open", Message: "电脑上的终端正开着一个对话框（确认、选择列表等），这条消息没有发出去：先回答或关掉它再发。", Data: json.RawMessage(`{"delivered":"none"}`)},
		},
		{
			name:     "not_delivered",
			opID:     "00000000-0000-4000-8000-000000000602",
			wantCode: "not_delivered",
			wantKey:  "rpc-errors.modes_partial_not_delivered",
			err:      &rpc.Error{Code: "not_delivered", Message: "电脑上的终端这次没有收下这条消息，什么都没输入：稍后再发。", Data: json.RawMessage(`{"delivered":"none","reason":"terminal_refused"}`)},
		},
		{
			name:     "node_outdated",
			opID:     "00000000-0000-4000-8000-000000000603",
			wantCode: proto.CodeNodeOutdated,
			wantKey:  "rpc-errors.modes_partial_node_outdated",
			err:      errors.New("unknown method session.ultracode"),
		},
		{
			name:     "ultracode_unsupported",
			opID:     "00000000-0000-4000-8000-000000000610",
			wantCode: "ultracode_unsupported",
			wantKey:  "rpc-errors.modes_partial_failed",
			err:      &rpc.Error{Code: "ultracode_unsupported", Message: "Claude Code version does not support ultracode"},
		},
		{
			name:     "null_rpc_data",
			opID:     "00000000-0000-4000-8000-000000000607",
			wantCode: "dialog_open",
			wantKey:  "rpc-errors.modes_partial_dialog_open",
			err:      &rpc.Error{Code: "dialog_open", Message: "dialog open", Data: json.RawMessage(`null`)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &clientOpsStore{sharedStore: newSentStore(), ops: map[string]db.ClientOp{}}
			h := newTestHub(t)
			h.store = store
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, _ map[string]any) (any, error) {
				if method == "session.ultracode" {
					return nil, tc.err
				}
				return map[string]any{"ok": true}, nil
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/box/s1/modes", strings.NewReader(`{"model":"opus","ultracode":true}`))
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Language", "en")
			req.Header.Set("X-Persona-Op-Id", tc.opID)
			w := httptest.NewRecorder()
			h.routes().ServeHTTP(w, req)
			var out map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
			}
			if w.Code != http.StatusConflict || out["code"] != tc.wantCode || out["delivered"] != "unknown" || out["partial"] != true {
				t.Fatalf("response = %d %v, want 409 code=%q delivered=unknown partial=true", w.Code, out, tc.wantCode)
			}
			wantErr := i18n.T(i18n.En, tc.wantKey)
			if out["error"] != wantErr {
				t.Fatalf("error = %q, want %q (body=%v)", out["error"], wantErr, out)
			}
			op, err := store.ClientOp(context.Background(), tc.opID)
			if err != nil || op == nil || op.State != "unknown" {
				t.Fatalf("client_op = %+v (err=%v), want state=unknown", op, err)
			}
		})
	}

	// Transport timeout or offline error on the second RPC after session.modes succeeded preserves partial=true and
	// marks retryable=false so clients do not automatically replay settings that already took effect.
	t.Run("timeout preserves partial flag and non-retryable", func(t *testing.T) {
		const opID = "00000000-0000-4000-8000-000000000604"
		store := &clientOpsStore{sharedStore: newSentStore(), ops: map[string]db.ClientOp{}}
		h := newTestHub(t)
		h.store = store
		modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, _ map[string]any) (any, error) {
			if method == "session.ultracode" {
				return nil, context.DeadlineExceeded
			}
			return map[string]any{"ok": true, "changed": true}, nil
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/box/s1/modes", strings.NewReader(`{"model":"opus","ultracode":true}`))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", "en")
		req.Header.Set("X-Persona-Op-Id", opID)
		w := httptest.NewRecorder()
		h.routes().ServeHTTP(w, req)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
		}
		if w.Code != http.StatusBadGateway || out["code"] != "timeout" || out["delivered"] != "unknown" || out["partial"] != true || out["retryable"] != false || out["error"] != i18n.T(i18n.En, "rpc-errors.modes_partial_failed") {
			t.Fatalf("timeout response = %d %v, want 502 timeout delivered=unknown partial=true retryable=false partial summary", w.Code, out)
		}
		op, err := store.ClientOp(context.Background(), opID)
		if err != nil || op == nil || op.State != "unknown" {
			t.Fatalf("client_op = %+v (err=%v), want state=unknown", op, err)
		}
	})

	t.Run("offline second RPC clears retryable on partial response", func(t *testing.T) {
		const opID = "00000000-0000-4000-8000-000000000609"
		store := &clientOpsStore{sharedStore: newSentStore(), ops: map[string]db.ClientOp{}}
		h := newTestHub(t)
		h.store = store
		modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, _ map[string]any) (any, error) {
			if method == "session.modes" {
				// Simulate the node going offline after session.modes completes and before session.ultracode starts.
				m := h.byID["box"]
				m.mu.Lock()
				m.peer = nil
				m.mu.Unlock()
				return map[string]any{"ok": true, "changed": true}, nil
			}
			return nil, errors.New("unexpected call after disconnect")
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/box/s1/modes", strings.NewReader(`{"model":"opus","ultracode":true}`))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept-Language", "en")
		req.Header.Set("X-Persona-Op-Id", opID)
		w := httptest.NewRecorder()
		h.routes().ServeHTTP(w, req)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
		}
		if w.Code != http.StatusServiceUnavailable || out["code"] != "node_unreachable" || out["delivered"] != "unknown" || out["partial"] != true || out["retryable"] != false || out["error"] != i18n.T(i18n.En, "rpc-errors.modes_partial_failed") || out["detail"] == nil {
			t.Fatalf("offline partial response = %d %v, want 503 node_unreachable delivered=unknown partial=true retryable=false partial summary with detail", w.Code, out)
		}
	})

	// When session.modes was a no-op (empty permission_mode, or node confirms changed=false), a refused ultracode step
	// keeps delivered="none" and records client_ops as "failed" without claiming earlier settings changed.
	for _, tc := range []struct {
		name, opID, body string
		modesReply       map[string]any
	}{
		{
			name:       "empty permission_mode is no-op",
			opID:       "00000000-0000-4000-8000-000000000605",
			body:       `{"permission_mode":"","ultracode":true}`,
			modesReply: map[string]any{"ok": true},
		},
		{
			name:       "node changed=false is no-op",
			opID:       "00000000-0000-4000-8000-000000000606",
			body:       `{"permission_mode":"plan","ultracode":true}`,
			modesReply: map[string]any{"ok": true, "applied": "now", "changed": false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &clientOpsStore{sharedStore: newSentStore(), ops: map[string]db.ClientOp{}}
			h := newTestHub(t)
			h.store = store
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, _ map[string]any) (any, error) {
				if method == "session.ultracode" {
					return nil, &rpc.Error{Code: "dialog_open", Message: "dialog open", Data: json.RawMessage(`{"delivered":"none"}`)}
				}
				return tc.modesReply, nil
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/box/s1/modes", strings.NewReader(tc.body))
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Language", "en")
			req.Header.Set("X-Persona-Op-Id", tc.opID)
			w := httptest.NewRecorder()
			h.routes().ServeHTTP(w, req)
			var out map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
			}
			if w.Code != http.StatusConflict || out["code"] != "dialog_open" || out["delivered"] != "none" || out["partial"] != nil {
				t.Fatalf("no-op response = %d %v, want 409 dialog_open delivered=none without partial", w.Code, out)
			}
			op, err := store.ClientOp(context.Background(), tc.opID)
			if err != nil || op == nil || op.State != "failed" {
				t.Fatalf("client_op = %+v (err=%v), want state=failed", op, err)
			}
		})
	}

	// Replaying a stored refusal receipt under another Accept-Language translates the error summary for the current
	// request without rerunning the operation or changing the stored receipt.
	t.Run("replay localizes stored refusal in current request language", func(t *testing.T) {
		const opID = "00000000-0000-4000-8000-000000000608"
		store := &clientOpsStore{sharedStore: newSentStore(), ops: map[string]db.ClientOp{}}
		h := newTestHub(t)
		h.store = store
		calls := 0
		modelsNode(t, h, MachineConfig{ID: "box"}, func(string, map[string]any) (any, error) {
			calls++
			return nil, &rpc.Error{
				Code:    "not_delivered",
				Message: "电脑上的终端这次没有收下这条消息，什么都没输入：稍后再发。",
				Data:    json.RawMessage(`{"delivered":"none","reason":"terminal_refused"}`),
			}
		})
		send := func(lang string) (int, map[string]any) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/box/s1/modes", strings.NewReader(`{"effort":"high"}`))
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Language", lang)
			req.Header.Set("X-Persona-Op-Id", opID)
			w := httptest.NewRecorder()
			h.routes().ServeHTTP(w, req)
			var out map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
			}
			return w.Code, out
		}
		if status, out := send("en"); status != http.StatusConflict || out["error"] != i18n.T(i18n.En, "rpc-errors.not_delivered") {
			t.Fatalf("first en response = %d %v", status, out)
		}
		if status, out := send("ja"); status != http.StatusConflict || out["error"] != i18n.T(i18n.Ja, "rpc-errors.not_delivered") || out["code"] != "not_delivered" || out["delivered"] != "none" {
			t.Fatalf("replayed ja response = %d %v, want Japanese not_delivered", status, out)
		}
		if calls != 1 {
			t.Fatalf("node calls = %d, want 1 (replayed from receipt)", calls)
		}
	})
}
