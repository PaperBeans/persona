package saved_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/kernel/seam"
	"github.com/nwverse/persona/server/internal/assistant/saved"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// ports are the kernel's ports as package assistant gives them, on a mux of the test's own.
type ports struct {
	mux      *http.ServeMux
	patterns []string
	full     bool
	bodies   int
}

func (p *ports) Install(r seam.Route) { r.Accept(p) }
func (p *ports) VisitRoute(pattern string, serve http.HandlerFunc) {
	p.patterns = append(p.patterns, pattern)
	p.mux.HandleFunc(pattern, serve)
}
func (p *ports) Full(*http.Request) bool { return p.full }
func (p *ports) Write(w http.ResponseWriter, _, _ error) {
	p.bodies++
	w.WriteHeader(http.StatusBadRequest)
}
func (p *ports) Report(*http.Request, defect.Defect) {}

// settings is an empty settings store: what saved's searches read and write.
type settings struct{}

func (settings) Setting(context.Context, string, any) (bool, error)               { return false, nil }
func (settings) CompareSetting(context.Context, string, int64, any) (bool, error) { return true, nil }

func store() any                                 { return settings{} }
func events(context.Context, map[string]any)     {}
func raised(t *testing.T, want string, f func()) { t.Helper(); defer caught(t, want); f() }

func caught(t *testing.T, want string) {
	t.Helper()
	if d, ok := recover().(defect.Defect); !ok || !strings.Contains(d.Error(), want) {
		t.Errorf("want a defect saying %q, got %v", want, d)
	}
}

func TestNewModuleRefusesANilDependency(t *testing.T) {
	raised(t, "saved.NewModule: the store is nil", func() { saved.NewModule(nil, events) })
	raised(t, "saved.NewModule: the events are nil", func() { saved.NewModule(store, nil) })
	raised(t, "saved.Module.Register: a Module comes from NewModule", func() {
		p := &ports{mux: http.NewServeMux()}
		seam.NewRegistry(p, p, p, p).Register(saved.Module{})
	})
}

// saved registers the routes it served before the kernel, every one a route the hub relays, and they answer through
// the kernel's ports.
func TestModuleServesSavedRoutesThroughTheKernel(t *testing.T) {
	p := &ports{mux: http.NewServeMux()}
	reg := seam.NewRegistry(p, p, p, p)
	reg.Register(saved.NewModule(store, events))
	reg.Seal()
	want := []string{
		"GET /api/v1/assistant/saved-prompts", "POST /api/v1/assistant/saved-prompts",
		"PUT /api/v1/assistant/saved-prompts/order", "PUT /api/v1/assistant/saved-prompts/{id}",
		"DELETE /api/v1/assistant/saved-prompts/{id}",
		"GET /api/v1/saved-agent-searches", "POST /api/v1/saved-agent-searches",
		"GET /api/v1/assistant/starred", "POST /api/v1/assistant/starred", "DELETE /api/v1/assistant/starred/{id}",
		"GET " + assistantwire.StarredImportPath, "POST " + assistantwire.StarredImportPreviewPath,
		"POST " + assistantwire.StarredImportPath,
	}
	got, sorted := slices.Clone(p.patterns), slices.Clone(want)
	slices.Sort(got)
	slices.Sort(sorted)
	if !slices.Equal(got, sorted) {
		t.Fatalf("saved registered %q\nwant %q", p.patterns, want)
	}

	// A device that may not do everything cannot add a saved search: Full is the Access port's answer.
	rec := httptest.NewRecorder()
	p.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/saved-agent-searches", strings.NewReader("{}")))
	if rec.Code != http.StatusForbidden {
		t.Errorf("a limited device's write answered %d, want %d", rec.Code, http.StatusForbidden)
	}
	// A full device's unreadable body goes to the BodyErrors port.
	p.full = true
	rec = httptest.NewRecorder()
	p.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/saved-agent-searches", errReader{}))
	if p.bodies != 1 || rec.Code != http.StatusBadRequest {
		t.Errorf("an unreadable body answered %d with %d BodyErrors writes, want 400 and 1", rec.Code, p.bodies)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken body") }
