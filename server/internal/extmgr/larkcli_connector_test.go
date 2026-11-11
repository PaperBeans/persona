package extmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/lark"
)

func TestLarkCLIConnectorOnlyRegistersWhenConfigured(t *testing.T) {
	h := newTestManager(t)
	h.connectors = h.newConnectors(nil)
	h.cfg.Lark = lark.Config{}
	h.registerConnectors()
	if h.connectors.Get("larkcli") != nil {
		t.Fatal("larkcli registered without lark.cli")
	}

	h = newTestManager(t)
	h.connectors = h.newConnectors(nil)
	bin, err := filepath.Abs(filepath.Join("..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: bin, Profile: "example"}}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	h.registerConnectors()
	if h.connectors.Get("larkcli") == nil {
		t.Fatal("larkcli was not registered with lark.cli")
	}
	var found bool
	for _, info := range h.listedConnectors(context.Background(), true) {
		if info.ID == "larkcli" {
			found = true
			if info.Status != "ok" || info.Account != "Example User" {
				t.Fatalf("settings connector = %+v", info)
			}
		}
	}
	if !found {
		t.Fatal("larkcli missing from settings connector list")
	}
}

func TestLarkCLIEffectiveProfileUsesBotAppAndStdinSecret(t *testing.T) {
	h := newTestManager(t)
	h.connectors = h.newConnectors(nil)
	bin, err := filepath.Abs(filepath.Join("..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	initLog := filepath.Join(t.TempDir(), "init.log")
	t.Setenv("LARKCLI_FAKE_SCENARIO", "config_different")
	t.Setenv("LARKCLI_FAKE_INIT_LOG", initLog)
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: bin}}
	h.cfg.Bots.Feishu = FeishuBotConfig{AppID: "cli_bot", AppSecret: "bot-secret", Domain: "feishu"}
	if got := h.effectiveLarkCLIProfile(context.Background()); got != managedLarkCLIProfile {
		t.Fatalf("profile = %q", got)
	}
	if err := h.syncLarkCLIProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(initLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(first); !strings.Contains(got, "--app-id cli_bot") || !strings.Contains(got, "stdin=bot-secret") || strings.Contains(strings.SplitN(got, "|", 2)[0], "bot-secret") {
		t.Fatalf("init record = %q", got)
	}
	// Once the managed profile reports the same app id, another manager start
	// or credentials refresh must not re-run init.
	t.Setenv("LARKCLI_FAKE_SCENARIO", "config_same")
	if err := h.syncLarkCLIProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	same, _ := os.ReadFile(initLog)
	if string(same) != string(first) {
		t.Fatalf("same app reinitialized: %q", same)
	}
	// A changed app id is the one case that rebinds the managed profile.
	h.cfg.Bots.Feishu.AppID = "cli_new"
	t.Setenv("LARKCLI_FAKE_SCENARIO", "config_different")
	if err := h.syncLarkCLIProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	changed, _ := os.ReadFile(initLog)
	if strings.Count(string(changed), "config init") != 2 || !strings.Contains(string(changed), "--app-id cli_new") {
		t.Fatalf("changed app did not reinitialize: %q", changed)
	}
}

func TestLarkCLIEffectiveProfilePrecedenceAndDefault(t *testing.T) {
	h := newTestManager(t)
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: "/bin/false", Profile: "explicit"}}
	h.cfg.Bots.Feishu = FeishuBotConfig{AppID: "cli_bot", AppSecret: "bot-secret"}
	if got := h.effectiveLarkCLIProfile(context.Background()); got != "explicit" {
		t.Fatalf("explicit profile = %q", got)
	}
	h.cfg.Lark.CLI.Profile = ""
	if got := h.effectiveLarkCLIProfile(context.Background()); got != managedLarkCLIProfile {
		t.Fatalf("bot profile = %q", got)
	}
	h.cfg.Bots.Feishu = FeishuBotConfig{}
	if got := h.effectiveLarkCLIProfile(context.Background()); got != "" {
		t.Fatalf("default profile = %q", got)
	}
}

func TestLarkCLIExplicitProfileSkipsBotBinding(t *testing.T) {
	h := newTestManager(t)
	bin, err := filepath.Abs(filepath.Join("..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	initLog := filepath.Join(t.TempDir(), "init.log")
	t.Setenv("LARKCLI_FAKE_SCENARIO", "config_different")
	t.Setenv("LARKCLI_FAKE_INIT_LOG", initLog)
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: bin, Profile: "advanced"}}
	h.cfg.Bots.Feishu = FeishuBotConfig{AppID: "cli_bot", AppSecret: "bot-secret"}
	if err := h.syncLarkCLIProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.effectiveLarkCLIProfile(context.Background()); got != "advanced" {
		t.Fatalf("profile = %q", got)
	}
	if _, err := os.Stat(initLog); err == nil {
		t.Fatal("explicit profile unexpectedly initialized managed profile")
	}
}

func TestAssistantLarkUsesManagedProfile(t *testing.T) {
	h := newTestManager(t)
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: "/opt/lark/bin/lark-cli"}}
	h.cfg.Bots.Feishu = FeishuBotConfig{AppID: "cli_bot", AppSecret: "bot-secret"}
	got, err := h.AssistantLark(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.CLI == nil || got.CLI.Profile != managedLarkCLIProfile {
		t.Fatalf("assistant profile = %+v", got.CLI)
	}
}

