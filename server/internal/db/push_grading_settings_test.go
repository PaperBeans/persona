package db

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

func gradingLayerTestValue(source, manager string) map[string]any {
	return map[string]any{
		"source": source, "manager": manager, "revision": 1, "updated_at": "2026-01-01T00:00:00Z",
		"config": map[string]any{"version": 1, "actions": map[string]any{"P1": "hold"}},
	}
}

func openPushGradingHubStore(t *testing.T) (*DB, context.Context) {
	t.Helper()
	d, ctx := open(t), context.Background()
	// open uses AllSets for the general db suite. These tests explicitly target
	// the production hub_settings table instead of the manager's settings table.
	d.sets = HubSet
	if _, err := d.pool.Exec(ctx, "TRUNCATE hub_settings"); err != nil {
		t.Fatal(err)
	}
	return d, ctx
}

func TestPushGradingLayerStoreOwnership(t *testing.T) {
	d, ctx := openPushGradingHubStore(t)
	key := "hub.push_grading_layer_test"
	var stored map[string]any
	ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, key, gradingTestMirrorKey, "manager-a", "extension:one", gradingLayerTestValue("extension:one", "manager-a"), &stored)
	if err != nil || !ok || stored["revision"] != float64(1) {
		t.Fatalf("first claim: ok=%v stored=%v err=%v", ok, stored, err)
	}
	stored = nil
	ok, err = d.PutPushGradingLayer(ctx, gradingTestBaseKey, key, gradingTestMirrorKey, "manager-a", "extension:one", gradingLayerTestValue("extension:one", "manager-a"), &stored)
	if err != nil || !ok || stored["revision"] != float64(2) {
		t.Fatalf("same-owner renewal: ok=%v stored=%v err=%v", ok, stored, err)
	}
	for _, claimant := range [][2]string{{"manager-a", "extension:two"}, {"manager-b", "extension:one"}} {
		ok, err = d.PutPushGradingLayer(ctx, gradingTestBaseKey, key, gradingTestMirrorKey, claimant[0], claimant[1], gradingLayerTestValue(claimant[1], claimant[0]), nil)
		if err != nil || ok {
			t.Fatalf("competing claim by %v: ok=%v err=%v", claimant, ok, err)
		}
	}
	ok, err = d.DeletePushGradingLayer(ctx, gradingTestBaseKey, key, gradingTestMirrorKey, "manager-a", "extension:two", 0)
	if err != nil || ok {
		t.Fatalf("cross-owner delete: ok=%v err=%v", ok, err)
	}
	ok, err = d.DeletePushGradingLayer(ctx, gradingTestBaseKey, key, gradingTestMirrorKey, "manager-a", "extension:one", 1)
	if err != nil || ok {
		t.Fatalf("stale-revision delete: ok=%v err=%v", ok, err)
	}
	ok, err = d.DeletePushGradingLayer(ctx, gradingTestBaseKey, key, gradingTestMirrorKey, "manager-a", "extension:one", 2)
	if err != nil || !ok {
		t.Fatalf("current delete: ok=%v err=%v", ok, err)
	}
	var raw map[string]any
	found, err := d.Setting(ctx, key, &raw)
	if err != nil || found {
		t.Fatalf("deleted row still exists: found=%v raw=%v err=%v", found, raw, err)
	}
}

