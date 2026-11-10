package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type grantTestStore struct {
	*fleetMem
	fail bool
}

func grantHash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func (m *grantTestStore) AlwaysGrants(_ context.Context, session string) ([]db.AlwaysGrant, error) {
	if m.fail {
		return nil, errors.New("database unavailable")
	}
	m.fmu.Lock()
	defer m.fmu.Unlock()
	out := []db.AlwaysGrant{}
	for k := range m.always[session] {
		out = append(out, db.AlwaysGrant{KeyHash: grantHash(k), ToolName: "Bash"})
	}
	return out, nil
}

func (m *grantTestStore) RevokeAlways(_ context.Context, session, hash string) error {
	if m.fail {
		return errors.New("database unavailable")
	}
	m.fmu.Lock()
	defer m.fmu.Unlock()
	for k := range m.always[session] {
		if grantHash(k) == hash {
			delete(m.always[session], k)
		}
	}
	return nil
}

type grantTestHub struct {
	*fakeHub
	reset func(DeviceCall) (json.RawMessage, error)
}

func (h *grantTestHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == "assistant.permissions.reset" {
		return h.reset(p)
	}
	return h.fakeHub.DeviceCall(ctx, p)
}

func TestAlwaysGrantRoutes(t *testing.T) {
	for _, result := range []string{`{"ok":true,"restart_required":false}`, `{"ok":true,"restart_required":true}`, `{}`, "offline"} {
		t.Run(result, func(t *testing.T) {
			ft := newFleetTest(t)
			st := &grantTestStore{fleetMem: ft.st}
			ft.s.store = func() Store { return st }
			resets := 0
			setHub(ft.s, &grantTestHub{fakeHub: ft.f, reset: func(p DeviceCall) (json.RawMessage, error) {
				resets++
				if ok, _ := st.AlwaysAllowed(context.Background(), "conv", []string{"tool:Bash"}); ok {
					t.Fatal("reset before deletion")
				}
				var in struct {
					SessionID string `json:"session_id"`
				}
				_ = json.Unmarshal(p.Params, &in)
				if in.SessionID != "conv" {
					t.Fatalf("reset wrong conversation: %s", p.Params)
				}
				if result == "offline" {
					return nil, errors.New("offline")
				}
				return json.RawMessage(result), nil
			}})
			ctx := context.Background()
			_ = st.AllowAlways(ctx, "conv", "Bash", []string{"tool:Bash", "tool:Edit"})
			_ = st.AllowAlways(ctx, "other", "Bash", []string{"tool:Bash"})
			path := "/api/v1/assistant/conversations/conv/always"
			w := serve(ft.s, "GET", path, "")
			var list struct {
				Grants []db.AlwaysGrant `json:"grants"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list.Grants) != 2 {
				t.Fatalf("list: %d %s", w.Code, w.Body)
			}
			for range 2 { // Deleting again retries the process reset after a dropped response.
				w = serve(ft.s, "DELETE", path+"/"+grantHash("tool:Bash"), "")
				var out struct {
					OK      bool `json:"ok"`
					Restart bool `json:"restart_required"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &out)
				if w.Code != 200 || !out.OK || out.Restart != (result != `{"ok":true,"restart_required":false}`) {
					t.Fatalf("delete: %d %s", w.Code, w.Body)
				}
			}
			if resets != 2 {
				t.Fatalf("resets = %d", resets)
			}
			if _, ok := ft.s.alwaysAllowed(ctx, alwaysIx("next", "conv", "tool:Bash")); ok {
				t.Fatal("revoked tool still allowed")
			}
			for _, c := range []struct{ session, key string }{{"conv", "tool:Edit"}, {"other", "tool:Bash"}} {
				if ok, _ := st.AlwaysAllowed(ctx, c.session, []string{c.key}); !ok {
					t.Fatalf("unrelated grant removed: %+v", c)
				}
			}
		})
	}
}

func TestAlwaysGrantRouteErrors(t *testing.T) {
	ft := newFleetTest(t)
	st := &grantTestStore{fleetMem: ft.st}
	ft.s.store = func() Store { return st }
	base := "/api/v1/assistant/conversations/conv/always"
	for _, method := range []string{"GET", "DELETE"} {
		path := base
		if method == "DELETE" {
			path += "/" + grantHash("tool:Bash")
		}
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set(RoleHeader, "answer")
		w := httptest.NewRecorder()
		ft.s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("limited role: %d %s", w.Code, w.Body)
		}
	}
	for _, hash := range []string{"bad", strings.Repeat("A", 64)} {
		if w := serve(ft.s, "DELETE", base+"/"+hash, ""); w.Code != 400 {
			t.Fatalf("invalid hash: %d", w.Code)
		}
	}
	if w := serve(ft.s, "GET", "/api/v1/assistant/conversations/bad%20id/always", ""); w.Code != 400 {
		t.Fatalf("invalid conversation: %d", w.Code)
	}
	if w := serve(ft.s, "GET", base, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"grants":[]`) {
		t.Fatalf("empty list: %d %s", w.Code, w.Body)
	}
	st.fail = true
	for _, method := range []string{"GET", "DELETE"} {
		path := base
		if method == "DELETE" {
			path += "/" + grantHash("tool:Bash")
		}
		if w := serve(ft.s, method, path, ""); w.Code != 500 {
			t.Fatalf("store error: %d %s", w.Code, w.Body)
		}
	}
	ft.s.store = func() Store { return nil }
	if w := serve(ft.s, "GET", base, ""); w.Code != 503 {
		t.Fatalf("no store: %d", w.Code)
	}
}

func TestRememberedAnswerDoesNotRestoreRevokedGrant(t *testing.T) {
	ft := newFleetTest(t)
	st := &grantTestStore{fleetMem: ft.st}
	ft.s.store = func() Store { return st }
	ix := alwaysIx("waiting", "conv", "tool:Bash")
	done := make(chan proto.Resolution, 1)
	go func() { r, _ := ft.s.waitInteraction(context.Background(), ix); done <- r }()
	ft.waitPending(1)
	_ = st.AllowAlways(context.Background(), "conv", "Bash", ix.AlwaysKeys)
	res, ok := ft.s.alwaysAllowed(context.Background(), ix)
	if !ok {
		t.Fatal("grant not remembered")
	}
	_ = st.RevokeAlways(context.Background(), "conv", grantHash("tool:Bash"))
	if err := ft.s.resolveRemembered(ix.ID, res, false); err != nil {
		t.Fatal(err)
	}
	<-done
	if _, ok := ft.s.alwaysAllowed(context.Background(), ix); ok {
		t.Fatal("in-flight automatic answer restored a revoked grant")
	}
}
