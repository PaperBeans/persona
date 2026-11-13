package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// runDevs answers model.run as a node would, or with err.
type runDevs struct {
	viaDevs
	saw []proto.ModelRunParams
}

func (d *runDevs) CallNode(ctx context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	if method != proto.MethodModelRun || id != "server" {
		return nil, fmt.Errorf("unexpected %s on %s", method, id)
	}
	var p proto.ModelRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	d.saw = append(d.saw, p)
	if d.err != nil {
		return nil, d.err
	}
	return json.Marshal(proto.ModelRunAnswer{Text: "answer to " + p.Prompt, Model: "m-run"})
}

// An API purpose may pick plain claude (no gateway, no key): Model configuration offers it with the main chat's computer,
// and its requests run there as model.run with the system prompt, the turns and the chosen model; "off" leaves the
// effort to the launcher. Offline, logged out and old nodes say so; with no main chat computer the status says to choose
// one; Codex and other CLIs are not offered, and a launcher since deleted is an error, not a run.
func TestAPIPurposeRunsOnALauncher(t *testing.T) {
	s, f, _ := modelService(t)
	devs := &runDevs{}
	s.devs = devs

	p := purposeOf(t, s, "kb")
	if p.MachineID != "server" || !slices.Contains(p.ProviderIDs, "launcher.claude") {
		t.Fatalf("kb offers %v on %q", p.ProviderIDs, p.MachineID)
	}
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude","model":"bad model"}`); r.Code != 400 {
		t.Fatalf("bad model id: %d %s", r.Code, r.Body.String())
	}
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude","model":"m-1"}`); r.Code != 200 {
		t.Fatalf("pick claude: %d %s", r.Code, r.Body.String())
	}
	if p := purposeOf(t, s, "kb"); p.Status != modelUnverified {
		t.Fatalf("status: %q", p.Status)
	}
	text, err := s.purposeModel("kb.compress").Complete(context.Background(), "sys", "hi", 64)
	if err != nil || text != "answer to hi" || len(devs.saw) != 1 {
		t.Fatalf("complete: %q %v %d", text, err, len(devs.saw))
	}
	if got := devs.saw[0]; got.Launcher != "claude" || got.Model != "m-1" || got.System != "sys" || got.TimeoutMS <= 0 {
		t.Fatalf("params: %+v", got)
	}
	reply, err := s.purposeModel("kb").Chat(context.Background(), ai.Request{Messages: []ai.Message{
		{Role: ai.RoleUser, Content: "first"}, {Role: ai.RoleAssistant, Content: "reply"}, {Role: ai.RoleUser, Content: "second"}}})
	if err != nil || reply.Model != "m-run" {
		t.Fatalf("chat: %+v %v", reply, err)
	}
	if in := devs.saw[1].Prompt; !strings.Contains(in, "User:\nfirst") || !strings.Contains(in, "Assistant:\nreply") || !strings.HasSuffix(strings.TrimSpace(strings.Split(in, "second")[0]), "User:") {
		t.Fatalf("transcript: %q", in)
	}

	// Triage defaults to "off", an API word: the launcher's own effort.
	if r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"provider":"launcher.claude"}`); r.Code != 200 {
		t.Fatalf("triage: %d %s", r.Code, r.Body.String())
	}
	if _, err := s.purposeModel("inbox.judge").Complete(context.Background(), "", "x", 16); err != nil || devs.saw[2].Effort != "" {
		t.Fatalf("triage run: %v %+v", err, devs.saw[2])
	}

	lang := s.lang(context.Background())
	args := i18n.Args{"name": "server", "launcher": "claude"}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("server: %w", apierr.ErrOffline), i18n.T(lang, "assistant-models.err_run_offline", args)},
		{&rpc.Error{Code: proto.CodeModelRunLoggedOut, Message: "x"}, i18n.T(lang, "assistant-models.err_run_logged_out", args)},
		{&rpc.Error{Code: proto.CodeModelRunTimeout, Message: "x"}, i18n.T(lang, "assistant-models.err_run_timeout", args)},
		{&rpc.Error{Code: proto.CodeNodeOutdated, Message: "Update the node"}, "Update the node"},
	} {
		devs.err = tc.err
		if _, err := s.purposeModel("kb").Complete(context.Background(), "", "x", 16); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.err, err, tc.want)
		}
	}

	// Not Claude Code: not offered, not run (codex exec can't be kept from its shell and MCP servers).
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "anti", Agent: "agy", API: "responses"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "cx", Agent: "codex", API: "responses"}})
	if p := purposeOf(t, s, "kb"); slices.Contains(p.ProviderIDs, "launcher.anti") || slices.Contains(p.ProviderIDs, "launcher.cx") {
		t.Fatalf("offered: %v", p.ProviderIDs)
	}
	for _, name := range []string{"cx", "gone"} {
		if _, err := s.apiConfig(context.Background(), ModelBinding{Provider: "launcher." + name}); err == nil {
			t.Fatalf("%s runs", name)
		}
	}

	// No main chat computer (the only one is a work computer): still offered, and the status names the setting.
	f.hello.Assistant.WorkMachines = []string{"server"}
	s.settingsChanged(context.Background())
	if p := purposeOf(t, s, "kb"); !slices.Contains(p.ProviderIDs, "launcher.claude") || p.Status != i18n.T(lang, "assistant-models.err_run_no_machine") {
		t.Fatalf("no machine: %v %q", p.ProviderIDs, p.Status)
	}
}

// Several turns go to the CLI as one transcript (tools/launcher_run_input).
func TestGoldenLauncherRunInput(t *testing.T) {
	golden(t, "launcher_run_input", runPrompt([]ai.Message{{Role: ai.RoleUser, Content: "first"}, {Role: ai.RoleAssistant, Content: "reply"}, {Role: ai.RoleUser, Content: "second"}}))
}
