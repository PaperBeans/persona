package devices

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// usageNode answers the usage map's methods with what the test sets, and keeps what it was asked.
type usageNode struct {
	mu      sync.Mutex
	methods []string
	params  []json.RawMessage
	answer  any
	fail    error
}

func usageMachine(t *testing.T, h *Devices, id string, caps []string) (*Machine, *usageNode) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hubSide, nodeSide := rpc.New("h", h2nW), rpc.New("n", n2hW)
	n := &usageNode{}
	nodeSide.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		n.mu.Lock()
		defer n.mu.Unlock()
		switch method {
		case proto.MethodMachineStorageUsage, proto.MethodMachineStorageTrash, proto.MethodMachineStorageUntrash, proto.MethodMachineStoragePurge:
			n.methods = append(n.methods, method)
			n.params = append(n.params, params)
			if n.fail != nil {
				return nil, n.fail
			}
			return n.answer, nil
		case "node.configure":
			return map[string]any{}, nil
		}
		return nil, &rpc.Error{Message: "unknown method " + method}
	}
	go func() { _ = hubSide.Serve(ctx, n2hR) }()
	go func() { _ = nodeSide.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); _ = h2nW.Close(); _ = n2hW.Close() })
	m := newMachine(h, MachineConfig{ID: id, Name: id})
	m.peer, m.online = hubSide, true
	m.info = proto.NodeInfo{OS: "darwin", Version: "test", Protocol: nodeproto.Version, Capabilities: caps}
	h.addEntry(m)
	return m, n
}

func TestUsageMapPassesThrough(t *testing.T) {
	h := newTestDevices(t)
	_, n := usageMachine(t, h, "mac", nodeproto.Announced)
	mux := h.Handler(http.NotFoundHandler())

	n.answer = proto.StorageUsageReport{At: 1767000000, OS: "darwin", State: proto.StorageUsageScanning, Home: "/Users/me",
		Volume: proto.StorageUsageVolume{Mount: "/", TotalBytes: 100, UsedBytes: 60, FreeBytes: 40},
		Node:   proto.StorageUsageNode{ID: "group:root", Kind: proto.UsageKindRoot, Category: proto.UsageCategoryOther, Bytes: 100, HasChildren: true}}
	code, out := clipCall(t, mux, "GET", "/api/v1/machines/mac/storage/usage?path=~/Movies&rescan=1&wait_ms=99999", "", "full")
	if code != http.StatusOK || out["machine_id"] != "mac" || out["state"] != "scanning" {
		t.Fatalf("%d %v", code, out)
	}
	node := out["node"].(map[string]any)
	if node["can_trash"] != false || node["has_children"] != true || node["partial"] != false {
		t.Errorf("the booleans are not always written: %v", node)
	}
	var up proto.StorageUsageParams
	_ = json.Unmarshal(n.params[0], &up)
	if up.Path != "~/Movies" || !up.Rescan || up.WaitMS != proto.StorageUsageWaitMostMS {
		t.Errorf("node asked %+v", up)
	}

	n.answer = proto.StorageTrashResult{PendingBytes: 9, Items: []proto.StorageTrashItem{{Path: "/Users/me/a", Display: "~/a", Bytes: 9, Status: proto.StorageTrashTrashed, TrashID: "t1"}}}
	code, out = clipCall(t, mux, "POST", "/api/v1/machines/mac/storage/trash", `{"paths":["/Users/me/a"],"confirm":true}`, "full")
	if code != http.StatusOK || out["pending_bytes"] != float64(9) || out["machine_id"] != "mac" {
		t.Fatalf("trash %d %v", code, out)
	}
	n.answer = proto.StorageUntrashResult{Restored: []proto.StorageRestoredItem{{Path: "~/a"}}}
	if code, out = clipCall(t, mux, "POST", "/api/v1/machines/mac/storage/trash/undo", `{"trash_ids":["t1"]}`, "full"); code != http.StatusOK {
		t.Fatalf("undo %d %v", code, out)
	}
	n.answer = proto.StoragePurgeResult{FreedBytes: 9, Items: []proto.StoragePurgeItem{{TrashID: "t1", Status: proto.StoragePurgePurged}}}
	if code, out = clipCall(t, mux, "POST", "/api/v1/machines/mac/storage/trash/purge", `{"trash_ids":["t1"],"confirm":true}`, "full"); code != http.StatusOK || out["freed_bytes"] != float64(9) {
		t.Fatalf("purge %d %v", code, out)
	}
	if !slices.Equal(n.methods, []string{proto.MethodMachineStorageUsage, proto.MethodMachineStorageTrash, proto.MethodMachineStorageUntrash, proto.MethodMachineStoragePurge}) {
		t.Errorf("node asked %v", n.methods)
	}
}

