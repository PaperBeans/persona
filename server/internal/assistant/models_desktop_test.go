package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Claude's desktop app as the launcher of Model configuration › Agents (models_desktop.go).

// desktopHub: mac's Claude desktop app keeps Claude Code sessions, so its list names claude-desktop (and the Codex
// App's codex-app); l4 has Claude Code and a launcher of its own, and no desktop app.
func desktopHub(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.ownModels = true
	f.sessions["l4"], f.sessions["mac"] = nil, nil
	f.devices = []Device{
		{ID: "server", Name: "server", Home: "/home/me", Online: true, Reachable: true},
		{ID: "l4", Name: "l4", Home: "/home/me", Online: true, Reachable: true, ClaudeVersion: "2.1.0"},
		{ID: "mac", Name: "mac", Home: "/Users/me", Online: true, Reachable: true, ClaudeVersion: "2.1.0"},
	}
	f.zshrc = map[string][]proto.Launcher{
		"server": {{Name: "claude", Source: "builtin", Agent: "claude"}},
		"l4":     {{Name: "other", Source: "zshrc"}},
		"mac": {
			{Name: "claude-desktop", Source: proto.SourceClaudeDesktop, Agent: "claude"},
			{Name: "codex-app", Source: "codex_app", Agent: "codex"},
		},
	}
	return newServiceOn(t, f, &profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}), f
}

// chooseDesktopForAgents saves Agents on the desktop app's launcher as Settings does.
func chooseDesktopForAgents(t *testing.T, s *Service, extra string) *http.Response {
	t.Helper()
	p := purposeOf(t, s, "agent.new")
	r := modelRequest(s, "PATCH", "agent.new", fmt.Sprintf(`{"revision":%d,"provider":"launcher.claude-desktop"%s}`, p.Revision, extra))
	return r.Result()
}

// Settings › Model configuration offers the desktop app to Agents only, named for people and with the computers that
// have it, and Agents saved on it runs on it. The Codex App is not offered anywhere; nor are the main chat, the API
// uses, and the Inbox AI twin's and Lark /agent's own choices.
func TestDesktopAppIsAgentsProviderOnly(t *testing.T) {
	s, f := desktopHub(t)
	l := s.requestCtxLang(context.Background())
	agents := purposeOf(t, s, "agent.new")
	if !slices.Contains(agents.ProviderIDs, "launcher.claude-desktop") {
		t.Fatalf("Agents does not offer the desktop app: %v", agents.ProviderIDs)
	}
	for _, id := range []string{"assistant", purposeMain, purposeSupervise, purposeMeetingRoom, purposeAgent, purposeLarkAgent, "kb", "inbox.triage"} {
		if p := purposeOf(t, s, id); slices.Contains(p.ProviderIDs, "launcher.claude-desktop") {
			t.Fatalf("%s offers the desktop app: %v", id, p.ProviderIDs)
		}
	}
	for _, p := range []string{"agent.new", "assistant", purposeAgent} {
		if slices.ContainsFunc(purposeOf(t, s, p).ProviderIDs, func(id string) bool { return id == "launcher.codex-app" }) {
			t.Fatalf("%s offers the Codex App", p)
		}
	}
	var catalog struct {
		Providers []ModelProvider `json:"providers"`
	}
	if r := modelRequest(s, "GET", "", ""); r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &catalog) != nil {
		t.Fatalf("catalog: %d %s", r.Code, r.Body.String())
	}
	i := slices.IndexFunc(catalog.Providers, func(p ModelProvider) bool { return p.ID == "launcher.claude-desktop" })
	if i < 0 {
		t.Fatalf("no desktop provider: %+v", catalog.Providers)
	}
	if p := catalog.Providers[i]; p.Name != mc(l, "provider_claude_desktop") || p.Kind != "launcher" || p.Agent != "claude" ||
		p.Source != proto.SourceClaudeDesktop || !slices.Equal(p.Machines, []string{"mac"}) {
		t.Fatalf("desktop provider: %+v", p)
	}

	// A node has no command for the app's launcher to check a model with: the save does not ask it.
	f.answerNode(assistantwire.DeviceLaunchersModelCheck, func(_ string, raw json.RawMessage) (any, error) {
		var args struct{ Name string }
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, err
		}
		if args.Name == claudeDesktopLauncher {
			return nil, errors.New("launcher claude-desktop not found")
		}
		return map[string]any{"agent": launcherAgent(args.Name)}, nil
	})
	if r := chooseDesktopForAgents(t, s, `,"model":"opus","effort":"high"`); r.StatusCode != http.StatusOK {
		t.Fatalf("save: %d", r.StatusCode)
	}
	agents = purposeOf(t, s, "agent.new")
	if agents.Effective.Provider != "launcher.claude-desktop" || agents.Effective.Model != "opus" || agents.Effective.Effort != "high" ||
		agents.Status != modelUnverified {
		t.Fatalf("Agents after the save: %+v (%s)", agents.Effective, agents.Status)
	}
	// The app runs no read-only plan start: the permission mode Plan is refused for it.
	if r := chooseDesktopForAgents(t, s, `,"permission_mode":"plan"`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("plan saved: %d", r.StatusCode)
	}
}

