package node

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func herdrDeletionFixture(t *testing.T, linked bool) (worktreeFixture, string, string) {
	t.Helper()
	w := worktreeNode(t)
	path, id := w.repo, "w1"
	if linked {
		path, id = filepath.Join(w.home, "topic"), "w2"
		runPullGit(t, w.repo, "worktree", "add", "-b", "topic", path)
	}
	show := func(present bool) {
		spaces, tabs, panes := []any{}, []any{}, []any{}
		if present {
			spaces = append(spaces, map[string]any{"workspace_id": id, "label": "Example", "cwd": path})
			tabs = append(tabs, map[string]any{"workspace_id": id, "tab_id": id + ":t1"})
			panes = append(panes, map[string]any{"workspace_id": id, "tab_id": id + ":t1", "pane_id": id + ":p1", "cwd": path})
		}
		w.f.reply("session.snapshot", snapshot(spaces, tabs, nil, panes))
	}
	show(true)
	w.f.handle("workspace.close", func(p map[string]any) any {
		if str(p["workspace_id"]) != id {
			return herdrFail{Code: "wrong_target", Message: "Wrong workspace"}
		}
		show(false)
		return map[string]any{}
	})
	w.f.handle("worktree.remove", func(p map[string]any) any {
		if str(p["workspace_id"]) != id {
			return herdrFail{Code: "wrong_target", Message: "Wrong workspace"}
		}
		args := []string{"worktree", "remove"}
		if p["force"] == true {
			args = append(args, "--force")
		}
		if _, err := git(t.Context(), w.repo, append(args, "--", path)...); err != nil {
			return herdrFail{Code: "remove_failed", Message: err.Error()}
		}
		show(false)
		return map[string]any{}
	})
	return w, id, path
}

func herdrDeletionPreview(t *testing.T, w worktreeFixture, id string, closeOnly bool) string {
	t.Helper()
	res, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview": true, "close_only": closeOnly})
	if err != nil {
		t.Fatal(err)
	}
	if res["kind"] != "herdr" || res["folder"] != closeOnly {
		t.Fatal(res)
	}
	return str(res["preview_token"])
}

