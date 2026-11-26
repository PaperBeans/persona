package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	recoveryops "github.com/nwverse/persona/server/internal/operations/recovery"
)

type fakeRecoveryService struct {
	overview recoveryops.Overview
	check    recoveryops.Check
	err      error
	calls    []string
}

func (f *fakeRecoveryService) Overview(context.Context) (recoveryops.Overview, error) {
	f.calls = append(f.calls, "overview")
	return f.overview, f.err
}
func (f *fakeRecoveryService) Inspect(_ context.Context, id string) (recoveryops.Check, error) {
	f.calls = append(f.calls, "inspect:"+id)
	return f.check, f.err
}
func (f *fakeRecoveryService) Verify(_ context.Context, id string) (recoveryops.Check, error) {
	f.calls = append(f.calls, "verify:"+id)
	return f.check, f.err
}

func TestRecoveryRoutesRequireFullCredentialsAndAuditOpaqueChecks(t *testing.T) {
	id := strings.Repeat("a", 64)
	service := &fakeRecoveryService{
		overview: recoveryops.Overview{Bundles: []recoveryops.Bundle{}},
		check: recoveryops.Check{Kind: "offline_integrity", State: "passed", CheckedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Bundle: recoveryops.Bundle{ID: id, Name: "private-bundle-name.tar", Revision: "revision"}},
	}
	var logs bytes.Buffer
	h := newTestHub(t)
	h.recovery = service
	h.logger = log.New(&logs, "", 0)
	mux := http.NewServeMux()
	h.recoveryRoutes(mux)

	request := func(method, path, role string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{ID: "device-fixture", Role: role}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := request(http.MethodGet, "/api/v1/hub/recovery", roleAnswer); w.Code != http.StatusForbidden || len(service.calls) != 0 {
		t.Fatalf("answer-only credential read recovery metadata: %d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, "/api/v1/hub/recovery", roleFull); w.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", w.Code, w.Body.String())
	}
	if w := request(http.MethodPost, "/api/v1/hub/recovery/bundles/"+id+"/inspect", roleFull); w.Code != http.StatusOK {
		t.Fatalf("inspect: %d %s", w.Code, w.Body.String())
	}
	if got := strings.Join(service.calls, ","); got != "overview,inspect:"+id {
		t.Fatalf("unexpected calls: %s", got)
	}
	if !strings.Contains(logs.String(), "recovery: action=inspect device=device-fixture result=passed") ||
		strings.Contains(logs.String(), "private-bundle-name") || strings.Contains(logs.String(), h.cfg.dir) {
		t.Fatalf("audit is missing or leaks private bundle data: %q", logs.String())
	}

	for _, path := range []string{
		"/api/v1/hub/recovery/create", "/api/v1/hub/recovery/restore",
		"/api/v1/hub/recovery/bundles/" + id + "/download", "/api/v1/hub/recovery/bundles/" + id + "/keys",
	} {
		if w := request(http.MethodPost, path, roleFull); w.Code != http.StatusNotFound {
			t.Fatalf("unsafe recovery route exists: %s -> %d", path, w.Code)
		}
	}
}

func TestRecoveryRoutesReturnSafeFailures(t *testing.T) {
	service := &fakeRecoveryService{err: errors.New("postgres://synthetic:secret@example.test/private?password=secret")}
	h := newTestHub(t)
	h.recovery = service
	mux := http.NewServeMux()
	h.recoveryRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/hub/recovery", nil))
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "example.test") {
		t.Fatalf("unsafe failure response: %d %s", w.Code, w.Body.String())
	}

	service.err = recoveryops.ErrInvalidBundleID
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/hub/recovery/bundles/not-an-id/inspect", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid opaque id: %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == "postgres://synthetic:secret@example.test/private?password=secret" {
		t.Fatalf("unsafe error body: %s", w.Body.String())
	}

	service.err = recoveryops.ErrBusy
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/hub/recovery/bundles/"+strings.Repeat("a", 64)+"/verify", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("a concurrent check must be refused, not stacked: %d %s", w.Code, w.Body.String())
	}
}
