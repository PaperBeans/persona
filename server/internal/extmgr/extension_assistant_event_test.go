package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/host"
	"github.com/nwverse/persona/server/internal/extension/sandbox"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/rpc"
)

// assistant.event (extension_assistant_event.go, docs/extensions/extensions.md "Assistant events"): only with the
// assistant.events scope, at a limited rate, and forwarded to the assistant with the extension's own identity.

type eventAsk struct {
	method string
	params json.RawMessage
}

// recordAssistant makes h's assistant answer with answer, and returns what it was asked.
func recordAssistant(h *Manager, answer func(eventAsk) (json.RawMessage, error)) func() []eventAsk {
	var mu sync.Mutex
	var asked []eventAsk
	h.opts.Assistant = func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		a := eventAsk{method, append(json.RawMessage(nil), params...)}
		mu.Lock()
		asked = append(asked, a)
		mu.Unlock()
		return answer(a)
	}
	return func() []eventAsk {
		mu.Lock()
		defer mu.Unlock()
		return append([]eventAsk(nil), asked...)
	}
}

func rpcCode(err error) string {
	var re *rpc.Error
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

func TestExtensionAssistantEventNeedsItsGrant(t *testing.T) {
	h := newTestManager(t)
	asked := recordAssistant(h, func(eventAsk) (json.RawMessage, error) { return json.RawMessage(`{"delivery":"delivered"}`), nil })
	granted := kbExtensionSpec(t, h, "example-events", extension.Permissions{Hub: []string{extension.ScopeAssistantEvents}})
	full := kbExtensionSpec(t, h, "example-full", extension.Permissions{HubAPI: true})
	params := json.RawMessage(`{"key":"disk","title":"Disk almost full"}`)
	for name, scope := range map[string]string{"assistant.read": extension.ScopeAssistantRead, "assistant.ask": extension.ScopeAssistantAsk,
		"notify": extension.ScopeNotify, "agents.control": extension.ScopeAgentsControl, "none": ""} {
		var perms extension.Permissions
		if scope != "" {
			perms.Hub = []string{scope}
		}
		spec := kbExtensionSpec(t, h, "example-"+strings.ReplaceAll(name, ".", "-"), perms)
		_, err := spec.Handle(context.Background(), assistantwire.MethodAssistantEvent, params)
		var re *rpc.Error
		if !errors.As(err, &re) || re.Code != extension.CodePermissionDenied || !strings.Contains(string(re.Data), `"assistant.events"`) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := len(asked()); n != 0 {
		t.Fatalf("a refused call reached the assistant: %v", asked())
	}
	// Granted by name, or by the legacy full grant (whose token can already message the main conversation).
	for _, spec := range []host.Spec{granted, full} {
		if _, err := spec.Handle(context.Background(), assistantwire.MethodAssistantEvent, params); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(asked()); n != 2 {
		t.Fatalf("%d calls reached the assistant", n)
	}
	// Switched off, it is no longer let through.
	h.extensions.reg.Get("example-events").Record.Enabled = false
	if _, err := granted.Handle(context.Background(), assistantwire.MethodAssistantEvent, params); rpcCode(err) != extension.CodePermissionDenied {
		t.Fatalf("a switched-off extension still tells: %v", err)
	}
}

func TestExtensionAssistantEventForwardsWithTheExtensionsOwnIdentity(t *testing.T) {
	h := newTestManager(t)
	asked := recordAssistant(h, func(a eventAsk) (json.RawMessage, error) {
		if strings.Contains(string(a.params), "no-main") {
			return nil, &rpc.Error{Code: assistantwire.CodeNoMainConversation, Message: "there is no main conversation"}
		}
		return json.RawMessage(`{"delivery":"replaced"}`), nil
	})
	spec := kbExtensionSpec(t, h, "example-events", extension.Permissions{Hub: []string{extension.ScopeAssistantEvents}})
	// Whatever the program puts in its params, who it is is the manager's word, and the params go on as they are.
	params := `{"key":"disk","title":"t","extension":"someone-else","name":"Someone"}`
	out, err := spec.Handle(context.Background(), assistantwire.MethodAssistantEvent, json.RawMessage(params))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(out); string(b) != `{"delivery":"replaced"}` {
		t.Fatalf("the assistant's answer: %s", b)
	}
	got := asked()
	if len(got) != 1 || got[0].method != assistantwire.MethodExtensionEvent {
		t.Fatalf("asked %+v", got)
	}
	var req assistantwire.ExtensionEvent
	if err := json.Unmarshal(got[0].params, &req); err != nil || req.Extension != "example-events" || req.Name != "Example" || string(req.Params) != params {
		t.Fatalf("the assistant got %+v (%v)", req, err)
	}
	// Its refusals reach the program with their codes.
	_, err = spec.Handle(context.Background(), assistantwire.MethodAssistantEvent, json.RawMessage(`{"key":"no-main","title":"t"}`))
	if rpcCode(err) != assistantwire.CodeNoMainConversation {
		t.Fatalf("no main conversation: %v", err)
	}
	// The assistant away: try again.
	h.opts.Assistant = nil
	if _, err := spec.Handle(context.Background(), assistantwire.MethodAssistantEvent, json.RawMessage(params)); rpcCode(err) != assistantwire.CodeAssistantOffline {
		t.Fatalf("assistant away: %v", err)
	}
}

func TestExtensionAssistantEventIsRateLimited(t *testing.T) {
	h := newTestManager(t)
	asked := recordAssistant(h, func(eventAsk) (json.RawMessage, error) { return json.RawMessage(`{"delivery":"queued"}`), nil })
	a := kbExtensionSpec(t, h, "example-a", extension.Permissions{Hub: []string{extension.ScopeAssistantEvents}})
	b := kbExtensionSpec(t, h, "example-b", extension.Permissions{Hub: []string{extension.ScopeAssistantEvents}})
	none := kbExtensionSpec(t, h, "example-none", extension.Permissions{})
	now := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	lim := &h.extensions.eventLimit
	lim.now, lim.window, lim.most = func() time.Time { return now }, 10*time.Minute, 3
	params := json.RawMessage(`{"key":"k","title":"t"}`)
	send := func(spec host.Spec) error {
		_, err := spec.Handle(context.Background(), assistantwire.MethodAssistantEvent, params)
		return err
	}
	for i := range 3 {
		now = now.Add(time.Minute)
		if err := send(a); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	now = now.Add(time.Minute) // the first call was 3 minutes ago
	err := send(a)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != assistantwire.CodeRateLimited {
		t.Fatalf("the fourth in the window: %v", err)
	}
	var data struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if json.Unmarshal(re.Data, &data) != nil || data.RetryAfter != (7*time.Minute).Seconds() {
		t.Fatalf("retry_after %s: the first call, 3 minutes before, leaves the window in 7 minutes", re.Data)
	}
	if n := len(asked()); n != 3 {
		t.Fatalf("a limited call reached the assistant: %d", n)
	}
	// Each extension has its own count, and one that may not tell is refused before it is counted.
	if err := send(b); err != nil {
		t.Fatalf("another extension: %v", err)
	}
	for range 5 {
		if rpcCode(send(none)) != extension.CodePermissionDenied {
			t.Fatal("a refusal")
		}
	}
	// The window slides: when the first call is out of it there is room again for one.
	now = now.Add(7 * time.Minute)
	if err := send(a); err != nil {
		t.Fatalf("after the first left the window: %v", err)
	}
	if rpcCode(send(a)) != assistantwire.CodeRateLimited {
		t.Fatal("the window holds three")
	}
	// The default is a limit that stops a loop and lets a monitor speak.
	if assistantEventMost < 5 || assistantEventMost > 60 || assistantEventWindow < time.Minute {
		t.Fatalf("limit %d in %s", assistantEventMost, assistantEventWindow)
	}
}

// serveEventExtensionForTest is a real extension program: on its HTTP side it calls assistant.event over its process
// connection with the body it is given and says how that went. It is given no hub URL or token.
func serveEventExtensionForTest() {
	ln, err := net.Listen("unix", os.Getenv("PERSONA_EXTENSION_SOCKET"))
	if err != nil {
		os.Exit(1)
	}
	var mu sync.Mutex
	var hub *pserve.Hub
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		h := hub
		mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		out := map[string]any{"hub_url_env": os.Getenv("PERSONA_HUB_URL")}
		var res assistantwire.AssistantEventResult
		if err := h.Call(r.Context(), "assistant.event", json.RawMessage(body), &res); err != nil {
			var re *rpc.Error
			if errors.As(err, &re) {
				out["code"], out["data"] = re.Code, string(re.Data)
			} else {
				out["code"] = err.Error()
			}
		} else {
			out["delivery"] = res.Delivery
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "eventer", Version: "1.0.0",
		Init: func(_ context.Context, _ extension.InitializeParams, h *pserve.Hub) (map[string]any, error) {
			mu.Lock()
			hub = h
			mu.Unlock()
			return nil, nil
		}})
}

func eventPackage(t *testing.T, hub string) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "self"), b, 0o755) // a sandbox sees its package only
	_ = os.WriteFile(filepath.Join(dir, "bin", "eventer"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=eventer exec \"$(dirname \"$0\")/self\" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": "eventer", "name": "Eventer", "version": "1.0.0",
		"engines": {"plugin_protocol": 1}, "runtime": {"command": {%q: "bin/eventer"}, "http": true, "resources": {"memory_mb": 256}},
		"activation_events": ["on_startup"], "contributes": {"api": [{"id": "x", "prefix": "eventerx"}]},
		"permissions": {"hub": %s}}`, runtime.GOOS+"-"+runtime.GOARCH, hub)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

// A real program, installed from a package and run (in the sandbox, where there is one), tells the assistant over its
// process connection alone: no HTTP to the hub, which the sandbox does not open to it. It is held to its grant and its
// rate, and the assistant hears who it is from the manager.
func TestExtensionAssistantEventFromARealProgram(t *testing.T) {
	for _, boxed := range []bool{false, true} {
		name := map[bool]string{false: "unsandboxed", true: "sandboxed"}[boxed]
		t.Run(name, func(t *testing.T) {
			if boxed {
				if runtime.GOOS != "linux" {
					t.Skip("the sandbox for real is Linux's here")
				}
				if sup := sandbox.Probe(); !sup.Available() {
					t.Skip("no sandbox here: " + sup.Why)
				}
			}
			h, _ := extensionTestHub(t)
			h.extensions.sandboxes.noDefault = !boxed
			asked := recordAssistant(h, func(a eventAsk) (json.RawMessage, error) {
				if strings.Contains(string(a.params), "no-main") {
					return nil, &rpc.Error{Code: assistantwire.CodeNoMainConversation, Message: "there is no main conversation"}
				}
				return json.RawMessage(`{"delivery":"delivered"}`), nil
			})
			h.extensions.eventLimit.most = 4
			mux := h.routes()
			master := h.cfg.Token
			code, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", master, `{"source": "dir:`+eventPackage(t, `["assistant.events"]`)+`"}`)
			if code != 200 {
				t.Fatalf("preview: %d %v", code, pv)
			}
			if b, _ := json.Marshal(pv["permissions"]); !strings.Contains(string(b), "Persona 主对话") {
				t.Fatalf("the install screen says what it may do: %s", b)
			}
			if code, out := call(t, mux, "POST", "/api/v1/extensions/-/install", master,
				fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); code != 200 {
				t.Fatalf("install: %d %v", code, out)
			}
			tell := func(body string) map[string]any {
				var out map[string]any
				waitLong(t, "the extension answering", func() bool {
					hp := h.extensions.get("eventer")
					if hp == nil || hp.proc == nil || !hp.proc.Ready() {
						return false
					}
					var code int
					code, out = call(t, mux, "POST", "/api/v1/extensions/eventer/event", master, body)
					return code == 200
				})
				return out
			}
			got := tell(`{"key":"disk","severity":"critical","title":"Disk almost full","body":"92% used"}`)
			if got["delivery"] != "delivered" || got["code"] != nil {
				t.Fatalf("the program's call: %v", got)
			}
			ev := asked()
			if len(ev) != 1 || ev[0].method != assistantwire.MethodExtensionEvent {
				t.Fatalf("the assistant was asked %+v", ev)
			}
			var req assistantwire.ExtensionEvent
			if err := json.Unmarshal(ev[0].params, &req); err != nil || req.Extension != "eventer" || req.Name != "Eventer" ||
				!strings.Contains(string(req.Params), `"Disk almost full"`) {
				t.Fatalf("what the assistant got: %+v (%v)", req, err)
			}
			if got := tell(`{"key":"no-main","title":"t"}`); got["code"] != assistantwire.CodeNoMainConversation {
				t.Fatalf("the assistant's refusal reaches the program: %v", got)
			}
			tell(`{"key":"k","title":"t"}`)
			tell(`{"key":"k","title":"t"}`)
			if got := tell(`{"key":"k","title":"t"}`); got["code"] != assistantwire.CodeRateLimited || !strings.Contains(fmt.Sprint(got["data"]), "retry_after") {
				t.Fatalf("a program that loops is stopped: %v", got)
			}
			if n := len(asked()); n != 4 {
				t.Fatalf("%d calls reached the assistant, four were allowed", n)
			}
		})
	}
}
