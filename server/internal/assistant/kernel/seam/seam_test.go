package seam_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/kernel/seam"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

const (
	relayed = "GET /api/v1/assistant/saved-prompts"  // in assistantwire.Routes
	inbox   = "GET /api/v1/inbox"                    // in assistantwire.InboxRoutes
	other   = "POST /api/v1/assistant/saved-prompts" // in assistantwire.Routes
)

// ports are package assistant's ports as a test sees them: the routes installed, in order, on a mux of their own.
type ports struct {
	mux      *http.ServeMux
	order    []string
	full     bool
	bodies   []string
	reported []string
}

func newPorts() *ports { return &ports{mux: http.NewServeMux()} }

func (p *ports) Install(r seam.Route) { r.Accept(p) }

func (p *ports) VisitRoute(pattern string, serve http.HandlerFunc) {
	p.order = append(p.order, pattern)
	p.mux.HandleFunc(pattern, serve)
}

func (p *ports) Full(*http.Request) bool { return p.full }

func (p *ports) Write(w http.ResponseWriter, err, bad error) {
	p.bodies = append(p.bodies, err.Error()+" / "+bad.Error())
	w.WriteHeader(http.StatusBadRequest)
}

func (p *ports) Report(r *http.Request, d defect.Defect) {
	p.reported = append(p.reported, r.Pattern+" "+d.Error())
}

func (p *ports) registry() *seam.Registry { return seam.NewRegistry(p, p, p, p) }

// module is a test module: its name and what it does with its Kernel.
type module struct {
	name     func() seam.ModuleName
	register func(k seam.Kernel)
}

func (m module) Name() seam.ModuleName  { return m.name() }
func (m module) Register(k seam.Kernel) { m.register(k) }

func named(name string, register func(k seam.Kernel)) module {
	return module{name: func() seam.ModuleName { return seam.NewModuleName(name) }, register: register}
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// raises runs f and fails unless it raises a defect.Defect whose text contains want.
func raises(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		d, isDefect := recover().(defect.Defect)
		switch {
		case !isDefect:
			t.Errorf("want a defect.Defect containing %q, got none", want)
		case !strings.Contains(d.Error(), want):
			t.Errorf("defect %q does not say %q", d.Error(), want)
		}
	}()
	f()
}

func TestANilPortIsADefect(t *testing.T) {
	p := newPorts()
	raises(t, "seam.NewRegistry: the RouteTable port is nil", func() { seam.NewRegistry(nil, p, p, p) })
	raises(t, "seam.NewRegistry: the Access port is nil", func() { seam.NewRegistry(p, nil, p, p) })
	raises(t, "seam.NewRegistry: the BodyErrors port is nil", func() { seam.NewRegistry(p, p, nil, p) })
	raises(t, "seam.NewRegistry: the Defects port is nil", func() { seam.NewRegistry(p, p, p, nil) })
}

func TestModuleNames(t *testing.T) {
	for _, name := range []string{"saved", "inbox-tasks", "a1"} {
		seam.NewModuleName(name)
	}
	raises(t, "seam.NewModuleName: a module name is not empty", func() { seam.NewModuleName("") })
	for _, name := range []string{"Saved", "saved_prompts", "saved prompts", "saved/x", "sävd"} {
		raises(t, "has a character outside [a-z0-9-]", func() { seam.NewModuleName(name) })
	}
}

func TestAModuleRegistersOnce(t *testing.T) {
	reg := newPorts().registry()
	reg.Register(named("saved", func(seam.Kernel) {}))
	raises(t, `seam.Registry.Register: module "saved" is registered twice`, func() {
		reg.Register(named("saved", func(seam.Kernel) {}))
	})
	raises(t, "seam.Registry.Register: a ModuleName comes from NewModuleName", func() {
		reg.Register(module{name: func() seam.ModuleName { return seam.ModuleName{} }, register: func(seam.Kernel) {}})
	})
	raises(t, "seam.Registry.Register: the module is nil", func() { reg.Register(nil) })
}

func TestRoutesAreRelayedOnes(t *testing.T) {
	seam.NewRoute(relayed, ok)
	seam.NewRoute(inbox, ok)
	raises(t, `seam.NewRoute: "GET /api/v1/nowhere" is no route the hub relays`, func() { seam.NewRoute("GET /api/v1/nowhere", ok) })
	raises(t, `seam.NewRoute: "GET /api/v1/assistant/saved-prompts" has no handler`, func() { seam.NewRoute(relayed, nil) })
	raises(t, "seam.Route.Accept: a Route comes from NewRoute", func() { seam.Route{}.Accept(newPorts()) })
}