// The Inbox AI twin and Lark /agent follow Agents but run headless: on the desktop app's launcher they run on Claude
// Code, and their row says why; one set apart keeps its own choice.
func TestHeadlessAgentsUsesDoNotFollowTheDesktopApp(t *testing.T) {
	s, _ := desktopHub(t)
	l := s.requestCtxLang(context.Background())
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude-desktop", Model: "opus"})
	for _, id := range []string{purposeAgent, purposeLarkAgent} {
		p := purposeOf(t, s, id)
		if p.Effective.Provider != "launcher.claude" || p.Effective.Model != "" || p.InheritedFrom != mc(l, "source_desktop_fallback") ||
			p.Status == mc(l, "status_provider_incompatible") {
			t.Fatalf("%s: %+v from %q (%s)", id, p.Effective, p.InheritedFrom, p.Status)
		}
		b, err := s.resolveModel(context.Background(), id)
		if err != nil || b.Provider != "launcher.claude" {
			t.Fatalf("%s resolves to %+v %v", id, b, err)
		}
	}
	setModel(t, s, purposeAgent, ModelBinding{Provider: "launcher.claude", Model: "sonnet"})
	if p := purposeOf(t, s, purposeAgent); p.Effective.Model != "sonnet" || p.InheritedFrom != "" {
		t.Fatalf("set apart: %+v from %q", p.Effective, p.InheritedFrom)
	}
	// Following another launcher, they follow it again.
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.other"})
	if p := purposeOf(t, s, purposeLarkAgent); p.Effective.Provider != "launcher.other" || p.InheritedFrom != purposeName(l, "agent.new") {
		t.Fatalf("Lark /agent after Agents moved: %+v from %q", p.Effective, p.InheritedFrom)
	}
}

