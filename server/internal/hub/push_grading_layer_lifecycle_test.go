package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extwire"
)

type flakyGradingLayerStore struct {
	*gradingStore
	mu        sync.Mutex
	failPut   bool
	failDel   bool
	failClear bool
	failPrune bool
}

func (s *flakyGradingLayerStore) PutPushGradingLayer(ctx context.Context, baseKey, layerKey, mirrorKey, manager, source string, value any, out any) (bool, error) {
	s.mu.Lock()
	fail := s.failPut
	s.mu.Unlock()
	if fail {
		return false, errors.New("database unavailable")
	}
	return s.gradingStore.PutPushGradingLayer(ctx, baseKey, layerKey, mirrorKey, manager, source, value, out)
}

func (s *flakyGradingLayerStore) DeletePushGradingLayer(ctx context.Context, baseKey, layerKey, mirrorKey, manager, source string, revision int64) (bool, error) {
	s.mu.Lock()
	fail := s.failDel
	s.mu.Unlock()
	if fail {
		return false, errors.New("database unavailable")
	}
	return s.gradingStore.DeletePushGradingLayer(ctx, baseKey, layerKey, mirrorKey, manager, source, revision)
}

func (s *flakyGradingLayerStore) ClearPushGradingLayer(ctx context.Context, layerKey, manager, source string) (bool, error) {
	s.mu.Lock()
	fail := s.failClear
	s.mu.Unlock()
	if fail {
		return false, errors.New("database unavailable")
	}
	return s.gradingStore.ClearPushGradingLayer(ctx, layerKey, manager, source)
}

func (s *flakyGradingLayerStore) PrunePushGradingLayer(ctx context.Context, layerKey, manager string, keepSources []string) (bool, error) {
	s.mu.Lock()
	fail := s.failPrune
	s.mu.Unlock()
	if fail {
		return false, errors.New("database unavailable")
	}
	return s.gradingStore.PrunePushGradingLayer(ctx, layerKey, manager, keepSources)
}

func TestPushGradingLayerRoutesNeverMutateOwnerSwitch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   bool
		value bool
	}{
		{"absent", false, false},
		{"disabled", true, false},
		{"enabled", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := gradingHub(t)
			store := h.push.store.(*gradingStore)
			if tc.set {
				if err := h.push.setPushGrading(context.Background(), tc.value); err != nil {
					t.Fatal(err)
				}
			}
			checkSwitch := func(stage string) {
				t.Helper()
				store.mu.Lock()
				raw, exists := store.vals[gradingSwitchSettingKey]
				store.mu.Unlock()
				if !tc.set {
					if exists {
						t.Fatalf("%s: created hub.ln_mode=%s when it was absent", stage, raw)
					}
					return
				}
				want, _ := json.Marshal(tc.value)
				if !exists || string(raw) != string(want) {
					t.Fatalf("%s: hub.ln_mode=%s (exists=%v), want %s", stage, raw, exists, want)
				}
			}

			extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
			extB := extGradingPrincipal(assistantwire.AssistantID, "ext-b")

			if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"hold"}}`); code != http.StatusOK {
				t.Fatalf("claim: %d %s", code, body)
			}
			checkSwitch("after claim")

			if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"silence"}}`); code != http.StatusOK {
				t.Fatalf("renewal: %d %s", code, body)
			}
			checkSwitch("after renewal")

			if code, body := gradingLayerHTTP(t, h, extB, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusConflict {
				t.Fatalf("conflict: %d %s, want 409", code, body)
			}
			checkSwitch("after conflict")

			// Base write with explicit null enabled preserves hub.ln_mode; explicit boolean mirrors it.
			baseNull := parseGradingTestConfig(t, `{"version":1,"enabled":null}`, false)
			if err := writePushGradingBase(context.Background(), store, baseNull); err != nil {
				t.Fatal(err)
			}
			checkSwitch("after base null enabled")

			if code, body := gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer?revision=1", ""); code != http.StatusOK || !strings.Contains(string(body), `"removed":false`) {
				t.Fatalf("stale delete: %d %s", code, body)
			}
			checkSwitch("after stale delete")

			if code, body := gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer?revision=2", ""); code != http.StatusOK || !strings.Contains(string(body), `"removed":true`) {
				t.Fatalf("current delete: %d %s", code, body)
			}
			checkSwitch("after delete")

			// Re-claim and prune via EventReport.
			if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusOK {
				t.Fatalf("re-claim: %d %s", code, body)
			}
			rawDrop, _ := json.Marshal(extwire.Report{PushGradingDeclared: true})
			busEvents[extwire.EventReport](h, nil, rawDrop)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if !h.waitPushGradingLayerReconciled(ctx) {
				t.Fatal("reconcile did not finish")
			}
			checkSwitch("after lifecycle prune")

			// Re-claim and clear via full credential.
			if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusOK {
				t.Fatalf("re-claim 2: %d %s", code, body)
			}
			if code, body := gradingLayerHTTP(t, h, nil, http.MethodDelete, "/api/v1/push/grading/layer", ""); code != http.StatusOK || !strings.Contains(string(body), `"removed":true`) {
				t.Fatalf("owner clear: %d %s", code, body)
			}
			checkSwitch("after owner clear")
		})
	}
}