func TestPushGradingLayerStoreConcurrentFirstClaim(t *testing.T) {
	d, ctx := openPushGradingHubStore(t)
	key := "hub.push_grading_layer_concurrent_test"
	start := make(chan struct{})
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, source := range []string{"extension:one", "extension:two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, key, gradingTestMirrorKey, "manager-a", source, gradingLayerTestValue(source, "manager-a"), nil)
			results <- result{ok: ok, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var succeeded int
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.ok {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful first claims = %d, want exactly one", succeeded)
	}
	values, err := d.Settings(ctx, key)
	if err != nil || len(values) != 1 {
		t.Fatalf("consistent read after concurrent claims: values=%v err=%v", values, err)
	}
}

const (
	gradingTestBaseKey   = "hub.push_grading_base_test"
	gradingTestLayerKey  = "hub.push_grading_layer_transaction_test"
	gradingTestMirrorKey = "hub.push_grading_mirror_test"
)

func assertGradingTestMirror(t *testing.T, d *DB, want bool) {
	t.Helper()
	var got bool
	found, err := d.Setting(context.Background(), gradingTestMirrorKey, &got)
	if err != nil || !found || got != want {
		t.Fatalf("mirror: found=%v got=%v want=%v err=%v", found, got, want, err)
	}
}

func putGradingTestBase(d *DB, enabled any, actions map[string]any) error {
	config := map[string]any{"version": 1}
	if enabled != nil {
		config["enabled"] = enabled
	} else if enabled == nil {
		config["enabled"] = nil
	}
	if actions != nil {
		config["actions"] = actions
	}
	return d.PutPushGradingBase(context.Background(), gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, config)
}

func putGradingTestLayer(d *DB, source string, out any) (bool, error) {
	return d.PutPushGradingLayer(context.Background(), gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey,
		"manager-a", source, gradingLayerTestValue(source, "manager-a"), out)
}

func TestPushGradingBaseAndLayerMirror(t *testing.T) {
	d, ctx := openPushGradingHubStore(t)
	if err := d.PutSetting(ctx, gradingTestMirrorKey, false); err != nil {
		t.Fatal(err)
	}
	if err := putGradingTestBase(d, nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGradingTestMirror(t, d, false)
	var layer map[string]any
	if ok, err := putGradingTestLayer(d, "extension:one", &layer); err != nil || !ok || layer["revision"] != float64(1) {
		t.Fatalf("layer claim: ok=%v layer=%v err=%v", ok, layer, err)
	}
	assertGradingTestMirror(t, d, false)
	if err := putGradingTestBase(d, false, nil); err != nil {
		t.Fatal(err)
	}
	assertGradingTestMirror(t, d, false)
	if err := putGradingTestBase(d, nil, nil); err != nil {
		t.Fatal(err)
	}
	assertGradingTestMirror(t, d, false)
	if ok, err := d.DeletePushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", 1); err != nil || !ok {
		t.Fatalf("delete layer: ok=%v err=%v", ok, err)
	}
	assertGradingTestMirror(t, d, false)
	if err := putGradingTestBase(d, true, map[string]any{"P1": "silence"}); err != nil {
		t.Fatal(err)
	}
	assertGradingTestMirror(t, d, true)
	var base struct {
		Actions map[string]string `json:"actions"`
	}
	if found, err := d.Setting(ctx, gradingTestBaseKey, &base); err != nil || !found || base.Actions["P1"] != "silence" {
		t.Fatalf("same-enabled base change was not stored: %+v found=%v err=%v", base, found, err)
	}
}

func TestPushGradingLayerConflictLeavesMirrorUntouched(t *testing.T) {
	d, ctx := openPushGradingHubStore(t)
	if err := d.PutSetting(ctx, gradingTestMirrorKey, true); err != nil {
		t.Fatal(err)
	}
	if ok, err := putGradingTestLayer(d, "extension:one", nil); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	assertGradingTestMirror(t, d, true)
	before, err := d.Settings(ctx, gradingTestLayerKey, gradingTestMirrorKey)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := putGradingTestLayer(d, "extension:two", nil); err != nil || ok {
		t.Fatalf("competing claim: ok=%v err=%v", ok, err)
	}
	if ok, err := d.DeletePushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", 9); err != nil || ok {
		t.Fatalf("stale delete: ok=%v err=%v", ok, err)
	}
	after, err := d.Settings(ctx, gradingTestLayerKey, gradingTestMirrorKey)
	if err != nil || string(after[gradingTestLayerKey]) != string(before[gradingTestLayerKey]) || string(after[gradingTestMirrorKey]) != string(before[gradingTestMirrorKey]) {
		t.Fatalf("conflict changed state: before=%v after=%v err=%v", before, after, err)
	}
}

func TestPushGradingConcurrentBaseAndLayerWrites(t *testing.T) {
	t.Run("claim versus owner false", func(t *testing.T) {
		d, ctx := openPushGradingHubStore(t)
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			errs <- putGradingTestBase(d, false, nil)
		}()
		go func() {
			<-start
			ok, err := putGradingTestLayer(d, "extension:one", nil)
			if err == nil && !ok {
				err = errPushGradingOwnerConflict
			}
			errs <- err
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		assertGradingTestMirror(t, d, false)
		var layer map[string]any
		if found, err := d.Setting(ctx, gradingTestLayerKey, &layer); err != nil || !found {
			t.Fatalf("layer missing after concurrent claim: found=%v err=%v", found, err)
		}
	})
	t.Run("delete versus owner true", func(t *testing.T) {
		d, ctx := openPushGradingHubStore(t)
		if err := putGradingTestBase(d, nil, nil); err != nil {
			t.Fatal(err)
		}
		if ok, err := putGradingTestLayer(d, "extension:one", nil); err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			errs <- putGradingTestBase(d, true, nil)
		}()
		go func() {
			<-start
			ok, err := d.DeletePushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", 1)
			if err == nil && !ok {
				err = errPushGradingOwnerConflict
			}
			errs <- err
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		assertGradingTestMirror(t, d, true)
	})
}

func TestPushGradingMirrorFailureRollsBack(t *testing.T) {
	d, ctx := openPushGradingHubStore(t)
	if err := putGradingTestBase(d, false, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := putGradingTestLayer(d, "extension:one", nil); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := d.pool.Exec(ctx, `CREATE FUNCTION fail_push_grading_mirror() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked test mirror'; END; $$;
		CREATE TRIGGER fail_push_grading_mirror BEFORE INSERT OR UPDATE ON hub_settings FOR EACH ROW
		WHEN (NEW.key = 'hub.push_grading_mirror_test') EXECUTE FUNCTION fail_push_grading_mirror()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_push_grading_mirror ON hub_settings; DROP FUNCTION IF EXISTS fail_push_grading_mirror()`)
	})
	if err := putGradingTestBase(d, true, nil); err == nil {
		t.Fatal("base write unexpectedly crossed the failing mirror")
	}
	if ok, err := putGradingTestLayer(d, "extension:one", nil); err != nil || !ok {
		t.Fatalf("layer renewal touched the failing legacy mirror: ok=%v err=%v", ok, err)
	}
	var layer map[string]any
	if found, err := d.Setting(ctx, gradingTestLayerKey, &layer); err != nil || !found || layer["revision"] != float64(2) {
		t.Fatalf("layer renewal did not persist independently: %+v found=%v err=%v", layer, found, err)
	}
	if ok, err := d.DeletePushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", 2); err != nil || !ok {
		t.Fatalf("delete touched the failing legacy mirror: ok=%v err=%v", ok, err)
	}
	var base struct {
		Enabled bool `json:"enabled"`
	}
	if found, err := d.Setting(ctx, gradingTestBaseKey, &base); err != nil || !found || base.Enabled {
		t.Fatalf("base changed after rollback: %+v found=%v err=%v", base, found, err)
	}
	if found, err := d.Setting(ctx, gradingTestLayerKey, &layer); err != nil || found {
		t.Fatalf("layer was not deleted independently: %+v found=%v err=%v", layer, found, err)
	}
	assertGradingTestMirror(t, d, false)
}