func TestHerdrUnusedCleanupPreviewCanBeSurrenderedByItsDevice(t *testing.T) {
	w, id, path := herdrDeletionFixture(t, true)
	preview := map[string]any{"kind": "herdr", "workspace_id": id, "preview": true, "preview_device": "phone"}
	result, err := request(t, w.n, "worktree.delete", preview)
	if err != nil || str(result["preview_token"]) == "" {
		t.Fatalf("preview: %v %v", result, err)
	}
	token := str(result["preview_token"])
	check := map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "preview_device": "phone", "reconcile": true}
	got, err := request(t, w.n, "worktree.delete", check)
	if err != nil || got["state"] != "present" || got["can_revoke_preview"] != true || got["removed"] != false {
		t.Fatalf("capability: %v %v", got, err)
	}
	revoke := maps.Clone(check)
	revoke["revoke_preview"] = true
	for key, value := range map[string]any{"workspace_id": "another", "preview_token": "other", "preview_device": "other", "reconcile": false, "force": true,
		"delete_branch": true, "close_only": true, "preview": true, "kind": "orca"} {
		wrong := maps.Clone(revoke)
		wrong[key] = value
		if result, err := request(t, w.n, "worktree.delete", wrong); err == nil {
			t.Errorf("%s override accepted: %v", key, result)
		}
	}
	if result, err := (&Node{}).herdrWorktreeDelete(t.Context(), id, workspaceDeletionInput{Token: token, Requester: "phone", Reconcile: true, RevokePreview: true}); err == nil {
		t.Fatalf("another node accepted token: %v", result)
	}
	ownerless := maps.Clone(revoke)
	delete(ownerless, "preview_device")
	if result, err := request(t, w.n, "worktree.delete", ownerless); err == nil {
		t.Fatalf("missing device accepted: %v", result)
	}
	for _, body := range []map[string]any{revoke, revoke, check} {
		result, err := request(t, w.n, "worktree.delete", body)
		if err != nil || result["workspace_id"] != id || result["removed"] != false || result["state"] != "preview_revoked" || result["revoked_preview_token"] != token {
			t.Fatalf("revocation receipt: %v %v", result, err)
		}
	}
	for _, device := range []string{"another-device", ""} {
		read := maps.Clone(check)
		read["preview_device"] = device
		result, err := request(t, w.n, "worktree.delete", read)
		if err != nil || result["state"] != "present" || result["revoked_preview_token"] != nil || result["can_revoke_preview"] != nil {
			t.Fatalf("another bearer learned private revocation state: %v %v", result, err)
		}
		delete(read, "reconcile")
		if result, err := request(t, w.n, "worktree.delete", read); err == nil {
			t.Fatalf("another bearer used a surrendered token: %v", result)
		}
	}
	confirm := maps.Clone(check)
	delete(confirm, "reconcile")
	confirm["force"] = true
	if result, err := request(t, w.n, "worktree.delete", confirm); err == nil {
		t.Fatalf("retired token accepted: %v", result)
	}
	if err := os.WriteFile(filepath.Join(path, "app.txt"), []byte("new uncommitted change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = request(t, w.n, "worktree.delete", preview)
	if err != nil || str(result["preview_token"]) == "" || result["preview_token"] == token || result["dirty"] != float64(1) {
		t.Fatalf("independent dirty preview: %v %v", result, err)
	}
	delete(confirm, "force")
	confirm["preview_token"] = result["preview_token"]
	if result, err := request(t, w.n, "worktree.delete", confirm); err == nil {
		t.Fatalf("fresh dirty preview removed without a fresh force decision: %v", result)
	}
	if got := w.f.trace("worktree.remove", "workspace.close"); len(got) != 0 {
		t.Fatalf("preview or revocation changed checkout: %v", got)
	}
	token = herdrDeletionPreview(t, w, id, false) // A preview issued through an older hub has no device identity.
	revoke["preview_token"] = token
	result, err = request(t, w.n, "worktree.delete", revoke)
	if err != nil || result["state"] != "present" || result["can_revoke_preview"] != nil || result["revoked_preview_token"] != nil {
		t.Fatalf("old hub token gained revocation capability: %v %v", result, err)
	}
	result, err = request(t, w.n, "worktree.delete", preview)
	if err != nil {
		t.Fatal(err)
	}
	expired := str(result["preview_token"])
	composerDeletions.Lock()
	composerDeletions.previews[expired].created = time.Now().Add(-25 * time.Hour)
	composerDeletions.Unlock()
	revoke["preview_token"] = expired
	if result, err := request(t, w.n, "worktree.delete", revoke); err == nil {
		t.Fatalf("expired token accepted: %v", result)
	}
}

func TestHerdrCleanupSurrenderRacesAtNativeDispatch(t *testing.T) {
	for _, method := range []string{"session.snapshot", "worktree.remove"} {
		t.Run(method, func(t *testing.T) {
			w, id, path := herdrDeletionFixture(t, true)
			result, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview": true, "preview_device": "phone"})
			if err != nil {
				t.Fatal(err)
			}
			token := str(result["preview_token"])
			confirm := map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "preview_device": "other-full-client"}
			revoke := maps.Clone(confirm)
			revoke["reconcile"], revoke["revoke_preview"], revoke["preview_device"] = true, true, "phone"
			// Keep a live checkout when the native side received the request but its result is unknown.
			w.f.handle("worktree.remove", func(map[string]any) any { return herdrFail{Code: "test_ambiguous", Message: "synthetic lost reply"} })
			entered, proceed := make(chan struct{}), make(chan struct{})
			var gated atomic.Bool
			w.f.after(func(c herdrCall) {
				if c.Method == method && gated.CompareAndSwap(false, true) {
					close(entered)
					<-proceed
				}
			})
			finished := make(chan error, 1)
			go func() { _, err := request(t, w.n, "worktree.delete", confirm); finished <- err }()
			select {
			case <-entered:
			case <-time.After(20 * time.Second):
				close(proceed)
				<-finished
				t.Fatal("confirmation did not reach the native dispatch boundary")
			}
			// While the serial native host is serving the delete, it may not answer its inventory.
			checkCtx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
			result, err = requestCtx(t, checkCtx, w.n, "worktree.delete", revoke)
			cancel()
			close(proceed)
			confirmErr := <-finished
			if confirmErr == nil || err != nil {
				t.Fatalf("confirmation %v, revoke %v %v", confirmErr, result, err)
			}
			if method == "session.snapshot" {
				if result["state"] != "preview_revoked" || result["revoked_preview_token"] != token {
					t.Fatalf("an unspent token was not fenced: %v", result)
				}
			} else if (result["state"] != "present" && result["state"] != "unknown") || result["can_revoke_preview"] != nil || result["revoked_preview_token"] != nil {
				t.Fatalf("an already-dispatched token was surrendered: %v", result)
			}
			if _, err := request(t, w.n, "worktree.delete", confirm); err == nil {
				t.Fatal("late confirm accepted")
			}
			recheck, err := request(t, w.n, "worktree.delete", revoke)
			expected := "preview_revoked"
			if method == "worktree.remove" {
				expected = "present"
			}
			if err != nil || recheck["state"] != expected || recheck["can_revoke_preview"] != nil {
				t.Fatalf("repeated check %v %v", recheck, err)
			}
			want := 0
			if method == "worktree.remove" {
				want = 1
			}
			if got := w.f.trace("worktree.remove", "workspace.close"); len(got) != want {
				t.Fatalf("native mutations %v, want %d", got, want)
			}
			if _, err := os.Stat(filepath.Join(path, "app.txt")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHerdrCleanupPreservesCrossDeviceAndMasterConfirmation(t *testing.T) {
	for _, device := range []string{"other-full-client", ""} {
		t.Run(map[bool]string{true: "master", false: "another-device"}[device == ""], func(t *testing.T) {
			w, id, _ := herdrDeletionFixture(t, true)
			p, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview": true, "preview_device": "phone"})
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": p["preview_token"], "reconcile": true}
			if device != "" {
				body["preview_device"] = device
			}
			read, err := request(t, w.n, "worktree.delete", body)
			if err != nil || read["state"] != "present" || read["can_revoke_preview"] != nil || read["revoked_preview_token"] != nil {
				t.Fatalf("ordinary reconciliation: %v %v", read, err)
			}
			body["revoke_preview"] = true
			if result, err := request(t, w.n, "worktree.delete", body); err == nil {
				t.Fatalf("non-owner surrendered preview: %v", result)
			}
			delete(body, "revoke_preview")
			delete(body, "reconcile")
			result, err := request(t, w.n, "worktree.delete", body)
			if err != nil || result["removed"] != true {
				t.Fatalf("ordinary confirmation: %v %v", result, err)
			}
			body["reconcile"] = true
			for _, requester := range []string{"phone", device} {
				body["preview_device"] = requester
				result, err = request(t, w.n, "worktree.delete", body)
				if err != nil || result["state"] != "removed" || result["can_revoke_preview"] != nil {
					t.Fatalf("completed readback: %v %v", result, err)
				}
			}
			if got := w.f.trace("worktree.remove", "workspace.close"); len(got) != 1 {
				t.Fatalf("native mutations: %v", got)
			}
		})
	}
}

func TestHerdrWorkspaceCloseKeepsMainCheckoutAndConsumesConfirmation(t *testing.T) {
	w, id, path := herdrDeletionFixture(t, false)
	token := herdrDeletionPreview(t, w, id, true)
	body := map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "close_only": true}
	res, err := request(t, w.n, "worktree.delete", body)
	if err != nil || res["removed"] != true {
		t.Fatalf("%v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(path, "app.txt")); err != nil {
		t.Fatal(err)
	}
	if branchOf(t, path) != "main" {
		t.Fatal("main branch changed")
	}
	if _, err := request(t, w.n, "worktree.delete", body); err == nil {
		t.Fatal("confirmation dispatched twice")
	}
	body["reconcile"] = true
	if res, err := request(t, w.n, "worktree.delete", body); err != nil || res["removed"] != true {
		t.Fatalf("reconciliation: %v %v", res, err)
	}
	if got := w.f.trace("workspace.close", "worktree.remove"); len(got) != 1 {
		t.Fatal(got)
	}
}

func TestHerdrLinkedWorktreeRemovalRetainsBranchByDefault(t *testing.T) {
	for _, deleteBranch := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep", true: "merged-branch"}[deleteBranch], func(t *testing.T) {
			w, id, path := herdrDeletionFixture(t, true)
			token := herdrDeletionPreview(t, w, id, false)
			res, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "delete_branch": deleteBranch})
			if err != nil || res["removed"] != true || res["branch_deleted"] == true != deleteBranch {
				t.Fatalf("%v %v", res, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("checkout remains: %v", err)
			}
			if branchExists(t.Context(), w.repo, "topic") == deleteBranch {
				t.Fatal("branch policy mismatch")
			}
		})
	}
}

func TestHerdrDeletionRefusesMainCheckoutAndChangedDirtyPreview(t *testing.T) {
	w, id, _ := herdrDeletionFixture(t, false)
	if _, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview": true}); err == nil {
		t.Fatal("main checkout accepted")
	}
	w, id, path := herdrDeletionFixture(t, true)
	token := herdrDeletionPreview(t, w, id, false)
	if err := os.WriteFile(filepath.Join(path, "app.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "force": true}); err == nil {
		t.Fatal("changed preview was forced")
	}
	token = herdrDeletionPreview(t, w, id, false)
	if _, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token}); err == nil {
		t.Fatal("dirty work removed without confirmation")
	}
	res, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "force": true})
	if err != nil || res["removed"] != true {
		t.Fatalf("confirmed discard: %v %v", res, err)
	}
}