func TestPushGradingLayerLifecycleManagerIsolationAndQuiesce(t *testing.T) {
	t.Run("other manager report never prunes owning manager layer", func(t *testing.T) {
		h, _ := gradingHub(t)
		extMgrB := extGradingPrincipal("manager-b", "ext-a")

		if code, body := gradingLayerHTTP(t, h, extMgrB, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"hold"}}`); code != http.StatusOK {
			t.Fatalf("PUT manager-b: %d %s", code, body)
		}

		// Assistant reporting empty push_grading_extensions must NOT prune manager-b's layer.
		h.reconcilePushGradingLayer(assistantwire.AssistantID, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("reconcile assistant did not finish")
		}
		snap, err := readPushGradingSnapshot(ctx, h.push.store)
		if err != nil || snap.Layer == nil || snap.Layer.Manager != "manager-b" || snap.Layer.Source != "extension:ext-a" {
			t.Fatalf("assistant report pruned manager-b layer: %+v err=%v", snap, err)
		}

		// Manager-b reporting scope removal prunes manager-b's layer.
		h.reconcilePushGradingLayer("manager-b", []string{"ext-other"})
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("reconcile manager-b did not finish")
		}
		snap, err = readPushGradingSnapshot(ctx, h.push.store)
		if err != nil || snap.Layer != nil {
			t.Fatalf("manager-b layer survived scope removal: %+v err=%v", snap, err)
		}
	})

	t.Run("quiesce defers reconciliation and resume flushes it", func(t *testing.T) {
		h, _ := gradingHub(t)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.quiesce.pause(ctx); err != nil {
			t.Fatal(err)
		}
		// HTTP write while paused returns 503, and setPushGrading also respects write quiesce.
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer", ""); code != http.StatusServiceUnavailable {
			t.Fatalf("DELETE while quiesced: %d %s, want 503", code, body)
		}
		if err := h.push.setPushGrading(ctx, true); err == nil {
			t.Fatal("setPushGrading succeeded while hub writes were quiesced")
		}
		// Reconcile while paused defers until resume.
		h.reconcilePushGradingLayer(assistantwire.AssistantID, nil)
		if h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("reconciler reported complete while quiesced")
		}
		if snap, err := readPushGradingSnapshot(ctx, h.push.store); err != nil || snap.Layer == nil {
			t.Fatalf("layer pruned while quiesced: %+v err=%v", snap, err)
		}

		h.quiesce.resume()
		h.resumePushGradingLayerReconcile()
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("reconciler did not flush after resume")
		}
		if snap, err := readPushGradingSnapshot(ctx, h.push.store); err != nil || snap.Layer != nil {
			t.Fatalf("layer remained after resume: %+v err=%v", snap, err)
		}
	})

	t.Run("transient prune failure is deferred and retried on resume", func(t *testing.T) {
		h, _ := gradingHub(t)
		baseStore := h.push.store.(*gradingStore)
		flaky := &flakyGradingLayerStore{gradingStore: baseStore}
		h.push.store = flaky
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		flaky.mu.Lock()
		flaky.failPrune = true
		flaky.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.reconcilePushGradingLayer(assistantwire.AssistantID, nil)
		if h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("waitPushGradingLayerReconciled returned true despite prune error")
		}
		if snap, err := readPushGradingSnapshot(ctx, baseStore); err != nil || snap.Layer == nil {
			t.Fatalf("layer disappeared during failed prune: %+v err=%v", snap, err)
		}

		flaky.mu.Lock()
		flaky.failPrune = false
		flaky.mu.Unlock()
		h.resumePushGradingLayerReconcile()
		if !h.waitPushGradingLayerReconciled(ctx) {
			t.Fatal("deferred prune did not succeed on resume")
		}
		if snap, err := readPushGradingSnapshot(ctx, baseStore); err != nil || snap.Layer != nil {
			t.Fatalf("layer remained after retry: %+v err=%v", snap, err)
		}
	})

	t.Run("transient prune failure retries automatically with backoff and exits cleanly on shutdown", func(t *testing.T) {
		h, _ := gradingHub(t)
		deployCtx, stopHub := context.WithCancel(context.Background())
		defer stopHub()
		h.deployCtx = deployCtx
		h.gradingReconcile.retryBase = 15 * time.Millisecond
		h.gradingReconcile.retryMax = 40 * time.Millisecond

		baseStore := h.push.store.(*gradingStore)
		flaky := &flakyGradingLayerStore{gradingStore: baseStore}
		h.push.store = flaky
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		flaky.mu.Lock()
		flaky.failPrune = true
		flaky.mu.Unlock()

		h.reconcilePushGradingLayer(assistantwire.AssistantID, nil)
		// Wait for the initial failure to be recorded into rec.deferred before allowing the store to recover.
		initCtx, cancelInit := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelInit()
		if h.waitPushGradingLayerReconciled(initCtx) {
			t.Fatal("expected initial prune attempt to fail while failPrune=true")
		}
		if snap, err := readPushGradingSnapshot(context.Background(), baseStore); err != nil || snap.Layer == nil {
			t.Fatalf("layer disappeared while failPrune=true: %+v err=%v", snap, err)
		}

		// Store recovers; automatic backoff retry must prune the layer without manual resume.
		flaky.mu.Lock()
		flaky.failPrune = false
		flaky.mu.Unlock()
		eventually(t, 2*time.Second, "automatic backoff retry prunes layer", func() bool {
			snap, err := readPushGradingSnapshot(context.Background(), baseStore)
			return err == nil && snap.Layer == nil
		})

		// Verify shutdown cancels any scheduled backoff retry promptly with no goroutine leak.
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`); code != http.StatusOK {
			t.Fatalf("PUT 2: %d %s", code, body)
		}
		flaky.mu.Lock()
		flaky.failPrune = true
		flaky.mu.Unlock()
		h.gradingReconcile.retryBase = 10 * time.Second
		h.gradingReconcile.retryMax = 10 * time.Second
		h.reconcilePushGradingLayer(assistantwire.AssistantID, nil)
		waitCtx, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelWait()
		_ = h.waitPushGradingLayerReconciled(waitCtx)
		stopHub()
		done := make(chan struct{})
		go func() {
			h.nodeWorkers.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("scheduled reconcile retry did not exit promptly on hub shutdown")
		}
	})
}

