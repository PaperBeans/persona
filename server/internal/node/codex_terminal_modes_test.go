package node

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
)

// Model and effort of a Codex on the terminal route (codex_terminal_modes.go), against a fake herdr whose Codex answers
// a typed /model the way Codex 0.169 does: thread_settings_applied in its rollout, or its refusal on the screen.

// cxModelsCache is the Codex home's models_cache.json in these tests: gpt-a takes low to high, gpt-b low, medium and
// max (low its default), gpt-c no level at all.
const cxModelsCache = `{"models":[
{"slug":"gpt-a","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}]},
{"slug":"gpt-b","default_reasoning_level":"low","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"max"}]},
{"slug":"gpt-c","supported_reasoning_levels":[]}]}`

// cxTerminalNode is codexNode's Codex on the terminal route, its session started by Codex version, running model at
// effort (its rollout's turn_context), with cxModelsCache in its home and an empty box on the screen.
func cxTerminalNode(t *testing.T, status, version, model, effort string) (*Node, *fakeHerdr, string) {
	t.Helper()
	oldWait, oldLook, oldRetry := codexSettingsWait, codexSettingsLook, codexStartingRetry
	codexSettingsWait, codexSettingsLook, codexStartingRetry = 300*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { codexSettingsWait, codexSettingsLook, codexStartingRetry = oldWait, oldLook, oldRetry })
	n, f, path := codexNode(t, status)
	meta := strings.Replace(cxMeta, `"cli_version":"0.163.0"`, `"cli_version":"`+version+`"`, 1)
	turn := `{"timestamp":"2026-11-01T13:40:01.000Z","type":"turn_context","payload":{"model":"` + model + `","effort":"` + effort + `"}}`
	if err := os.WriteFile(path, []byte(cxToday(meta+"\n"+turn)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(n.codexHome(), "models_cache.json"), []byte(cxModelsCache), 0o600); err != nil {
		t.Fatal(err)
	}
	f.setScreen(cxPane, cxEmpty)
	return n, f, path
}

// cxSettingsLine is the thread_settings_applied Codex writes when it applies model and effort ("" effort: null).
func cxSettingsLine(model, effort string) string {
	var e any
	if effort != "" {
		e = effort
	}
	b, _ := json.Marshal(map[string]any{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "type": "event_msg",
		"payload": map[string]any{"type": "thread_settings_applied", "thread_id": cxID,
			"thread_settings": map[string]any{"model": model, "reasoning_effort": e, "approval_policy": "on-request"}}})
	return string(b)
}

// cxModelAnswers makes the Codex in cxPane, running model, take a typed /model as Codex 0.169 does: the settings it
// applies go to its rollout (no --reasoning: the new model's default from cxModelsCache), unless refuse returns the
// line Codex prints instead (a refusal, "" for none).
func cxModelAnswers(t *testing.T, f *fakeHerdr, path, model string, refuse func(cmd string) string) {
	t.Helper()
	var mu sync.Mutex
	defaults := map[string]string{"gpt-a": "medium", "gpt-b": "low"}
	f.after(func(c herdrCall) {
		cmd := str(c.Params["text"])
		if c.Method != "agent.prompt" || !strings.HasPrefix(cmd, "/model") {
			return
		}
		if refuse != nil {
			if line := refuse(cmd); line != "" {
				f.setScreen(cxPane, "• Done.\n\n■ "+line+"\n\n› Ask Codex to do anything\n\n  gpt-6 low · /private/tmp/project\n")
				return
			}
		}
		mu.Lock()
		defer mu.Unlock()
		effort := ""
		for _, arg := range strings.Fields(cmd)[1:] {
			if level, ok := strings.CutPrefix(arg, "--reasoning="); ok {
				effort = level
			} else {
				model, effort = arg, defaults[arg]
			}
		}
		appendLine(t, path, cxSettingsLine(model, effort))
	})
}

// cxTyped is every line the node typed into Codex.
func cxTyped(f *fakeHerdr) []string {
	var typed []string
	for _, c := range f.calls() {
		if c.Method == "agent.prompt" {
			typed = append(typed, str(c.Params["text"]))
		}
	}
	return typed
}

