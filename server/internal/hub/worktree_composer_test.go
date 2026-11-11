package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
)

// Exercise the real mounted route, including its legacy branch/path validation and receipt wrapper.
func TestWorktreeComposerMountedRoutesAndReceipt(t *testing.T) {
	h := newTestHub(t)
	h.store = &clientOpsStore{ops: map[string]db.ClientOp{}}
	var creates atomic.Int32
	var params map[string]any
	boardNode(t, h, "machine", func(_ context.Context, method string, raw json.RawMessage) (any, error) {
		if method == "worktree.create" {
			creates.Add(1)
		}
		params = nil
		_ = json.Unmarshal(raw, &params)
		return map[string]any{"workspace_id": "repo::/work/new", "path": "/work/new", "branch": "new"}, nil
	})
	body := `{"workspace_id":"repo::/code","kind":"orca","composer":true,"name":"new","branch_override":"topic","base_ref":"main","no_parent":true,"note":"Review","sparse_preset_id":"ui","source_id":"issue:42","source_url":"https://github.com/example/app/issues/42","locale":"en","cwd":"ignored","command":"ignored"}`
	mux := worktreeReceiptRoutes(h)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, worktreeReceiptRequest(worktreeReceiptPath, body, worktreeReceiptID, "device", roleFull))
		if w.Code != http.StatusOK {
			t.Fatalf("composer-only mounted create: %d %s", w.Code, w.Body)
		}
	}
	if creates.Load() != 1 || params["composer"] != true || params["name"] != "new" || params["branch_override"] != "topic" || params["no_parent"] != true || params["command"] != nil || params["cwd"] != nil || params["path"] != nil {
		t.Fatalf("composer fields or deduplication: %v creates=%d", params, creates.Load())
	}
	for _, flag := range []string{`"true"`, `false`, `null`} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, worktreeReceiptRequest(worktreeReceiptPath, `{"workspace_id":"w1","composer":`+flag+`}`, "", "device", roleFull))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("malformed composer flag %s bypassed legacy validation: %d", flag, w.Code)
		}
	}
	for _, extra := range []string{`"composer":"true","branch":"topic","path":"/work/topic"`, `"composer":true,"branch_override":123`, `"composer":true,"no_parent":"true"`} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, worktreeReceiptRequest(worktreeReceiptPath, `{"workspace_id":"w1",`+extra+`}`, "", "device", roleFull))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("malformed intent passed mounted route: %d %s", w.Code, w.Body)
		}
	}
	for action, body := range map[string]string{
		"prepare": `{"workspace_id":"w1","kind":"orca","composer":true,"locale":"ja"}`,
		"source":  `{"workspace_id":"w1","kind":"orca","query":"42"}`,
		"preset":  `{"workspace_id":"w1","kind":"orca","name":"UI","directories":"src\nshared"}`,
		"delete":  `{"workspace_id":"w1","kind":"orca","preview_token":"opaque","force":true,"allowFailedArchiveHook":true,"allowUnverifiedPtyStop":true}`,
	} {
		path := "/api/v1/machines/machine/worktree/" + action
		for _, role := range []string{roleAnswer, roleLink, roleExtension} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, worktreeReceiptRequest(path, body, "", "device", role))
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s reached %s: %d", role, action, w.Code)
			}
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, worktreeReceiptRequest(path, body, "", "device", roleFull))
		if w.Code != http.StatusOK {
			t.Fatalf("mounted %s: %d %s", action, w.Code, w.Body)
		}
		if params["allowFailedArchiveHook"] != nil || params["allowUnverifiedPtyStop"] != nil {
			t.Fatal("waivers forwarded", params)
		}
		if action == "prepare" && params["composer"] != true {
			t.Fatal("composer probe stripped", params)
		}
	}
}

func TestWorktreeComposerReceiptUnknownDoesNotCreateAgain(t *testing.T) {
	h := newTestHub(t)
	h.store = &clientOpsStore{ops: map[string]db.ClientOp{}}
	var creates atomic.Int32
	boardNode(t, h, "machine", func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
		creates.Add(1)
		return nil, context.DeadlineExceeded
	})
	body := `{"workspace_id":"w1","kind":"orca","composer":true}`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		worktreeReceiptRoutes(h).ServeHTTP(w, worktreeReceiptRequest(worktreeReceiptPath, body, worktreeReceiptID, "device", roleFull))
		if w.Code != http.StatusBadGateway {
			t.Fatalf("ambiguous result: %d %s", w.Code, w.Body)
		}
	}
	if creates.Load() != 1 {
		t.Fatalf("ambiguous create retried: %d", creates.Load())
	}
	op, err := h.store.ClientOp(context.Background(), worktreeReceiptID)
	if err != nil || op == nil || op.State != "unknown" {
		t.Fatalf("ambiguous receipt: %+v %v", op, err)
	}
}