func TestPushGradingLayerRoutesAndLifecycleRealPostgres(t *testing.T) {
	dsn := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := db.Open(ctx, dsn, db.HubSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	cleanup := func() {
		_, _ = store.ClearPushGradingLayer(context.Background(), pushGradingLayerSettingKey, "", "")
		_ = store.PutSetting(context.Background(), gradingSwitchSettingKey, false)
	}
	cleanup()
	t.Cleanup(cleanup)

	h := newTestHub(t)
	h.store = store
	h.push.store = store
	if err := h.push.setPushGrading(ctx, true); err != nil {
		t.Fatal(err)
	}

	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-pg-a")
	extB := extGradingPrincipal(assistantwire.AssistantID, "ext-pg-b")
	extOtherMgr := extGradingPrincipal("manager-pg-b", "ext-pg-a")

	// Claim and renew in real Postgres, checking preview and live applyPushGrading parity immediately after each change.
	code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"hold","P2":"push"}}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"revision":1`) {
		t.Fatalf("PG claim: %d %s", code, body)
	}
	prev1 := previewGradingHTTP(t, h, extA, `{"notification":{"kind":"assistant","level":"P2","related_key":"pg:1"}}`)
	allow1, reason1 := h.push.applyPushGrading(pushPolicy{kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP2, relatedKey: "pg:1"})
	if !prev1.Allow || !allow1 || reason1 != "" || prev1.LayerSource != "extension:ext-pg-a" {
		t.Fatalf("PG claim preview/live mismatch: prev=%+v live=(%v,%q)", prev1, allow1, reason1)
	}
	// Manager-pg-b does NOT inherit assistant's layer (still uses owner switch -> P2 silenced).
	prevOther := previewGradingHTTP(t, h, nil, `{"notification":{"kind":"assistant","level":"P2","manager":"manager-pg-b","related_key":"pg:2"}}`)
	allowOther, reasonOther := h.push.applyPushGrading(pushPolicy{kind: assistantwire.NotifyPush, manager: "manager-pg-b", level: assistantwire.LevelP2, relatedKey: "pg:2"})
	if prevOther.Allow || prevOther.Reason != pushGradingOutcomeSilenced || prevOther.LiveReason != interceptReasonP2 || allowOther || reasonOther != interceptReasonP2 {
		t.Fatalf("PG manager isolation mismatch: prev=%+v live=(%v,%q)", prevOther, allowOther, reasonOther)
	}

	code, body = gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1,"actions":{"P1":"silence"},"dedup_window":"4h"}`)
	if code != http.StatusOK || !strings.Contains(string(body), `"revision":2`) {
		t.Fatalf("PG renew: %d %s", code, body)
	}
	prev2 := previewGradingHTTP(t, h, extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"pg:3"}}`)
	allow2, reason2 := h.push.applyPushGrading(pushPolicy{kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "pg:3"})
	if prev2.Allow || prev2.Reason != pushGradingOutcomeSilenced || prev2.LiveReason != interceptReasonP2 || allow2 || reason2 != interceptReasonP2 {
		t.Fatalf("PG renew immediate cache invalidation mismatch: prev=%+v live=(%v,%q)", prev2, allow2, reason2)
	}

	// Competing extension and competing manager both get 409 layer_taken.
	for name, p := range map[string]*principal{"other ext": extB, "other manager": extOtherMgr} {
		code, body = gradingLayerHTTP(t, h, p, http.MethodPut, "/api/v1/push/grading/layer", `{"version":1}`)
		if code != http.StatusConflict || !strings.Contains(string(body), codePushGradingLayerTaken) {
			t.Fatalf("PG %s conflict: %d %s", name, code, body)
		}
	}

	// Other manager's report leaves assistant's layer intact; assistant dropping ext-pg-a prunes it durably.
	h.reconcilePushGradingLayer("manager-pg-b", nil)
	if !h.waitPushGradingLayerReconciled(ctx) {
		t.Fatal("PG manager-pg-b reconcile failed")
	}
	if snap, err := readPushGradingSnapshot(ctx, store); err != nil || snap.Layer == nil || snap.Layer.Revision != 2 {
		t.Fatalf("PG other manager report altered layer: %+v err=%v", snap, err)
	}

	rawDrop, _ := json.Marshal(extwire.Report{PushGradingDeclared: true, PushGradingExtensions: []string{"ext-pg-b"}})
	busEvents[extwire.EventReport](h, nil, rawDrop)
	if !h.waitPushGradingLayerReconciled(ctx) {
		t.Fatal("PG assistant reconcile failed")
	}
	if snap, err := readPushGradingSnapshot(ctx, store); err != nil || snap.Layer != nil {
		t.Fatalf("PG layer survived reconcile: %+v err=%v", snap, err)
	}
	// Immediately after prune, cache is invalidated and P1 reverts to owner's default "held" / ln_p1_held.
	prevAfterPrune := previewGradingHTTP(t, h, nil, `{"notification":{"kind":"assistant","level":"P1","related_key":"pg:4"}}`)
	allowAfterPrune, reasonAfterPrune := h.push.applyPushGrading(pushPolicy{kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "pg:4"})
	if prevAfterPrune.Allow || prevAfterPrune.Reason != pushGradingOutcomeHeld || prevAfterPrune.LiveReason != interceptReasonP1 || allowAfterPrune || reasonAfterPrune != interceptReasonP1 {
		t.Fatalf("PG post-prune parity mismatch: prev=%+v live=(%v,%q)", prevAfterPrune, allowAfterPrune, reasonAfterPrune)
	}

	// Verify owner hub.ln_mode remained true throughout.
	var lnMode bool
	if found, err := store.Setting(ctx, gradingSwitchSettingKey, &lnMode); err != nil || !found || !lnMode {
		t.Fatalf("PG hub.ln_mode mutated: found=%v val=%v err=%v", found, lnMode, err)
	}
}
