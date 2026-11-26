package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// serveOffloadExtensionForTest is example-cloud's program: a disk_offload provider that writes each call's method and
// params as a line of JSON to OFFLOAD_OUT. offload.run asks for its tool until a run says install; a path ending in
// "refuse-me" it refuses itself; the computer "garbled" gets an inventory that breaks the schema; the job "old" cannot
// be canceled.
func serveOffloadExtensionForTest() {
	out := os.Getenv("OFFLOAD_OUT")
	var mu sync.Mutex
	note := func(method string, params json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			b, _ := json.Marshal(map[string]any{"method": method, "params": params})
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
	tool := &wire.OffloadTool{Name: "example-cli", Title: "Example Cloud tool", Installed: false,
		Source: "Downloaded from the Example Cloud release page into ~/.local/bin."}
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "example-cloud", Version: "1.0.0",
		Namespaces: map[string]pserve.Handler{
			"offload": func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				note(method, params)
				switch method {
				case "inventory":
					var p wire.OffloadInventoryParams
					_ = json.Unmarshal(params, &p)
					if p.Machine.ID == "garbled" {
						return map[string]any{"state": "ready", "at": 1, "total_bytes": 1, "items": []any{map[string]any{"id": "x", "name": "x", "kind": "photo", "bytes": 1, "restorable": true}}}, nil
					}
					return wire.OffloadInventory{State: wire.OffloadStateReady, At: 1767000000, TotalBytes: 9000, Partial: p.Refresh,
						Items: []wire.OffloadItem{{ID: "f-1", Name: "Raw footage", Kind: "dir", Bytes: 9000, OriginPath: "/Users/me/Movies/Raw", Restorable: true}}}, nil
				case "run":
					var p wire.OffloadRunParams
					_ = json.Unmarshal(params, &p)
					for _, path := range p.Paths {
						if strings.HasSuffix(path, "refuse-me") {
							return wire.OffloadRunResult{Status: wire.OffloadRunStatusRefused, Items: []wire.OffloadRunItem{{Path: path, Reason: "Too big for your plan.", Refusal: "credentials"}}}, nil
						}
					}
					switch {
					case p.DryRun:
						var items []wire.OffloadRunItem
						for _, path := range p.Paths {
							items = append(items, wire.OffloadRunItem{Path: path, Ok: true, Bytes: 4096})
						}
						return wire.OffloadRunResult{Status: wire.OffloadRunStatusOk, Items: items, Tool: tool}, nil
					case p.Action == wire.OffloadActionOffload && !p.Install:
						return wire.OffloadRunResult{Status: wire.OffloadRunStatusNeedsInstall, Tool: tool}, nil
					}
					return wire.OffloadRunResult{Status: wire.OffloadRunStatusStarted, Job: "job-1"}, nil
				case "job":
					return wire.OffloadJob{State: "running", Phase: "upload", DoneBytes: 10, TotalBytes: 100}, nil
				case "cancel":
					var p wire.OffloadJobParams
					_ = json.Unmarshal(params, &p)
					if p.Job == "old" {
						return nil, pserve.UnknownMethod(method)
					}
					return wire.Empty{}, nil
				}
				return nil, pserve.UnknownMethod(method)
			}}})
}

