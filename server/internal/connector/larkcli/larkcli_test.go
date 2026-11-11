package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func testConnector(t *testing.T, scenario string) *Connector {
	return testConnectorWithProfile(t, scenario, "example")
}

func testConnectorWithProfile(t *testing.T, scenario, profile string) *Connector {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", scenario)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if scenario == "needs_refresh" || scenario == "ready" {
		now = time.Date(2026, 11, 10, 6, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	return &Connector{runner: adapter.New(adapter.Config{Bin: bin, Profile: profile, Timeout: time.Second, Now: func() time.Time {
		return now
	}}), now: func() time.Time { return now }, profile: profile}
}

func TestManifestAndTools(t *testing.T) {
	c := testConnector(t, "success")
	if err := connector.Check(c); err != nil {
		t.Fatal(err)
	}
	if c.ID() != "larkcli" || c.Name() != "Lark via lark-cli" {
		t.Fatalf("identity = %s / %s", c.ID(), c.Name())
	}
	var names []string
	for _, tool := range c.Tools() {
		names = append(names, tool.Name)
		if tool.Description == "" || !strings.HasPrefix(string(tool.InputSchema), `{"type":"object"`) {
			t.Fatalf("bad tool: %+v", tool)
		}
	}
	if strings.Join(names, ",") != "larkcli_chats,larkcli_messages,larkcli_search" {
		t.Fatalf("tools = %v", names)
	}
}

func TestLoginDeviceFlow(t *testing.T) {
	c := testConnector(t, "success")
	p, err := c.LoginStep(context.Background(), "", nil)
	if err != nil || p.Kind != connector.PromptDeviceCode || p.Step != "device" || p.UserCode != "ABCD-1234" ||
		!strings.Contains(p.URL, "accounts.feishu.cn") {
		t.Fatalf("start prompt = %+v, err = %v", p, err)
	}
	p = waitForLogin(t, c)
	if !p.Done || p.Account != "Example User" {
		t.Fatalf("done prompt = %+v", p)
	}
}

func TestLoginPendingFailureAndRestartCancel(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("LARKCLI_FAKE_LOGIN_RELEASE", release)
	c := testConnector(t, "login_wait")
	first, err := c.LoginStep(context.Background(), "", nil)
	if err != nil || first.UserCode != "ABCD-1234" {
		t.Fatalf("first login = %+v, err = %v", first, err)
	}
	second, err := c.LoginStep(context.Background(), "", nil)
	if err != nil || second.UserCode != "ABCD-1234" {
		t.Fatalf("restart login = %+v, err = %v", second, err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if p := waitForLogin(t, c); !p.Done {
		t.Fatalf("restarted login prompt = %+v", p)
	}

	c = testConnector(t, "login_failure")
	if _, err := c.LoginStep(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	if p := waitForLogin(t, c); p.Error == "" || p.Done {
		t.Fatalf("failure prompt = %+v", p)
	}
}

func waitForLogin(t *testing.T, c *Connector) connector.Prompt {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		p, err := c.LoginStep(context.Background(), "device", nil)
		if err != nil {
			t.Fatal(err)
		}
		if p.Done || p.Error != "" {
			return p
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("login did not reach a terminal state: %+v", p)
		}
	}
}

func TestStatusSuccessAndExpiryReminder(t *testing.T) {
	c := testConnector(t, "rolling")
	st := c.Status(context.Background())
	if st.State != connector.StateOK || st.Account != "Example User" || st.Via != "lark-cli" {
		t.Fatalf("status = %+v", st)
	}
	if st.Error != "" {
		t.Fatalf("unexpected reminder: %q", st.Error)
	}

	c = testConnector(t, "refresh_expiring")
	st = c.Status(context.Background())
	if st.State != connector.StateOK || !strings.Contains(st.Error, "1 天") ||
		!strings.Contains(st.Error, "lark-cli --profile example auth login --scope") {
		t.Fatalf("expiry status = %+v", st)
	}
}

func TestStatusRefreshWindowLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scenario  string
		state     string
		wantError string
		wantGuide bool
	}{
		{name: "healthy rolling login", scenario: "rolling", state: connector.StateOK},
		{name: "refresh window nearly used", scenario: "refresh_expiring", state: connector.StateOK, wantError: "1 天"},
		{name: "expired login", scenario: "expired", state: connector.StateNotSignedIn, wantError: "过期", wantGuide: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := testConnectorWithProfile(t, tc.scenario, "").Status(context.Background())
			if st.State != tc.state {
				t.Fatalf("state = %q, want %q; status = %+v", st.State, tc.state, st)
			}
			if tc.wantError == "" && st.Error != "" {
				t.Fatalf("unexpected error = %q", st.Error)
			}
			if tc.wantError != "" && !strings.Contains(st.Error, tc.wantError) {
				t.Fatalf("error = %q, want %q", st.Error, tc.wantError)
			}
			if tc.wantGuide != (len(st.Guide) == 1) {
				t.Fatalf("guide = %#v", st.Guide)
			}
		})
	}
}

func TestLoginGuideUsesConfiguredProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		command string
	}{
		{name: "default profile", command: "lark-cli auth login --scope"},
		{name: "named profile", profile: "example", command: "lark-cli --profile example auth login --scope"},
		{name: "profile with spaces and non ASCII", profile: "工作 profile", command: "lark-cli --profile '工作 profile' auth login --scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := testConnectorWithProfile(t, "not_logged_in", tc.profile).Status(context.Background())
			if len(st.Guide) != 1 || !strings.Contains(st.Guide[0], tc.command) {
				t.Fatalf("guide = %#v, want command %q", st.Guide, tc.command)
			}
		})
	}
}

func TestStatusCategories(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		state    string
	}{
		{"not signed in", "not_logged_in", connector.StateNotSignedIn},
		{"expired", "expired", connector.StateNotSignedIn},
		{"old version", "old", connector.StateError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := testConnector(t, tc.scenario).Status(context.Background())
			if st.State != tc.state || st.Error == "" {
				t.Fatalf("status = %+v", st)
			}
		})
	}
	c := testConnector(t, "success")
	// The CLI status fixture has no missing scopes; exercise the mapped state with
	// a runner error from a read call through the tool path below.
	t.Setenv("LARKCLI_FAKE_SCENARIO", "missing_scope")
	_, err := c.Call(context.Background(), "larkcli_search", json.RawMessage(`{"query":"example"}`))
	if adapter.KindOf(err) != adapter.KindMissingPermission {
		t.Fatalf("kind = %q, err = %v", adapter.KindOf(err), err)
	}
	if !strings.Contains(err.Error(), "im:chat:read") {
		t.Fatalf("missing scope = %v", err)
	}
	c = testConnector(t, "auth_missing_scope")
	st := c.Status(context.Background())
	if st.State != connector.StateNoPermission || !strings.Contains(st.Error, "search:message") || len(st.Guide) != 1 ||
		len(st.MissingScopes) == 0 || st.MissingScopes[0] != "search:message" ||
		!strings.Contains(st.Guide[0], "lark-cli --profile example auth login --scope") ||
		st.GuideURL != "https://"+strings.Join([]string{"open", "larksuite", "com"}, ".")+"/app/cli_example/auth" {
		t.Fatalf("missing scope status = %+v", st)
	}
}

func TestStatusUsesRefreshExpiryAndReportsFeatureScopes(t *testing.T) {
	for _, scenario := range []string{"needs_refresh", "ready"} {
		st := testConnector(t, scenario).Status(context.Background())
		if st.State != connector.StateNoPermission || strings.Contains(st.Error, "not signed") || len(st.Guide) != 1 ||
			!strings.Contains(st.Error, "im:chat:read") || !strings.Contains(st.Guide[0], "lark-cli --profile example auth login --scope") {
			t.Fatalf("%s status = %+v", scenario, st)
		}
	}
}

func TestStatusAcceptsExactlyReadAsMeScopesAndNamesOneGap(t *testing.T) {
	if st := testConnector(t, "success").Status(context.Background()); st.State != connector.StateOK {
		t.Fatalf("all connector scopes should be enough: %+v", st)
	}
	c := testConnector(t, "auth_read_scope_missing")
	st := c.Status(context.Background())
	if st.State != connector.StateNoPermission || len(st.MissingScopes) != 1 || st.MissingScopes[0] != "wiki:node:retrieve" {
		t.Fatalf("one missing connector scope = %+v", st)
	}
}

func TestReadToolsCallTheAdapter(t *testing.T) {
	c := testConnector(t, "success")
	out, err := c.Call(context.Background(), "larkcli_chats", json.RawMessage(`{"filter":"recent","limit":2}`))
	if err != nil || !strings.Contains(out, "oc_example_group") {
		t.Fatalf("chats = %s, err = %v", out, err)
	}
	out, err = c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_group"}`))
	if err != nil || !strings.Contains(out, "om_example_older") {
		t.Fatalf("messages = %s, err = %v", out, err)
	}
	out, err = c.Call(context.Background(), "larkcli_search", json.RawMessage(`{"query":"example","kind":"messages"}`))
	if err != nil || !strings.Contains(out, "om_example_search") {
		t.Fatalf("search = %s, err = %v", out, err)
	}
}

func TestDisplayNameFollowsLanguage(t *testing.T) {
	c := New(adapter.Config{Bin: "lark-cli"})
	if got := connector.DisplayName(i18n.With(context.Background(), i18n.ZhHans), c); got != "飞书 · 我的消息" { // i18n-ignore: the zh-Hans catalog entry under test
		t.Fatalf("zh-Hans name = %q", got)
	}
	if got := connector.DisplayName(i18n.With(context.Background(), "en"), c); got != "Lark · my messages" {
		t.Fatalf("en name = %q", got)
	}
	if c.Name() != Name {
		t.Fatalf("stable name changed: %q", c.Name())
	}
}
