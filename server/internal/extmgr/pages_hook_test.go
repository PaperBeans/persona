package extmgr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// fakePages is the assistant's pages as a test gives them: two places, and every call it was asked.
type fakePages struct {
	mu         sync.Mutex
	placements []string
	langs      []i18n.Lang
	calls      []fakePagesCall
	refuse     error
}

type fakePagesCall struct {
	ui, device string
	lang       i18n.Lang
	in         wire.UIRequest
}

func (f *fakePages) Places(_ context.Context, lang i18n.Lang, placement string) []UIPlace {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.placements = append(f.placements, placement)
	f.langs = append(f.langs, lang)
	return []UIPlace{
		{Extension: PagesExtension, ExtensionName: "Persona", ID: "index", Title: "Dashboards", Placement: "nav_page", Size: "full", Icon: "chart"},
		{Extension: PagesExtension, ExtensionName: "Persona", ID: "pg_0123456789abcdef", Title: "Morning run", Placement: "nav_page", Size: "full", Icon: "heart"},
	}
}

func (f *fakePages) UI(_ context.Context, lang i18n.Lang, ui string, in wire.UIRequest, device string) (extension.ActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakePagesCall{ui: ui, device: device, lang: lang, in: in})
	if f.refuse != nil {
		return extension.ActionResult{}, f.refuse
	}
	return extension.ActionResult{Toast: "drawn " + ui}, nil
}

func (f *fakePages) asked() ([]string, []fakePagesCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.placements), slices.Clone(f.calls)
}

