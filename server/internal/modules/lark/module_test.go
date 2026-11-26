package lark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/modapi"
)

// routeHost records the routes a module serves.
type routeHost struct {
	modapi.Host
	routes map[string]http.HandlerFunc
	order  []string
}

func (h *routeHost) AddHubRoute(pattern string, f http.HandlerFunc) error {
	h.routes[pattern] = f
	h.order = append(h.order, pattern)
	return nil
}

func TestModuleInfo(t *testing.T) {
	info := (Module{}).Info()
	if info.ID != "lark" || info.API != 1 || info.Origin != "builtin" || info.Feature != "module.lark" {
		t.Fatalf("%+v", info)
	}
	h := &routeHost{routes: map[string]http.HandlerFunc{}}
	if err := (Module{}).Register(h); err != nil {
		t.Fatal(err)
	}
	// Exactly the routes the manifest declares (the kernel refuses any other), in the same order.
	if !slices.Equal(h.order, info.HubRoutes) || len(h.order) != 4 {
		t.Fatalf("registered %v, manifest %v", h.order, info.HubRoutes)
	}
}

// Without a layer on the request the routes answer 404, as with the module off.
func TestModuleRoutesWithoutALayer(t *testing.T) {
	for _, m := range []Module{{}, NewModule(func(context.Context) *Layer { return nil })} {
		h := &routeHost{routes: map[string]http.HandlerFunc{}}
		if err := m.Register(h); err != nil {
			t.Fatal(err)
		}
		for pattern, f := range h.routes {
			method, path, _ := strings.Cut(pattern, " ")
			w := httptest.NewRecorder()
			f(w, httptest.NewRequest(method, path, strings.NewReader("{}")))
			if w.Code != http.StatusNotFound {
				t.Errorf("%s without a layer: %d, want 404", pattern, w.Code)
			}
		}
	}
}

// With a layer the companion routes answer from it: a read-only device is refused, a bad companion is a 400.
func TestModuleRoutesServeTheLayer(t *testing.T) {
	s := newTestService(t, newFakeHub())
	h := &routeHost{routes: map[string]http.HandlerFunc{}}
	if err := (NewModule(func(context.Context) *Layer { return s.layer })).Register(h); err != nil {
		t.Fatal(err)
	}
	call := func(pattern, body, role string) int {
		method, path, _ := strings.Cut(pattern, " ")
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if role != "" {
			r.Header.Set("X-Persona-Role", role)
		}
		w := httptest.NewRecorder()
		h.routes[pattern](w, r)
		return w.Code
	}
	if code := call(routeCompanions, `{"bots":[]}`, "answer"); code != http.StatusForbidden {
		t.Errorf("read-only role: %d, want 403", code)
	}
	bad, _ := json.Marshal(map[string]any{"bots": []CompanionBot{{Kind: "robot", Name: "X", AppID: "cli_inboxbot01"}}})
	if code := call(routeCompanions, string(bad), ""); code != http.StatusBadRequest {
		t.Errorf("unknown kind: %d, want 400", code)
	}
}

func TestDivertNoticesRaisesDefectForUnknownNoticeKind(t *testing.T) {
	s := newTestService(t, newFakeHub())
	defer func() {
		r := recover()
		if err, ok := r.(error); !ok || err.Error() != `assistantwire.NoticeKind: unknown notice kind ""` {
			t.Errorf("DivertNotices panic = %v, want an unknown notice kind defect", r)
		}
	}()
	s.layer.DivertNotices(t.Context(), assistantwire.NoticeKind{}, nil, nil)
}