func TestARouteIsRegisteredOnce(t *testing.T) {
	p := newPorts()
	reg := p.registry()
	reg.Register(named("saved", func(k seam.Kernel) {
		k.Route(seam.NewRoute(relayed, ok))
		k.Route(seam.NewRoute(other, ok))
		raises(t, `seam.Kernel.Route: "GET /api/v1/assistant/saved-prompts" is registered twice, by "saved" and "saved"`, func() {
			k.Route(seam.NewRoute(relayed, ok))
		})
		raises(t, "seam.Kernel.Route: a Route comes from NewRoute", func() { k.Route(seam.Route{}) })
	}))
	reg.Register(named("inbox", func(k seam.Kernel) {
		raises(t, `seam.Kernel.Route: "POST /api/v1/assistant/saved-prompts" is registered twice, by "saved" and "inbox"`, func() {
			k.Route(seam.NewRoute(other, ok))
		})
		k.Route(seam.NewRoute(inbox, ok))
	}))
	if got, want := strings.Join(p.order, ", "), relayed+", "+other+", "+inbox; got != want {
		t.Errorf("installed %s, want %s (registration order)", got, want)
	}
}

func TestNothingRegistersAfterSeal(t *testing.T) {
	var kept seam.Kernel
	reg := newPorts().registry()
	reg.Register(named("saved", func(k seam.Kernel) { kept = k }))
	reg.Seal()
	raises(t, "seam.Registry.Register: the registry is sealed", func() { reg.Register(named("inbox", func(seam.Kernel) {})) })
	raises(t, "seam.Kernel.Route: the registry is sealed", func() { kept.Route(seam.NewRoute(relayed, ok)) })
	raises(t, "seam.Registry.Seal: the registry is sealed", reg.Seal)
	// What requests use stays open.
	kept.Full(httptest.NewRequest(http.MethodGet, "/", nil))
	kept.BodyError(httptest.NewRecorder(), errors.New("read"), errors.New("bad"))
}

func TestAZeroKernelOrRegistryIsADefect(t *testing.T) {
	var k seam.Kernel
	raises(t, "seam.Kernel.Route: a Kernel comes from Registry.Register", func() { k.Route(seam.NewRoute(relayed, ok)) })
	raises(t, "seam.Kernel.Full: a Kernel comes from Registry.Register", func() {
		k.Full(httptest.NewRequest(http.MethodGet, "/", nil))
	})
	raises(t, "seam.Kernel.BodyError: a Kernel comes from Registry.Register", func() {
		k.BodyError(httptest.NewRecorder(), errors.New("read"), errors.New("bad"))
	})
	var zero seam.Registry
	raises(t, "seam.Registry.Register: a Registry comes from NewRegistry", func() { zero.Register(named("saved", func(seam.Kernel) {})) })
	var none *seam.Registry
	raises(t, "seam.Registry.Seal: a Registry comes from NewRegistry", none.Seal)
}

func TestKernelServicesAreThePorts(t *testing.T) {
	p := newPorts()
	p.full = true
	p.registry().Register(named("saved", func(k seam.Kernel) {
		if !k.Full(httptest.NewRequest(http.MethodGet, "/", nil)) {
			t.Error("Full is not the Access port's answer")
		}
		k.BodyError(httptest.NewRecorder(), errors.New("too large"), errors.New("bad prompt"))
	}))
	if got := strings.Join(p.bodies, ";"); got != "too large / bad prompt" {
		t.Errorf("BodyErrors got %q", got)
	}
}

// A defect inside a registered route answers that request with 500 and is reported; the next request is served.
func TestADefectInARouteAnswers500(t *testing.T) {
	p := newPorts()
	reg := p.registry()
	calls := 0
	reg.Register(named("saved", func(k seam.Kernel) {
		k.Route(seam.NewRoute(relayed, func(w http.ResponseWriter, r *http.Request) {
			if calls++; calls == 1 {
				defect.Raise("saved.list", "a Host without a store")
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	}))
	reg.Seal()
	srv := httptest.NewServer(p.mux)
	defer srv.Close()
	for i, want := range []int{http.StatusInternalServerError, http.StatusNoContent} {
		res, err := http.Get(srv.URL + "/api/v1/assistant/saved-prompts")
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if res.StatusCode != want {
			t.Errorf("request %d: status %d, want %d", i+1, res.StatusCode, want)
		}
		if i == 0 && !strings.Contains(string(body), `"code":"internal"`) {
			t.Errorf("the defect's answer is %q, want the internal error body", body)
		}
	}
	if got, want := strings.Join(p.reported, ";"), relayed+" saved.list: a Host without a store"; got != want {
		t.Errorf("reported %q, want %q", got, want)
	}
}

// Any other panic is not the kernel's: it goes on, unchanged, to whoever serves the mux.
func TestAnyOtherPanicGoesOn(t *testing.T) {
	for _, value := range []any{errors.New("not a defect"), http.ErrAbortHandler} {
		p := newPorts()
		reg := p.registry()
		reg.Register(named("saved", func(k seam.Kernel) {
			k.Route(seam.NewRoute(relayed, func(http.ResponseWriter, *http.Request) { panic(value) }))
		}))
		func() {
			defer func() {
				if r := recover(); r != value {
					t.Errorf("the panic %v came out as %v", value, r)
				}
			}()
			p.mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/assistant/saved-prompts", nil))
		}()
		if len(p.reported) != 0 {
			t.Errorf("reported %q for a panic that is no defect", p.reported)
		}
	}
}