// The assistant's pages (Dashboards, #15017) are listed after the extensions' own places, for a page placement only,
// drawn by the hook (never an extension's process) for a device with full access only, and never reached with an
// extension's token or over the native UI stream.
func TestPagesHook(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	_, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", master, `{"source": "dir:`+uiPackage(t)+`"}`)
	call(t, mux, "POST", "/api/v1/extensions/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"]))
	waitLong(t, "the extension up", func() bool {
		hp := h.extensions.get("example-ui")
		return hp != nil && hp.proc != nil && hp.proc.Ready()
	})
	pages := &fakePages{}
	h.opts.Pages = pages
	phone := deviceToken("phone", roleFull)
	watch := deviceToken("watch", roleAnswer)

	owners := func(list []any, key string) []string {
		var out []string
		for _, p := range list {
			out = append(out, fmt.Sprint(p.(map[string]any)[key]))
		}
		return out
	}
	// listed after the extensions' places
	code, all := call(t, mux, "GET", "/api/v1/extensions/-/ui", phone, "")
	list, _ := all["ui"].([]any)
	got := owners(list, "extension")
	if code != http.StatusOK || len(got) < 3 || got[len(got)-1] != PagesExtension || got[len(got)-2] != PagesExtension {
		t.Fatalf("the pages come last: %d %v", code, got)
	}
	first := slices.Index(got, PagesExtension)
	if first == 0 || slices.Contains(got[first:], "example-ui") {
		t.Fatalf("every extension place comes first: %v", got)
	}
	// the old paths name it the old way
	_, old := call(t, mux, "GET", "/api/v1/plugins/-/ui", phone, "")
	if l, _ := old["ui"].([]any); !slices.Contains(owners(l, "plugin"), PagesExtension) {
		t.Fatalf("the old path keeps its keys: %v", old)
	}
	// a page placement lists them, any other does not even ask
	for placement, want := range map[string]bool{"nav_page": true, "extension_page": true, "plugin_page": true, "overview_card": false, "machine_section": false, "widget": false} {
		before, _ := pages.asked()
		_, out := call(t, mux, "GET", "/api/v1/extensions/-/ui?placement="+placement, phone, "")
		l, _ := out["ui"].([]any)
		after, _ := pages.asked()
		if listed := slices.Contains(owners(l, "extension"), PagesExtension); listed != want || (len(after) > len(before)) != want {
			t.Fatalf("placement %s: listed %v asked %v, want %v", placement, listed, len(after) > len(before), want)
		}
		if want && after[len(after)-1] != placement {
			t.Fatalf("the hook is told the placement: %v", after)
		}
	}
	// a device that can only answer and an extension's token see none of it
	before, _ := pages.asked()
	if _, out := call(t, mux, "GET", "/api/v1/extensions/-/ui", watch, ""); slices.Contains(owners(out["ui"].([]any), "extension"), PagesExtension) {
		t.Fatalf("an answer-only device lists no pages: %v", out)
	}
	token := h.extTokens.issueAll("example-all", "Example all scopes")
	if code, _ := call(t, mux, "GET", "/api/v1/extensions/-/ui", token, ""); code != http.StatusForbidden {
		t.Fatalf("an extension's token is not let in: %d", code)
	}
	if code, _ := call(t, mux, "POST", "/api/v1/extensions/-pages/-/ui/index", token, `{}`); code != http.StatusForbidden {
		t.Fatalf("an extension's token draws no page: %d", code)
	}
	for _, p := range []principal{{ID: "extension:example-all", Role: roleExtension, Extension: "example-all"}, {ID: "x", Role: roleFull, Extension: "example-all"}, {ID: "watch", Role: roleAnswer}} {
		r := httptest.NewRequest("GET", "/api/v1/extensions/-/ui", nil).WithContext(withPrincipal(context.Background(), p))
		if out := h.pagesPlaces(r, ""); out != nil {
			t.Fatalf("%+v listed pages: %v", p, out)
		}
	}
	if after, _ := pages.asked(); len(after) != len(before) {
		t.Fatal("the hook was asked for a caller that gets none")
	}
	if _, open := extwire.RouteScopes["GET /api/v1/extensions/-/ui"]; open {
		t.Fatal("the places list is open to extensions' tokens")
	}

	// a POST goes to the hook, with the device and the request
	code, res := call(t, mux, "POST", "/api/v1/extensions/-pages/-/ui/pg_0123456789abcdef", phone, `{"action":"done","values":{"note":"easy"},"state":"{\"p\":\"\",\"v\":3}","context":{"ui_version":10,"locale":"ja"}}`)
	_, calls := pages.asked()
	if code != http.StatusOK || res["toast"] != "drawn pg_0123456789abcdef" || len(calls) != 1 {
		t.Fatalf("drawn by the hook: %d %v %v", code, res, calls)
	}
	c := calls[0]
	if c.ui != "pg_0123456789abcdef" || c.device != "phone" || c.in.Action != "done" || c.in.Values["note"] != "easy" || c.in.State != `{"p":"","v":3}` ||
		c.in.Context == nil || c.in.Context.UIVersion != 10 || c.lang != i18n.Ja {
		t.Fatalf("the hook gets the request: %+v", c)
	}
	// an explicit UI language wins over the body's
	req := httptest.NewRequest("POST", "/api/v1/extensions/-pages/-/ui/index", strings.NewReader(`{"context":{"locale":"ja"}}`))
	req.Header.Set("Authorization", "Bearer "+phone)
	req.Header.Set("Accept-Language", "en")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if _, calls = pages.asked(); w.Code != http.StatusOK || calls[len(calls)-1].lang != i18n.En {
		t.Fatalf("Accept-Language wins: %d %+v", w.Code, calls[len(calls)-1])
	}
	// a device that can only answer is refused before the hook
	if code, _ := call(t, mux, "POST", "/api/v1/extensions/-pages/-/ui/index", watch, `{}`); code != http.StatusForbidden {
		t.Fatalf("an answer-only device: %d", code)
	}
	if _, after := pages.asked(); len(after) != len(calls) {
		t.Fatal("the hook was asked for an answer-only device")
	}
	// the hook's refusals keep their status
	pages.refuse = &PagesError{Status: http.StatusNotFound, Code: "not_found", Err: errors.New("Dashboards are off")}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/-pages/-/ui/index", phone, `{}`); code != http.StatusNotFound || out["code"] != "not_found" {
		t.Fatalf("a refusal: %d %v", code, out)
	}
	// without the hook (another manager), the id is nobody's
	h.opts.Pages = nil
	if code, _ := call(t, mux, "POST", "/api/v1/extensions/-pages/-/ui/index", phone, `{}`); code != http.StatusNotFound {
		t.Fatalf("no hook: %d", code)
	}
	if _, out := call(t, mux, "GET", "/api/v1/extensions/-/ui", phone, ""); slices.Contains(owners(out["ui"].([]any), "extension"), PagesExtension) {
		t.Fatalf("no hook lists nothing: %v", out)
	}
}

// The native UI stream never opens a page of a reserved id: the apps draw those over HTTP.
func TestPagesNativeStreamRefused(t *testing.T) {
	h := newTestManager(t)
	who := extwire.UIPrincipal{Hub: "hub", Principal: "phone", Connection: "c1", Role: "full"}
	for _, id := range []string{PagesExtension, "-subscriptions", "-anything"} {
		req := wire.UIStreamRequest{Request: wire.UIStreamRequestOpen, Protocol: 1, RequestID: "open", Extension: id, Contribution: "index",
			Context: &wire.UIContext{UIVersion: 10, Stream: &wire.UIStreamCapability{Protocol: 1, Components: []string{"text"}}}}
		if _, err := h.pageOpen(context.Background(), who, req); pageFailureCode(err) != "negotiation" {
			t.Fatalf("%s: %v (%s)", id, err, pageFailureCode(err))
		}
	}
}