// offloadPackage is example-cloud's package, its calls written to out.
func offloadPackage(t *testing.T, out string) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "cloud"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=offload exec "+self+" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": "example-cloud", "name": "Example Cloud", "version": "1.0.0",
		"activation_events": ["on_startup"], "engines": {"plugin_protocol": 1},
		"runtime": {"command": {%q: "bin/cloud"}, "env": {"OFFLOAD_OUT": %q}},
		"contributes": {"disk_offload": [{"id": "cloud", "name": "Example Cloud", "kinds": ["file", "dir", "app"], "restore": true}]}}`,
		runtime.GOOS+"-"+runtime.GOARCH, out)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

// offloadMac is a computer whose node answers machine.storage.check: a path with .ssh in it is refused as
// credentials. It counts the checks.
func offloadMac(t *testing.T, h *Manager, id string, caps []string, online bool) *int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hubSide, nodeSide := rpc.New("h", h2nW), rpc.New("n", n2hW)
	var mu sync.Mutex
	checks := new(int)
	nodeSide.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != proto.MethodMachineStorageCheck {
			return nil, &rpc.Error{Message: "unknown method " + method}
		}
		mu.Lock()
		*checks++
		mu.Unlock()
		var p proto.StorageCheckParams
		_ = json.Unmarshal(params, &p)
		if p.Purpose != proto.StoragePurposeOffload {
			return nil, &rpc.Error{Code: "bad_request", Message: "purpose"}
		}
		var res proto.StorageCheckResult
		for _, path := range p.Paths {
			it := proto.StorageCheckItem{Path: path, OK: true, Kind: "dir", Bytes: 4096}
			if rest, ok := strings.CutPrefix(path, "~/"); ok {
				it.Path = "/Users/me/" + rest // the node answers with the path it resolved and judged
			}
			if strings.Contains(path, ".ssh") {
				it.OK, it.Refusal = false, proto.RefusalCredentials
			}
			res.Items = append(res.Items, it)
		}
		return res, nil
	}
	go func() { _ = hubSide.Serve(ctx, n2hR) }()
	go func() { _ = nodeSide.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); _ = h2nW.Close(); _ = n2hW.Close() })
	m := newMachine(h, MachineConfig{ID: id, Name: "Studio " + id})
	m.peer, m.online, m.info = hubSide, online, proto.NodeInfo{OS: "darwin", Protocol: nodeproto.Version, Capabilities: caps}
	h.fleetMu.Lock()
	h.machines, h.byID[id] = append(h.machines, m), m
	h.fleetMu.Unlock()
	return checks
}

// offloadCalls are the calls the program wrote, as method and params.
func offloadCalls(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, _ := os.ReadFile(path)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var c map[string]any
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// The five routes with a provider installed: listing calls no program, every call carries the computer and the
// language, an offload goes through the computer's protection rules first and asks before installing the tool.
func TestOffloadRoutes(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	out := filepath.Join(t.TempDir(), "calls.jsonl")
	c, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+offloadPackage(t, out)+`"}`)
	if c != 200 {
		t.Fatalf("preview %d %v", c, pv)
	}
	if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}
	checks := offloadMac(t, h, "mac", nodeproto.Announced, true)
	offloadMac(t, h, "garbled", nodeproto.Announced, true)
	old := slices.DeleteFunc(slices.Clone(nodeproto.Announced), func(c string) bool { return c == nodeproto.MachineStorageUsage })
	offloadMac(t, h, "old", old, true)
	offloadMac(t, h, "off", nodeproto.Announced, false)
	base := "/api/v1/extensions/example-cloud/-/offload/cloud"
	waitLong(t, "the provider answering", func() bool {
		c, _ := call(t, mux, "GET", base+"/inventory?machine=mac", master, "")
		return c == 200
	})

	c, list := call(t, mux, "GET", "/api/v1/extensions/-/offload?machine=mac", master, "")
	if c != 200 || fmt.Sprint(list["providers"]) != "[map[extension:example-cloud id:cloud kinds:[file dir app] name:Example Cloud restore:true]]" {
		t.Fatalf("providers %d %v", c, list)
	}
	if c, _ := call(t, mux, "GET", "/api/v1/extensions/-/offload?machine=nowhere", master, ""); c != 404 {
		t.Errorf("an unknown computer: %d", c)
	}

	before := len(offloadCalls(t, out))
	c, inv := call(t, mux, "GET", base+"/inventory?machine=Studio%20mac&refresh=1", master, "")
	if c != 200 || inv["state"] != "ready" || inv["partial"] != true || len(inv["items"].([]any)) != 1 {
		t.Fatalf("inventory %d %v", c, inv)
	}
	calls := offloadCalls(t, out)
	last := calls[len(calls)-1]["params"].(map[string]any)
	if len(calls) != before+1 || last["contribution"] != "cloud" || last["locale"] == "" || last["refresh"] != true ||
		fmt.Sprint(last["machine"]) != "map[id:mac name:Studio mac online:true os:darwin]" {
		t.Fatalf("what offload.inventory was told: %v", last)
	}
	if c, body := call(t, mux, "GET", base+"/inventory?machine=garbled", master, ""); c != 502 || body["code"] != "plugin_failed" {
		t.Errorf("an answer that breaks the schema: %d %v", c, body)
	}

	run := func(body string) (int, map[string]any) { return call(t, mux, "POST", base+"/run", master, body) }
	// A dry run is checked on the computer and answered with the program's verdicts.
	c, dry := run(`{"machine": "mac", "action": "offload", "paths": ["/Users/me/Movies/Raw"], "dry_run": true}`)
	if c != 200 || dry["status"] != "ok" || len(dry["items"].([]any)) != 1 || *checks != 1 {
		t.Fatalf("dry run %d %v (checks %d)", c, dry, *checks)
	}
	// The computer's own rules refuse before the program hears of it.
	before = len(offloadCalls(t, out))
	c, refused := run(`{"machine": "mac", "action": "offload", "paths": ["/Users/me/Movies/Raw", "/Users/me/.ssh"], "confirm": true}`)
	items, _ := refused["items"].([]any)
	if c != 422 || refused["code"] != "offload_refused" || len(items) != 1 || items[0].(map[string]any)["refusal"] != "credentials" ||
		items[0].(map[string]any)["reason"] == "" || len(offloadCalls(t, out)) != before {
		t.Fatalf("refused by the computer %d %v", c, refused)
	}
	// The program refuses some itself.
	if c, body := run(`{"machine": "mac", "action": "offload", "paths": ["/Users/me/refuse-me"], "confirm": true}`); c != 422 || body["code"] != "offload_refused" ||
		!strings.Contains(fmt.Sprint(body["items"]), "Too big") || strings.Contains(fmt.Sprint(body["items"]), "refusal") {
		t.Fatalf("refused by the program %d %v", c, body)
	}
	// The tool first: nothing moves until the user agrees to install it.
	c, need := run(`{"machine": "mac", "action": "offload", "paths": ["/Users/me/Movies/Raw"], "confirm": true}`)
	tool, _ := need["tool"].(map[string]any)
	if c != 409 || need["code"] != "tool_install_required" || tool["name"] != "example-cli" || tool["source"] == "" || need["error"] == "" {
		t.Fatalf("needs install %d %v", c, need)
	}
	c, started := run(`{"machine": "mac", "action": "offload", "paths": ["~/Movies/Raw"], "confirm": true, "install": true}`)
	if c != 202 || started["job"] != "job-1" {
		t.Fatalf("started %d %v", c, started)
	}
	calls = offloadCalls(t, out)
	last = calls[len(calls)-1]["params"].(map[string]any)
	// The program is handed the paths the computer's rules judged, not the client's spelling of them.
	if last["install"] != true || last["request_id"] == "" || last["action"] != "offload" || fmt.Sprint(last["paths"]) != "[/Users/me/Movies/Raw]" {
		t.Fatalf("what offload.run was told: %v", last)
	}
	// Two spellings of one path are one path named twice.
	if c, body := run(`{"machine": "mac", "action": "offload", "paths": ["~/Movies/Raw", "/Users/me/Movies/Raw"], "dry_run": true}`); c != 400 || body["code"] != "bad_request" {
		t.Fatalf("one path twice %d %v", c, body)
	}
	// A restore names items and needs no computer check.
	n := *checks
	if c, body := run(`{"machine": "off", "action": "restore", "items": ["f-1"], "confirm": true}`); c != 202 || *checks != n {
		t.Fatalf("restore %d %v", c, body)
	}

	if c, job := call(t, mux, "GET", base+"/jobs/job-1?machine=mac", master, ""); c != 200 || job["phase"] != "upload" || job["items"] == nil {
		t.Fatalf("job %d %v", c, job)
	}
	if c, body := call(t, mux, "DELETE", base+"/jobs/job-1?machine=mac", master, ""); c != 200 || body["canceled"] != true {
		t.Fatalf("cancel %d %v", c, body)
	}
	if c, body := call(t, mux, "DELETE", base+"/jobs/old?machine=mac", master, ""); c != 200 || body["canceled"] != false {
		t.Fatalf("a job that cannot be canceled %d %v", c, body)
	}

	// What the manager refuses itself.
	many := `["/a` + strings.Repeat(`", "/a`, proto.StoragePathsMost) + `"]`
	for _, c := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", base + "/run", `{"machine": "mac", "action": "offload", "paths": ["/Users/me/x"]}`, 400, "confirmation_required"},
		{"POST", base + "/run", `{"machine": "mac", "action": "delete", "paths": ["/Users/me/x"], "confirm": true}`, 400, "bad_request"},
		{"POST", base + "/run", `{"machine": "mac", "action": "offload", "paths": [], "confirm": true}`, 400, "bad_request"},
		{"POST", base + "/run", `{"machine": "mac", "action": "offload", "items": ["f-1"], "confirm": true}`, 400, "bad_request"},
		{"POST", base + "/run", `{"machine": "mac", "action": "offload", "paths": ` + many + `, "confirm": true}`, 400, "bad_request"},
		{"POST", base + "/run", `{"machine": "mac", "action": "offload", "paths": ["/x"], "confirm": true, "sneaky": 1}`, 400, "bad_request"},
		{"POST", base + "/run", `{"machine": "nowhere", "action": "offload", "paths": ["/x"], "confirm": true}`, 404, "not_found"},
		{"POST", base + "/run", `{"machine": "off", "action": "offload", "paths": ["/x"], "confirm": true}`, 503, "machine_offline"},
		{"POST", base + "/run", `{"machine": "old", "action": "offload", "paths": ["/x"], "confirm": true}`, 409, "node_outdated"},
		{"GET", "/api/v1/extensions/example-cloud/-/offload/other/inventory?machine=mac", "", 404, "not_found"},
		{"GET", "/api/v1/extensions/nobody/-/offload/cloud/inventory?machine=mac", "", 404, "not_found"},
		{"GET", base + "/inventory", "", 404, "not_found"},
	} {
		code, body := call(t, mux, c.method, c.path, master, c.body)
		if code != c.status || body["code"] != c.code || body["error"] == "" {
			t.Errorf("%s %s %s: %d %v, want %d %s", c.method, c.path, c.body, code, body, c.status, c.code)
		}
	}

	// Only a device with full credentials.
	for _, p := range []principal{{ID: "watch", Role: roleAnswer}, {Role: roleFull, Extension: "example-cloud"}} {
		for _, r := range []*http.Request{httptest.NewRequest("GET", "/api/v1/extensions/-/offload", nil),
			httptest.NewRequest("GET", base+"/inventory?machine=mac", nil),
			httptest.NewRequest("POST", base+"/run", strings.NewReader(`{"machine": "mac", "action": "offload", "paths": ["/x"], "confirm": true}`))} {
			rec := httptest.NewRecorder()
			h.apiMux.ServeHTTP(rec, r.WithContext(withPrincipal(r.Context(), p)))
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"read_only"`) {
				t.Errorf("%+v %s: %d %s", p, r.URL, rec.Code, rec.Body)
			}
		}
	}
}