func TestHerdrClosingDoesNotRequireExistingDirectoryOrAuthorizeDeletion(t *testing.T) {
	w, id, path := herdrDeletionFixture(t, false)
	token := herdrDeletionPreview(t, w, id, true)
	if _, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token}); err == nil {
		t.Fatal("close confirmation authorized checkout deletion")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	res, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "close_only": true})
	if err != nil || res["removed"] != true {
		t.Fatalf("close missing folder: %v %v", res, err)
	}
}

func TestHerdrDeletionRequiresObservedRemoval(t *testing.T) {
	w, id, _ := herdrDeletionFixture(t, true)
	w.f.reply("worktree.remove", map[string]any{})
	token := herdrDeletionPreview(t, w, id, false)
	body := map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token}
	res, err := request(t, w.n, "worktree.delete", body)
	if err != nil || res["removed"] == true || res["state"] != "present" {
		t.Fatalf("unverified acknowledgement: %v %v", res, err)
	}
	body["reconcile"] = true
	if _, err := request(t, w.n, "worktree.delete", body); err != nil {
		t.Fatal(err)
	}
	if got := w.f.trace("worktree.remove"); len(got) != 1 {
		t.Fatal(got)
	}
}

func TestHerdrClosePreviewUsesTheCatalogsCanonicalDirectory(t *testing.T) {
	w, id, path := herdrDeletionFixture(t, false)
	link := filepath.Join(w.home, "alias")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	w.f.reply("session.snapshot", snapshot([]any{map[string]any{"workspace_id": id, "cwd": link}}, nil, nil, nil))
	res, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview": true, "close_only": true})
	if err != nil || res["path"] != realPath(path) {
		t.Fatalf("canonical preview: %v %v", res, err)
	}
}