func TestPushGradingLayerWritesPreserveLegacySwitch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial bool
	}{{"disabled", false}, {"enabled", true}} {
		t.Run(tc.name, func(t *testing.T) {
			d, ctx := openPushGradingHubStore(t)
			if err := d.PutSetting(ctx, gradingTestMirrorKey, tc.initial); err != nil {
				t.Fatal(err)
			}
			var base map[string]any
			if found, err := d.Setting(ctx, gradingTestBaseKey, &base); err != nil || found {
				t.Fatalf("test needs no base row: found=%v base=%v err=%v", found, base, err)
			}
			ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", gradingLayerTestValue("extension:one", "manager-a"), nil)
			if err != nil || !ok {
				t.Fatalf("claim: ok=%v err=%v", ok, err)
			}
			assertGradingTestMirror(t, d, tc.initial)
			ok, err = d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", gradingLayerTestValue("extension:one", "manager-a"), nil)
			if err != nil || !ok {
				t.Fatalf("renewal: ok=%v err=%v", ok, err)
			}
			assertGradingTestMirror(t, d, tc.initial)
			ok, err = d.DeletePushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", 2)
			if err != nil || !ok {
				t.Fatalf("delete: ok=%v err=%v", ok, err)
			}
			assertGradingTestMirror(t, d, tc.initial)
			if err := putGradingTestBase(d, nil, nil); err != nil {
				t.Fatal(err)
			}
			assertGradingTestMirror(t, d, tc.initial)
		})
	}
}