func TestLarkCLIBrand(t *testing.T) {
	for _, tc := range []struct{ domain, want string }{
		{"", "feishu"}, {"feishu", "feishu"}, {"lark", "lark"}, {"larksuite", "lark"}, {"https://platform.larksuite.com", "lark"},
	} {
		if got := larkCLIBrand(tc.domain); got != tc.want {
			t.Errorf("domain %q = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestBotViewExposesLarkCLIBinding(t *testing.T) {
	h := newTestManager(t)
	bin, err := filepath.Abs(filepath.Join("..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: bin}}
	h.cfg.Bots.Feishu = FeishuBotConfig{AppID: "cli_example", AppSecret: "bot-secret"}
	view := h.botView(context.Background())
	if view.LarkCLI == nil || view.LarkCLI.Profile != managedLarkCLIProfile || view.LarkCLI.AppID != "cli_example" || !view.LarkCLI.SameApp || view.LarkCLI.Mode != "bot" || view.LarkCLI.Choice != "" || len(view.LarkCLI.Profiles) != 2 {
		t.Fatalf("larkcli view = %+v", view.LarkCLI)
	}
	// The managed profile is "follow the bot's app" itself: it is never listed as a separate choice, nor accepted as one.
	for _, p := range view.LarkCLI.Profiles {
		if p.Name == managedLarkCLIProfile {
			t.Fatalf("managed profile listed: %+v", view.LarkCLI.Profiles)
		}
	}
}

func TestLarkCLIAppProfileChoicePrecedenceAndValidation(t *testing.T) {
	h := newTestManager(t)
	h.store = newBotStore()
	bin, err := filepath.Abs(filepath.Join("..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: bin}}
	if err := h.store.PutSetting(context.Background(), botSettingsKey, FeishuBotSettings{LarkCLIProfile: "other"}); err != nil {
		t.Fatal(err)
	}
	if got := h.effectiveLarkCLIProfile(context.Background()); got != "other" {
		t.Fatalf("chosen profile = %q", got)
	}
	if got := h.larkCLIMode(context.Background()); got != "chosen" {
		t.Fatalf("mode = %q", got)
	}
	if err := h.validateLarkCLIProfile(context.Background(), "other"); err != nil {
		t.Fatalf("existing profile rejected: %v", err)
	}
	if err := h.validateLarkCLIProfile(context.Background(), "missing"); err == nil {
		t.Fatal("missing profile accepted")
	}
	h.cfg.Lark.CLI.Profile = "fixed"
	if got := h.effectiveLarkCLIProfile(context.Background()); got != "fixed" {
		t.Fatalf("config profile did not win: %q", got)
	}
	if got := h.larkCLIMode(context.Background()); got != "config" {
		t.Fatalf("config mode = %q", got)
	}
}

func TestLarkCLIProfileRouteRejectsManagerConfigChoice(t *testing.T) {
	h := newTestManager(t)
	h.store = newBotStore()
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: "/bin/false", Profile: "fixed"}}
	mux := http.NewServeMux()
	h.botRoutes(mux)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/bots/feishu", strings.NewReader(`{"larkcli_profile":"other"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != "larkcli_profile_fixed" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestLarkCLIProfileRouteStoresExistingChoice(t *testing.T) {
	h := newTestManager(t)
	h.store = newBotStore()
	bin, err := filepath.Abs(filepath.Join("..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", "success")
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Bin: bin}}
	mux := http.NewServeMux()
	h.botRoutes(mux)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/bots/feishu", strings.NewReader(`{"larkcli_profile":"other"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var view feishuBotView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.LarkCLI == nil || view.LarkCLI.Choice != "other" || view.LarkCLI.Mode != "chosen" {
		t.Fatalf("view = %+v", view.LarkCLI)
	}
	if got := h.larkCLIProfileChoice(context.Background()); got != "other" {
		t.Fatalf("saved choice = %q", got)
	}
}