func TestHerdrParentCloseExplainsNativeGroupRequirement(t *testing.T) {
	w, id, _ := herdrDeletionFixture(t, false)
	w.f.reply("workspace.close", herdrFail{Code: "workspace_group_close_required", Message: "Group confirmation required"})
	token := herdrDeletionPreview(t, w, id, true)
	_, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "close_only": true})
	if err == nil || refusalCode(err) != "worktree_herdr_children" {
		t.Fatal(err)
	}
}

func TestHerdrDeletionKeepsUnmergedBranchAndRejectsChangedWorkspace(t *testing.T) {
	w, id, path := herdrDeletionFixture(t, true)
	if err := os.WriteFile(filepath.Join(path, "app.txt"), []byte("topic commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPullGit(t, path, "add", "app.txt")
	runPullGit(t, path, "commit", "-m", "topic work")
	token := herdrDeletionPreview(t, w, id, false)
	res, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "delete_branch": true})
	if err != nil || res["removed"] != true || res["branch_deleted"] == true || !branchExists(t.Context(), w.repo, "topic") {
		t.Fatalf("unmerged branch: %v %v", res, err)
	}
	w, id, _ = herdrDeletionFixture(t, false)
	token = herdrDeletionPreview(t, w, id, true)
	w.f.reply("session.snapshot", snapshot([]any{map[string]any{"workspace_id": id, "cwd": "/different"}}, nil, nil, nil))
	if _, err := request(t, w.n, "worktree.delete", map[string]any{"kind": "herdr", "workspace_id": id, "preview_token": token, "close_only": true}); err == nil {
		t.Fatal("replaced workspace accepted")
	}
	if len(w.f.trace("workspace.close")) != 0 {
		t.Fatal("replaced workspace closed")
	}
}
