package extmgr

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestUnselectedNodeSyncPreservesOtherMachineStates(t *testing.T) {
	ctx := context.Background()
	h, _ := extensionTestHub(t)
	reg := h.extensions.reg
	// Drive each sync explicitly so the old snapshot and the newer states have a
	// deterministic order, without changing the production synchronization path.
	reg.OnChange = nil
	pv, err := reg.Preview(ctx, "dir:"+helperPackage(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Install(ctx, pv.ID, pv.Digest, "test"); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetSecret(ctx, "example-helper", "api_key", "fixture-key", "test"); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetConfig(ctx, "example-helper", json.RawMessage(`{"computers":["box"]}`), "test"); err != nil {
		t.Fatal(err)
	}
	beforeSelection := reg.SnapshotExact("example-helper")
	if err := reg.SetConfig(ctx, "example-helper", json.RawMessage(`{"computers":["box","oldbox","win"]}`), "test"); err != nil {
		t.Fatal(err)
	}
	box, _ := extensionNodeMachine(t, h, "box", "linux", "amd64", false)
	oldbox, _ := extensionNodeMachine(t, h, "oldbox", "darwin", "arm64", true)
	other, _ := extensionNodeMachine(t, h, "other", "linux", "amd64", false)
	win, _ := extensionNodeMachine(t, h, "win", "windows", "amd64", false)
	for _, m := range []*Machine{box, oldbox} {
		if err := h.syncExtensionNodes(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	check := func() {
		t.Helper()
		views := map[string]ExtensionNodeView{}
		for _, view := range h.extensions.view(ctx, reg.SnapshotExact("example-helper")).Nodes {
			views[view.MachineID] = view
		}
		if views["box"].State != "running" || views["oldbox"].State != extensionNodeOutdated || len(views) != 2 {
			t.Fatalf("selected computer states changed: %+v", views)
		}
	}
	check()

	// A sync can pass nodeSnapshotCurrent, then lose its turn while another
	// computer finishes syncing a newer configuration. Its old selection must
	// not give it authority to discard those other computers' states.
	h.extensions.keepNodeState("example-helper", "1.0.0", "other", proto.PluginNodeState{ID: "example-helper", Version: "1.0.0", State: "running"})
	if prog := h.syncExtensionNode(ctx, beforeSelection, other, true); prog != nil {
		t.Fatalf("an unselected computer kept a program: %+v", prog)
	}
	check()

	// A picked computer without a compatible program still removes its own
	// previous state, but cannot discard any other computer's state either.
	h.extensions.keepNodeState("example-helper", "1.0.0", "win", proto.PluginNodeState{ID: "example-helper", Version: "1.0.0", State: "running"})
	if err := h.syncExtensionNodes(ctx, win); err != nil {
		t.Fatal(err)
	}
	check()

	// A computer removed from the fleet cannot sync its own row away. Preserve
	// cleanup of unselected rows even when their machine no longer exists.
	h.extensions.keepNodeState("example-helper", "1.0.0", "gone", proto.PluginNodeState{ID: "example-helper", Version: "1.0.0", State: "running"})
	if err := h.syncExtensionNodes(ctx, other); err != nil {
		t.Fatal(err)
	}
	check()
}