// What the manager refuses itself never reaches the node.
func TestUsageMapRefusals(t *testing.T) {
	h := newTestDevices(t)
	_, n := usageMachine(t, h, "mac", nodeproto.Announced)
	_, oldN := usageMachine(t, h, "old", slices.DeleteFunc(slices.Clone(nodeproto.Announced), func(c string) bool { return c == nodeproto.MachineStorageUsage }))
	off, _ := usageMachine(t, h, "off", nodeproto.Announced)
	off.online = false
	mux := h.Handler(http.NotFoundHandler())
	many := `{"confirm":true,"paths":["` + strings.Repeat(`/a","`, proto.StoragePathsMost) + `/a"]}`
	huge := `{"confirm":true,"paths":["` + strings.Repeat("x", usageMapBodyMost) + `"]}`
	for _, c := range []struct {
		method, path, body, role string
		status                   int
		code                     string
	}{
		{"POST", "/api/v1/machines/mac/storage/trash", `{"paths":["/Users/me/a"]}`, "full", http.StatusBadRequest, proto.CodeConfirmationRequired},
		{"POST", "/api/v1/machines/mac/storage/trash", `{"paths":[],"confirm":true}`, "full", http.StatusBadRequest, "bad_request"},
		{"POST", "/api/v1/machines/mac/storage/trash", many, "full", http.StatusBadRequest, "bad_request"},
		{"POST", "/api/v1/machines/mac/storage/trash", huge, "full", http.StatusBadRequest, "bad_request"},
		{"POST", "/api/v1/machines/mac/storage/trash", `not json`, "full", http.StatusBadRequest, "bad_request"},
		{"POST", "/api/v1/machines/mac/storage/trash/purge", `{"trash_ids":["t1"]}`, "full", http.StatusBadRequest, proto.CodeConfirmationRequired},
		{"POST", "/api/v1/machines/mac/storage/trash/undo", `{"trash_ids":[]}`, "full", http.StatusBadRequest, "bad_request"},
		{"GET", "/api/v1/machines/mac/storage/usage?wait_ms=soon", "", "full", http.StatusBadRequest, "bad_request"},
		{"GET", "/api/v1/machines/old/storage/usage", "", "full", http.StatusConflict, proto.CodeNodeOutdated},
		{"POST", "/api/v1/machines/old/storage/trash", `{"paths":["/a"],"confirm":true}`, "full", http.StatusConflict, proto.CodeNodeOutdated},
		{"GET", "/api/v1/machines/off/storage/usage", "", "full", http.StatusServiceUnavailable, devicewire.CodeMachineOffline},
		{"GET", "/api/v1/machines/mac/storage/usage", "", "answer", http.StatusForbidden, proto.CodeReadOnly},
		{"POST", "/api/v1/machines/mac/storage/trash", `{"paths":["/a"],"confirm":true}`, "extension", http.StatusForbidden, proto.CodeReadOnly},
		{"POST", "/api/v1/machines/mac/storage/trash/purge", `{"trash_ids":["t"],"confirm":true}`, "answer", http.StatusForbidden, proto.CodeReadOnly},
		{"GET", "/api/v1/machines/nowhere/storage/usage", "", "full", http.StatusNotFound, "not_found"},
	} {
		code, out := clipCall(t, mux, c.method, c.path, c.body, c.role)
		if code != c.status || out["code"] != c.code {
			t.Errorf("%s %s (%s): %d %v, want %d %s", c.method, c.path, c.role, code, out, c.status, c.code)
		}
		if msg, _ := out["error"].(string); msg == "" {
			t.Errorf("%s %s: no error text", c.method, c.path)
		}
	}
	if len(n.methods) != 0 || len(oldN.methods) != 0 {
		t.Fatalf("the node was asked: %v / %v", n.methods, oldN.methods)
	}
}

func TestUsageMapNodeErrors(t *testing.T) {
	h := newTestDevices(t)
	_, n := usageMachine(t, h, "mac", nodeproto.Announced)
	mux := h.Handler(http.NotFoundHandler())
	for _, c := range []struct {
		fail   error
		status int
		code   string
	}{
		{&rpc.Error{Message: "unknown method machine.storage.usage"}, http.StatusConflict, proto.CodeNodeOutdated},
		{&rpc.Error{Code: "bad_request", Message: "not a folder the map opens"}, http.StatusBadRequest, "bad_request"},
		{&rpc.Error{Message: context.DeadlineExceeded.Error()}, http.StatusGatewayTimeout, devicewire.CodeDeadline},
		{&rpc.Error{Message: "/Users/me/secret disk on fire"}, http.StatusBadGateway, codeUsageFailed},
	} {
		n.mu.Lock()
		n.fail = c.fail
		n.mu.Unlock()
		code, out := clipCall(t, mux, "GET", "/api/v1/machines/mac/storage/usage?path=/x", "", "full")
		if code != c.status || out["code"] != c.code {
			t.Errorf("%v: %d %v", c.fail, code, out)
		}
		if msg, _ := out["error"].(string); msg == "" || strings.Contains(msg, "secret") {
			t.Errorf("%v: error %q", c.fail, msg)
		}
	}
}