func cxModes(t *testing.T, n *Node, params map[string]any) error {
	t.Helper()
	params["session_id"] = cxID
	_, err := request(t, n, "session.modes", params)
	return err
}

// A terminal-route Codex that takes /model with arguments offers what an app-server thread offers: the model, and the
// efforts of the model it runs (its home's models_cache.json; Codex's whole list for a model the cache does not name;
// none for a model that takes none). An older Codex's session offers neither.
func TestCodexTerminalRow(t *testing.T) {
	for _, c := range []struct {
		name, version, model string
		canModel             bool
		efforts              []string
		effort               string
	}{
		{"listed", "0.169.1", "gpt-a", true, []string{"low", "medium", "high"}, "medium"},
		{"prerelease", "0.171.0-alpha.13", "gpt-a", true, []string{"low", "medium", "high"}, "medium"},
		{"not in the cache", "0.169.1", "gpt-z", true, codexEffortLevels, "medium"},
		{"no levels", "0.169.1", "gpt-c", true, nil, ""},
		{"older Codex", "0.168.2", "gpt-a", false, nil, "medium"},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, _, _ := cxTerminalNode(t, proto.StatusIdle, c.version, c.model, "medium")
			s := paneRow(t, n, cxID)
			if got := s.Capabilities.Model != nil && *s.Capabilities.Model; got != c.canModel {
				t.Errorf("capabilities.model %v", got)
			}
			if !slices.Equal(s.Efforts, c.efforts) || s.Effort != c.effort {
				t.Errorf("efforts %v, effort %q", s.Efforts, s.Effort)
			}
		})
	}
}