func TestPushGradingLayerClearAndPrune(t *testing.T) {
	t.Run("clear filters by optional manager and source without touching mirror", func(t *testing.T) {
		d, ctx := openPushGradingHubStore(t)
		if err := d.PutSetting(ctx, gradingTestMirrorKey, true); err != nil {
			t.Fatal(err)
		}
		if ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", gradingLayerTestValue("extension:one", "manager-a"), nil); err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
		// Mismatched manager or source leaves the layer intact.
		for _, tc := range []struct {
			name, mgr, src string
		}{
			{"wrong manager", "manager-b", ""},
			{"wrong source", "", "extension:two"},
			{"wrong manager with right source", "manager-b", "extension:one"},
			{"right manager with wrong source", "manager-a", "extension:two"},
		} {
			if deleted, err := d.ClearPushGradingLayer(ctx, gradingTestLayerKey, tc.mgr, tc.src); err != nil || deleted {
				t.Fatalf("%s: deleted=%v err=%v, want false nil", tc.name, deleted, err)
			}
		}
		// Exact manager+source clears it.
		if deleted, err := d.ClearPushGradingLayer(ctx, gradingTestLayerKey, "manager-a", "extension:one"); err != nil || !deleted {
			t.Fatalf("exact clear: deleted=%v err=%v", deleted, err)
		}
		assertGradingTestMirror(t, d, true)

		// Unfiltered clear deletes any owner and is idempotent on empty.
		if ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-b", "extension:two", gradingLayerTestValue("extension:two", "manager-b"), nil); err != nil || !ok {
			t.Fatalf("re-claim: ok=%v err=%v", ok, err)
		}
		if deleted, err := d.ClearPushGradingLayer(ctx, gradingTestLayerKey, "", ""); err != nil || !deleted {
			t.Fatalf("unfiltered clear: deleted=%v err=%v", deleted, err)
		}
		if deleted, err := d.ClearPushGradingLayer(ctx, gradingTestLayerKey, "", ""); err != nil || deleted {
			t.Fatalf("repeat clear: deleted=%v err=%v", deleted, err)
		}
		assertGradingTestMirror(t, d, true)
	})

	t.Run("prune reconciles manager ownership and handles empty or nil keep list", func(t *testing.T) {
		for _, initial := range []bool{false, true} {
			d, ctx := openPushGradingHubStore(t)
			if err := d.PutSetting(ctx, gradingTestMirrorKey, initial); err != nil {
				t.Fatal(err)
			}
			if _, err := d.pool.Exec(ctx, `CREATE FUNCTION fail_push_grading_prune_mirror() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'blocked test mirror'; END; $$;
				CREATE TRIGGER fail_push_grading_prune_mirror BEFORE INSERT OR UPDATE ON hub_settings FOR EACH ROW
				WHEN (NEW.key = 'hub.push_grading_mirror_test') EXECUTE FUNCTION fail_push_grading_prune_mirror()`); err != nil {
				t.Fatal(err)
			}
			cleanupTrigger := func() {
				_, _ = d.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_push_grading_prune_mirror ON hub_settings; DROP FUNCTION IF EXISTS fail_push_grading_prune_mirror()`)
			}

			if ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", gradingLayerTestValue("extension:one", "manager-a"), nil); err != nil || !ok {
				cleanupTrigger()
				t.Fatalf("claim: ok=%v err=%v", ok, err)
			}
			// Empty manager is rejected.
			if deleted, err := d.PrunePushGradingLayer(ctx, gradingTestLayerKey, " ", nil); err == nil || deleted {
				cleanupTrigger()
				t.Fatalf("empty manager prune: deleted=%v err=%v, want error", deleted, err)
			}
			// Different manager's report never prunes manager-a's layer.
			if deleted, err := d.PrunePushGradingLayer(ctx, gradingTestLayerKey, "manager-b", nil); err != nil || deleted {
				cleanupTrigger()
				t.Fatalf("other manager prune: deleted=%v err=%v", deleted, err)
			}
			// Owning manager keeping extension:one preserves the layer.
			if deleted, err := d.PrunePushGradingLayer(ctx, gradingTestLayerKey, "manager-a", []string{"extension:one", "extension:two"}); err != nil || deleted {
				cleanupTrigger()
				t.Fatalf("active owner prune: deleted=%v err=%v", deleted, err)
			}
			// Dropping extension:one while keeping another extension prunes the layer.
			if deleted, err := d.PrunePushGradingLayer(ctx, gradingTestLayerKey, "manager-a", []string{"extension:two"}); err != nil || !deleted {
				cleanupTrigger()
				t.Fatalf("dropped owner prune: deleted=%v err=%v", deleted, err)
			}
			// After prune, another owner can claim revision 1 cleanly.
			var claimed map[string]any
			if ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:two", gradingLayerTestValue("extension:two", "manager-a"), &claimed); err != nil || !ok || claimed["revision"] != float64(1) {
				cleanupTrigger()
				t.Fatalf("post-prune claim: ok=%v claimed=%v err=%v", ok, claimed, err)
			}
			// Nil keepSources (all extensions disabled/uninstalled/revoked) prunes the layer too.
			if deleted, err := d.PrunePushGradingLayer(ctx, gradingTestLayerKey, "manager-a", nil); err != nil || !deleted {
				cleanupTrigger()
				t.Fatalf("nil keepSources prune: deleted=%v err=%v", deleted, err)
			}
			var raw map[string]any
			if found, err := d.Setting(ctx, gradingTestLayerKey, &raw); err != nil || found {
				cleanupTrigger()
				t.Fatalf("pruned row still exists: found=%v raw=%v err=%v", found, raw, err)
			}
			assertGradingTestMirror(t, d, initial)
			cleanupTrigger()
		}
	})

	t.Run("concurrent prune and owner claim serialize cleanly", func(t *testing.T) {
		d, ctx := openPushGradingHubStore(t)
		if ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-a", "extension:one", gradingLayerTestValue("extension:one", "manager-a"), nil); err != nil || !ok {
			t.Fatalf("initial claim: ok=%v err=%v", ok, err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		var pruned bool
		var claimed bool
		go func() {
			<-start
			ok, err := d.PrunePushGradingLayer(ctx, gradingTestLayerKey, "manager-a", nil)
			pruned = ok
			errs <- err
		}()
		go func() {
			<-start
			ok, err := d.PutPushGradingLayer(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, "manager-b", "extension:two", gradingLayerTestValue("extension:two", "manager-b"), nil)
			claimed = ok
			errs <- err
		}()
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if !pruned {
			t.Fatal("expected prune of manager-a layer to succeed")
		}
		var raw map[string]any
		found, err := d.Setting(ctx, gradingTestLayerKey, &raw)
		if err != nil {
			t.Fatal(err)
		}
		if claimed {
			if !found || raw["manager"] != "manager-b" || raw["source"] != "extension:two" || raw["revision"] != float64(1) {
				t.Fatalf("after claim-second: found=%v raw=%v", found, raw)
			}
		} else if found {
			t.Fatalf("after claim-first conflict + prune: row should be absent, got %v", raw)
		}
	})
}

func TestPushGradingSetSwitchAtomicity(t *testing.T) {
	d, ctx := openPushGradingHubStore(t)

	// Without a base row, SetPushGradingSwitch writes only hub.ln_mode (does not fabricate hub.push_grading).
	if err := d.SetPushGradingSwitch(ctx, gradingTestBaseKey, gradingTestMirrorKey, true); err != nil {
		t.Fatal(err)
	}
	vals, err := d.Settings(ctx, gradingTestBaseKey, gradingTestMirrorKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, hasBase := vals[gradingTestBaseKey]; hasBase {
		t.Fatalf("SetPushGradingSwitch created base row unexpectedly: %s", vals[gradingTestBaseKey])
	}
	if string(vals[gradingTestMirrorKey]) != "true" {
		t.Fatalf("mirror = %s, want true", vals[gradingTestMirrorKey])
	}

	// With a base row carrying custom actions, SetPushGradingSwitch updates enabled and mirror atomically
	// without losing the other base fields.
	base := map[string]any{"version": 1, "enabled": true, "actions": map[string]string{"P1": "push"}}
	if err := d.PutPushGradingBase(ctx, gradingTestBaseKey, gradingTestLayerKey, gradingTestMirrorKey, base); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPushGradingSwitch(ctx, gradingTestBaseKey, gradingTestMirrorKey, false); err != nil {
		t.Fatal(err)
	}
	vals, err = d.Settings(ctx, gradingTestBaseKey, gradingTestMirrorKey)
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]any
	if err := json.Unmarshal(vals[gradingTestBaseKey], &updated); err != nil {
		t.Fatal(err)
	}
	if updated["enabled"] != false {
		t.Fatalf("base.enabled = %v, want false", updated["enabled"])
	}
	actions, _ := updated["actions"].(map[string]any)
	if actions["P1"] != "push" {
		t.Fatalf("SetPushGradingSwitch lost base actions: %+v", updated)
	}
	if string(vals[gradingTestMirrorKey]) != "false" {
		t.Fatalf("mirror = %s, want false", vals[gradingTestMirrorKey])
	}
}
