package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestWorkSessionNativeCatalogAndStart(t *testing.T) {
	cliHome := fakeCLIHome(t)
	cli := filepath.Join(cliHome, "bin", "claude")
	data, err := os.ReadFile(cli)
	if err != nil {
		t.Fatal(err)
	}
	list := strings.Replace(string(data), `"displayName":"Default"}]`, `"displayName":"Default"},{"value":"model-of-mine","displayName":"Project model"}]`, 1)
	if err := os.WriteFile(cli, []byte(list), 0o700); err != nil {
		t.Fatal(err)
	}
	n, herdr, cwd := herdrNode(t)
	startHerdr(herdr, "w1:p2", proto.StatusUnknown)
	ctx := context.Background()
	opts, err := n.workSessionOptions(ctx, "mine", "", "", false, true)
	if err != nil || opts.AgentType != "claude" || opts.ModelMode != "selectable" || !opts.Attachments || len(opts.Models) != 1 || opts.Models[0].ID != "model-of-mine" {
		t.Fatalf("native options %+v: %v", opts, err)
	}
	fixed, err := n.workSessionOptions(ctx, "fixed", "", "", false, true)
	if err != nil || fixed.ModelMode != "fixed" || fixed.FixedModel != "fixed-model" || len(fixed.Models) != 0 {
		t.Fatalf("fixed options %+v: %v", fixed, err)
	}
	start := func(launcher, agent, model string) error {
		raw, _ := json.Marshal(map[string]any{"cwd": cwd, "workspace_id": "w1", "launcher": launcher, "agent_type": agent, "model": model, "async": true})
		_, err := n.handle(ctx, "session.start.work", raw)
		return err
	}
	for _, c := range []struct{ launcher, agent, model, code string }{
		{"mine", "claude", "not-in-catalog", "work_model_unavailable"},
		{"mine", "codex", "model-of-mine", "work_harness_changed"},
		{"fixed", "claude", "fixed-model", "work_model_unavailable"},
		{"unknown", "claude", "model-of-mine", "work_launcher_unavailable"},
	} {
		code, data := refusalOf(t, start(c.launcher, c.agent, c.model))
		if code != c.code || data["delivered"] != "none" {
			t.Fatalf("%+v: %q %v", c, code, data)
		}
	}
	if calls := herdr.trace("pane.send_input"); len(calls) != 0 {
		t.Fatalf("refusals reached the terminal: %v", calls)
	}
	if err := start("mine", "claude", "model-of-mine"); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, herdr, "pane.send_input", 1)
	if got := strings.Join(herdr.trace("pane.send_input"), "\n"); !strings.Contains(got, "--model") || !strings.Contains(got, "model-of-mine") {
		t.Fatalf("model did not reach the launch command: %s", got)
	}
}

func TestWorkSessionRuntimeDoesNotAcceptModelOverride(t *testing.T) {
	n, _ := runtimeNode(t)
	rt := dialRuntime(t, n, dshHello())
	name := n.runtimes.launchers()[0].Name
	ctx := context.Background()
	opts, err := n.workSessionOptions(ctx, name, "", "", false, true)
	if err != nil || opts.ModelMode != "default" || !opts.InlinePrompt || len(opts.Models) != 0 {
		t.Fatalf("runtime options %+v: %v", opts, err)
	}
	raw, _ := json.Marshal(map[string]string{"cwd": n.home, "launcher": name, "agent_type": opts.AgentType, "model": "override", "prompt": "Start work"})
	_, err = n.handle(ctx, "session.start.work", raw)
	if code, data := refusalOf(t, err); code != "work_model_unavailable" || data["delivered"] != "none" {
		t.Fatalf("%q %v", code, data)
	}
	if got := rt.got("session.start"); len(got) != 0 {
		t.Fatalf("override reached the runtime: %s", got)
	}
}