// The phone's pick goes in as one /model line, and is made once the rollout records it: an effort alone; a model,
// keeping the effort the new model takes (Codex would put it on its default); a model that does not take it, which
// Codex puts on its default; both at once. Mid-turn too. The row shows the new values from the record.
func TestCodexTerminalModesTyped(t *testing.T) {
	for _, c := range []struct {
		name, status, effortNow string
		params                  map[string]any
		typed                   string
		model, effort           string
	}{
		{"effort", proto.StatusIdle, "medium", map[string]any{"effort": "high"}, "/model --reasoning=high", "gpt-a", "high"},
		{"model keeps the effort", proto.StatusIdle, "medium", map[string]any{"model": "gpt-b"}, "/model gpt-b --reasoning=medium", "gpt-b", "medium"},
		{"model it does not take", proto.StatusIdle, "high", map[string]any{"model": "gpt-b"}, "/model gpt-b", "gpt-b", "low"},
		{"both", proto.StatusIdle, "medium", map[string]any{"model": "gpt-b", "effort": "max"}, "/model gpt-b --reasoning=max", "gpt-b", "max"},
		{"mid-turn", proto.StatusWorking, "medium", map[string]any{"effort": "low"}, "/model --reasoning=low", "gpt-a", "low"},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, f, path := cxTerminalNode(t, c.status, "0.169.1", "gpt-a", c.effortNow)
			cxModelAnswers(t, f, path, "gpt-a", nil)
			if err := cxModes(t, n, c.params); err != nil {
				t.Fatal(err)
			}
			if typed := cxTyped(f); len(typed) != 1 || typed[0] != c.typed {
				t.Fatalf("typed %q", typed)
			}
			if s := paneRow(t, n, cxID); s.Model != c.model || s.Effort != c.effort {
				t.Errorf("row %q / %q", s.Model, s.Effort)
			}
		})
	}
	t.Run("what it runs already", func(t *testing.T) {
		n, f, path := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		cxModelAnswers(t, f, path, "gpt-a", nil)
		for _, p := range []map[string]any{{"effort": "medium"}, {"model": "gpt-a"}, {"model": "gpt-a", "effort": "medium"}} {
			if err := cxModes(t, n, p); err != nil {
				t.Fatal(err)
			}
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
}

// Every refusal says why, with nothing typed when it is the node's own: a permission mode, a level the model does not
// take, an older Codex, no rollout yet, a draft in the box, a dialog on the screen, a botmux pane, a subagent. Codex's
// own refusal is passed on in its words; a Codex still starting gets one more try, and its second answer is passed on;
// a change the rollout never shows is unconfirmed, typed once.
func TestCodexTerminalModesRefused(t *testing.T) {
	t.Run("permission mode", func(t *testing.T) {
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		if err := cxModes(t, n, map[string]any{"permission_mode": "plan", "effort": "high"}); err == nil || !strings.Contains(err.Error(), "权限模式要在电脑上改") {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("level the model does not take", func(t *testing.T) {
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		if err := cxModes(t, n, map[string]any{"effort": "max"}); err == nil || !strings.Contains(err.Error(), "low / medium / high") {
			t.Fatalf("err %v", err)
		}
		if err := cxModes(t, n, map[string]any{"model": "gpt-c", "effort": "low"}); err == nil || !strings.Contains(err.Error(), "不支持设置思考强度") {
			t.Fatalf("a model with no levels: %v", err)
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("older Codex", func(t *testing.T) {
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.163.0", "gpt-a", "medium")
		if err := cxModes(t, n, map[string]any{"effort": "high"}); refusalCode(err) != proto.CodeModesCodexVersion {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("no rollout yet", func(t *testing.T) {
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		fresh := "01a19756-0000-7000-8000-000000000001"
		n.mu.Lock()
		n.agents[fresh] = host.Agent{Agent: "codex", PaneID: "w1:p2", AgentStatus: proto.StatusIdle}
		n.mu.Unlock()
		if _, err := request(t, n, "session.modes", map[string]any{"session_id": fresh, "effort": "high"}); refusalCode(err) != proto.CodeModesCodexNoRollout {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("draft in the box", func(t *testing.T) {
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		f.setScreen(cxPane, cxBox("half typed"))
		if err := cxModes(t, n, map[string]any{"effort": "high"}); refusalCode(err) != screen.InputDraft {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("Codex refuses", func(t *testing.T) {
		n, f, path := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		words := "Model 'gpt-x' is unavailable. Use /model to see available models."
		cxModelAnswers(t, f, path, "gpt-a", func(string) string { return words })
		err := cxModes(t, n, map[string]any{"model": "gpt-x"})
		if refusalCode(err) != proto.CodeModesCodexRefused || err.Error() != words {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 1 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("still starting", func(t *testing.T) {
		n, f, path := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		var mu sync.Mutex
		tries := 0
		cxModelAnswers(t, f, path, "gpt-a", func(string) string {
			mu.Lock()
			defer mu.Unlock()
			if tries++; tries == 1 {
				return "Session is still starting; try /model again in a moment."
			}
			f.setScreen(cxPane, cxEmpty)
			return ""
		})
		if err := cxModes(t, n, map[string]any{"effort": "high"}); err != nil {
			t.Fatal(err)
		}
		if typed := cxTyped(f); len(typed) != 2 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("unconfirmed", func(t *testing.T) {
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		err := cxModes(t, n, map[string]any{"effort": "high"})
		var re *rpc.Error
		if refusalCode(err) != proto.CodeModesCodexUnconfirmed || !errors.As(err, &re) || deliveredOf(re) != deliveredUnknown {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 1 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("still starting twice", func(t *testing.T) {
		// Codex prints its answer again below the first: the second try's answer is new, and passed on.
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		words := "Session is still starting; try /model again in a moment."
		var mu sync.Mutex
		shown := "• Done.\n\n"
		f.after(func(c herdrCall) {
			if c.Method != "agent.prompt" || !strings.HasPrefix(str(c.Params["text"]), "/model") {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			shown += "■ " + words + "\n\n"
			f.setScreen(cxPane, shown+"› Ask Codex to do anything\n\n  gpt-6 low · /private/tmp/project\n")
		})
		err := cxModes(t, n, map[string]any{"effort": "high"})
		if refusalCode(err) != proto.CodeModesCodexRefused || err.Error() != words {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 2 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("dialog on the screen", func(t *testing.T) {
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		f.setScreen(cxPane, screenFixture(t, "codex-0.163.0-exec-approval-120.txt"))
		if err := cxModes(t, n, map[string]any{"effort": "high"}); refusalCode(err) != screen.DialogOpen {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("botmux pane", func(t *testing.T) {
		// botmux hands what is typed over as a whole message: /model would reach the model as a turn.
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		n.mu.Lock()
		a := n.agents[cxID]
		a.PaneID = host.Botmux + ":b1"
		n.agents[cxID] = a
		n.mu.Unlock()
		if s := paneRow(t, n, cxID); s.Capabilities != nil && s.Capabilities.Model != nil && *s.Capabilities.Model || len(s.Efforts) != 0 {
			t.Fatalf("a botmux pane offers model %v, efforts %v", s.Capabilities.Model, s.Efforts)
		}
		if err := cxModes(t, n, map[string]any{"effort": "high"}); err == nil || !strings.Contains(err.Error(), "Codex / TRAE CLI 的会话不支持") {
			t.Fatalf("err %v", err)
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
	t.Run("subagent", func(t *testing.T) {
		// Its parent drives it, through the app-server the parent runs on: nothing is typed into the pane.
		n, f, _ := cxTerminalNode(t, proto.StatusIdle, "0.169.1", "gpt-a", "medium")
		n.codices = newCodexRegistry()
		home := canonicalCodexHome(filepath.Join(n.home, ".codex"), n.home)
		n.codices.config.LegacyHome = home // the default home keeps the thread ids as they are
		if id := n.registerCodex(home, cxID, ""); id != cxID {
			t.Fatalf("registered as %q", id)
		}
		n.codices.remotes = map[string]*codexRemote{"daemon": {n: n, spec: proto.CodexServer{ID: "daemon", Home: home}, ready: true,
			threads: map[string]remoteThread{cxID: {id: cxID, publicID: cxID, parent: "01a19756-0000-7000-8000-0000000000aa"}}}}
		if _, _, ok := n.codexOnTerminal(cxID); ok {
			t.Fatal("a subagent counted as on the terminal route")
		}
		if err := cxModes(t, n, map[string]any{"effort": "high"}); err == nil {
			t.Fatal("a subagent's effort changed")
		}
		if typed := cxTyped(f); len(typed) != 0 {
			t.Fatalf("typed %q", typed)
		}
	})
}

func TestCodexModelArgsCommand(t *testing.T) {
	for _, c := range []struct{ model, effort, want string }{
		{"gpt-6.2-astra", "high", "/model gpt-6.2-astra --reasoning=high"},
		{"", "ultra", "/model --reasoning=ultra"},
		{"acme/gpt-5-pro", "", "/model acme/gpt-5-pro"},
		{"gpt-5.1", "none", "/model gpt-5.1 --reasoning=none"}, // a model's own level beyond Codex's settings list
	} {
		if got, err := codexModelArgsCommand(c.model, c.effort); err != nil || got != c.want {
			t.Errorf("%q %q: %q %v", c.model, c.effort, got, err)
		}
	}
	for _, c := range [][2]string{{"", ""}, {"gpt x", ""}, {"--reasoning=high", ""}, {"gpt-a", "x-high"}, {"gpt-a", "High"}, {"gpt-a", "high --speed=fast"}} {
		if got, err := codexModelArgsCommand(c[0], c[1]); err == nil {
			t.Errorf("%q %q made %q", c[0], c[1], got)
		}
	}
}

// Codex's refusals as 0.169 to 0.171 print them, each only when it is new on the screen (one more copy than before
// counts), at the start of its line and about what was typed.
func TestCodexModelArgsOutcome(t *testing.T) {
	box := "\n› Ask Codex to do anything\n\n  GPT-6.2-Astra medium · ~/project\n"
	for _, c := range []struct {
		name, before, after, model, effort string
		code, words                        string
	}{
		{"model", box, "■ Model 'gpt-9' is unavailable. Use /model to see available models." + box, "gpt-9", "", proto.CodeModesCodexRefused, "Model 'gpt-9' is unavailable. Use /model to see available models."},
		{"level", box, "■ Reasoning level 'ultra' is unavailable for gpt-6.2-sol. Available: low, medium." + box, "gpt-6.2-sol", "ultra", proto.CodeModesCodexRefused, "Reasoning level 'ultra' is unavailable for gpt-6.2-sol. Available: low, medium."},
		{"account list", box, "■ Your ChatGPT model list doesn't include `gpt-9`. Check the spelling or use /model to choose another model." + box, "gpt-9", "", proto.CodeModesCodexRefused, "Your ChatGPT model list doesn't include `gpt-9`. Check the spelling or use /model to choose another model."},
		{"reserve", box, "■ Model arguments are unavailable while using Reserve. Use /model to adjust effort." + box, "", "high", proto.CodeModesCodexRefused, "Model arguments are unavailable while using Reserve. Use /model to adjust effort."},
		{"starting", box, "■ Session is still starting; try /model again in a moment." + box, "", "high", "", "Session is still starting; try /model again in a moment."},
		{"changed", box, "• Model changed to gpt-6.2-astra high for this session only" + box, "", "high", "", ""},
		{"an earlier answer", "■ Model 'gpt-9' is unavailable. Use /model to see available models." + box, "■ Model 'gpt-9' is unavailable. Use /model to see available models." + box, "gpt-9", "", "", ""},
		{"busy", box, "■ '/model' is disabled while a task is in progress." + box, "", "high", proto.CodeModesCodexRefused, "'/model' is disabled while a task is in progress."},
		{"the same answer again", "■ Model 'gpt-9' is unavailable. Use /model to see available models." + box, "■ Model 'gpt-9' is unavailable. Use /model to see available models.\n\n■ Model 'gpt-9' is unavailable. Use /model to see available models." + box, "gpt-9", "", proto.CodeModesCodexRefused, "Model 'gpt-9' is unavailable. Use /model to see available models."},
		{"quoted mid-line", box, "• The tool said: Unexpected argument 'x'. Use '--reasoning=<level>' or '--speed=<tier>'." + box, "", "high", "", ""},
		{"another model", box, "■ Model 'gpt-8' is unavailable. Use /model to see available models." + box, "gpt-9", "", "", ""},
	} {
		err := codexModelArgsOutcome(c.before, c.after, c.model, c.effort)
		var starting codexStarting
		switch {
		case c.words == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.words != "" && (err == nil || err.Error() != c.words):
			t.Errorf("%s: %v", c.name, err)
		case c.code != "" && refusalCode(err) != c.code:
			t.Errorf("%s: code %q", c.name, refusalCode(err))
		case c.words != "" && c.code == "" && !errors.As(err, &starting):
			t.Errorf("%s: not a retry: %v", c.name, err)
		}
	}
}

// Only Codex's record written after the typing counts, and only with the values typed; a level alone matches whatever
// model, a model alone whatever level.
func TestCodexSettingsRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	earlier := cxSettingsLine("gpt-b", "max") + "\n"
	if err := os.WriteFile(path, []byte(earlier+cxSettingsLine("gpt-a", "")+"\n"+cxSettingsLine("gpt-a", "low")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	from := int64(len(earlier))
	for _, c := range []struct {
		model, effort string
		want          bool
	}{
		{"gpt-a", "low", true}, {"", "low", true}, {"gpt-a", "", true},
		{"gpt-b", "max", false}, {"", "max", false}, {"gpt-a", "high", false},
	} {
		if got := codexSettingsRecorded(path, from, c.model, c.effort); got != c.want {
			t.Errorf("%q %q: %v", c.model, c.effort, got)
		}
	}
	if codexSettingsRecorded(filepath.Join(t.TempDir(), "none.jsonl"), 0, "", "low") {
		t.Error("a missing rollout recorded a change")
	}
}

func TestCodexHomeOf(t *testing.T) {
	home := filepath.Join("/tmp", "h", ".codex")
	if got := codexHomeOf(filepath.Join(home, "sessions", "2026", "11", "21", "rollout-x.jsonl")); got != home {
		t.Errorf("home %q", got)
	}
	if got := codexHomeOf(filepath.Join(home, "archived_sessions", "rollout-x.jsonl")); got != "" {
		t.Errorf("an archived rollout's home %q", got)
	}
}