// A person's New Agent start follows the default into the app where the computer has it; on one without it, the
// form's default and the start are the launcher that computer runs instead, with no error. A start Persona makes
// itself never opens the app from the default; one the caller names still does.
func TestDesktopAppDefaultOnEachComputer(t *testing.T) {
	s, f := desktopHub(t)
	ctx := context.Background()
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude-desktop", Model: "opus", Effort: "high"})

	// New Agent's form: the default on each computer, as GET /machines/{mid}/launchers says it.
	defaultOn := func(machine string) map[string]any {
		t.Helper()
		r := request(s, "GET", "/api/v1/machines/"+machine+"/launchers", "")
		var out struct {
			Launchers []ConfiguredLauncher `json:"launchers"`
			Default   map[string]any       `json:"default"`
		}
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &out) != nil {
			t.Fatalf("%s: %d %s", machine, r.Code, r.Body.String())
		}
		return out.Default
	}
	if d := defaultOn("mac"); d["provider"] != "claude-desktop" || d["model"] != "opus" || d["effort"] != "high" {
		t.Fatalf("mac's default: %v", d)
	}
	if d := defaultOn("l4"); d["provider"] != "claude" || d["model"] != "" || d["effort"] != "" {
		t.Fatalf("l4's default: %v", d)
	}
	// Hidden from the pickers, a default still runs (#4659): the form says so where the computer has it.
	if err := s.putSetting(ctx, launchersHiddenKey, []string{"claude-desktop"}); err != nil {
		t.Fatal(err)
	}
	if d := defaultOn("mac"); d["provider"] != "claude-desktop" {
		t.Fatalf("mac's default, hidden: %v", d)
	}
	if d := defaultOn("l4"); d["provider"] != "claude" {
		t.Fatalf("l4's default, hidden: %v", d)
	}
	if err := s.putSetting(ctx, launchersHiddenKey, []string{}); err != nil {
		t.Fatal(err)
	}

	// The start a person makes with no launcher picked.
	params, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "mac", Params: map[string]any{"cwd": "", "launcher": ""}})
	if err != nil || params["launcher"] != "claude-desktop" || params["model"] != "opus" || params["effort"] != "high" {
		t.Fatalf("mac: %v %v", params, err)
	}
	params, err = s.AgentStartParams(ctx, AgentStartParams{MachineID: "l4", Params: map[string]any{"cwd": "/home/me/code", "launcher": ""}})
	if err != nil || params["launcher"] != "claude" || params["model"] != nil || params["effort"] != nil {
		t.Fatalf("l4: %v %v", params, err)
	}

	// agent_start on mac: Claude Code, and the answer says why; naming the launcher opens it in the app.
	out, err := s.runTool(ctx, "server", "conv9", "agent_start", json.RawMessage(`{"machine":"mac","cwd":"/Users/me/code","prompt":"Reply with pong"}`))
	if err != nil {
		t.Fatal(err)
	}
	if st := startsOf(f)[0]; st["launcher"] != "claude" || st["model"] != nil || !strings.Contains(out, "Note: ") || !strings.Contains(out, "claude-desktop") {
		t.Fatalf("agent_start: %v\n%s", st, out)
	}
	if _, err := s.runTool(ctx, "server", "conv9", "agent_start", json.RawMessage(`{"machine":"mac","cwd":"/Users/me/code","prompt":"Reply with pong","launcher":"claude-desktop"}`)); err != nil {
		t.Fatal(err)
	}
	if st := startsOf(f)[1]; st["launcher"] != "claude-desktop" {
		t.Fatalf("agent_start naming it: %v", st)
	}
	// A model the caller names goes with it to Claude Code.
	out, err = s.runTool(ctx, "server", "conv9", "agent_start", json.RawMessage(`{"machine":"mac","cwd":"/Users/me/code","prompt":"Reply with pong","model":"sonnet"}`))
	if err != nil {
		t.Fatal(err)
	}
	if st := startsOf(f)[2]; st["launcher"] != "claude" || st["model"] != "sonnet" || !strings.Contains(out, i18n.T(s.lang(ctx), "assistant-tasks.launcher_desktop_default_model",
		i18n.Args{"launcher": "claude-desktop", "machine": "mac", "to": "claude", "model": "sonnet"})) {
		t.Fatalf("agent_start with a model: %v\n%s", st, out)
	}
	// A message resuming an Agent started outside Persona goes on Claude Code, as before.
	params, err = s.AgentResumeParams(ctx, AgentResumeParams{MachineID: "mac", SessionID: "outside-1", Params: map[string]any{}})
	if err != nil || params["launcher"] != "claude" {
		t.Fatalf("resume: %v %v", params, err)
	}
}

// A computer whose list can't be read is not second-guessed for a person's New Agent start, which opens in the app as
// before; a start Persona makes there still never opens the app, though the last list the hub saw names it.
func TestDesktopAppDefaultOnAnUnreadableList(t *testing.T) {
	s, f := desktopHub(t)
	ctx := context.Background()
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.claude-desktop", Model: "opus"})
	f.mu.Lock()
	delete(f.zshrc, "mac")
	f.launcherList = nil // mac's live list can't be read
	f.mu.Unlock()
	s.models.zshrcMu.Lock()
	s.models.zshrc = map[string][]proto.Launcher{"mac": {{Name: claudeDesktopLauncher, Source: proto.SourceClaudeDesktop, Agent: "claude"}}}
	s.models.zshrcMu.Unlock()

	params, err := s.AgentStartParams(ctx, AgentStartParams{MachineID: "mac", Params: map[string]any{"cwd": "", "launcher": ""}})
	if err != nil || params["launcher"] != claudeDesktopLauncher {
		t.Fatalf("New Agent: %v %v", params, err)
	}
	out, err := s.runTool(ctx, "server", "conv9", "agent_start", json.RawMessage(`{"machine":"mac","cwd":"/Users/me/code","prompt":"Reply with pong"}`))
	if err != nil {
		t.Fatal(err)
	}
	if st := startsOf(f)[0]; st["launcher"] != "claude" || st["model"] != nil || !strings.Contains(out, "claude-desktop") {
		t.Fatalf("agent_start: %v\n%s", st, out)
	}
}

// Settings' model list for the desktop app comes from a computer that has it.
func TestDesktopAppModelListInSettings(t *testing.T) {
	s, f := desktopHub(t)
	f.mu.Lock()
	f.agentModels = map[string][]proto.AgentModel{"claude-desktop": {{ID: "opus", Name: "Opus"}}}
	f.mu.Unlock()
	r := request(s, "GET", "/api/v1/model-config/providers/launcher.claude-desktop/models", "")
	var out struct {
		proto.LauncherModels
		Machine string `json:"machine"`
	}
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &out) != nil {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if out.Machine != "mac" || len(out.Models) != 1 || out.Models[0].ID != "opus" {
		t.Fatalf("list: %+v", out)
	}
}
