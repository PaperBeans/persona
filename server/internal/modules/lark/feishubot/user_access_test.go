package feishubot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
)

var testUserScopes = []string{"offline_access", "docx:document:readonly", "wiki:wiki:readonly"}

const testRedirect = "https://hub.example.com/lark-oauth"

// The config call adds the scopes as user scopes and the redirect URL to the security settings, and nothing else;
// the publish follows.
func TestConfigureUserAccessAddsUserScopesAndRedirect(t *testing.T) {
	noBackoff(t)
	var sent map[string]any
	c, configHits, publishHits := configureSrv(t,
		func(in map[string]any) (int, string) { sent = in; return 0, "" },
		ok0)

	rep := c.ConfigureUserAccess(t.Context(), testRedirect, testUserScopes)

	if !rep.Configured || !rep.Published || rep.Issue != IssueNone || rep.VersionID != "oav_1" {
		t.Fatalf("report = %+v", rep)
	}
	if configHits.Load() != 1 || publishHits.Load() != 1 {
		t.Fatalf("config %d publish %d calls, want 1 and 1", configHits.Load(), publishHits.Load())
	}
	var names []string
	for _, s := range sent["scope"].(map[string]any)["add_scopes"].([]any) {
		m := s.(map[string]any)
		if m["token_type"] != "user" {
			t.Errorf("scope %v: token_type %v, want user", m["scope_name"], m["token_type"])
		}
		names = append(names, m["scope_name"].(string))
	}
	if !slices.Equal(names, testUserScopes) {
		t.Fatalf("add_scopes = %v, want %v", names, testUserScopes)
	}
	urls := sent["security"].(map[string]any)["add"].(map[string]any)["redirect_urls"].([]any)
	if len(urls) != 1 || urls[0] != testRedirect {
		t.Fatalf("redirect_urls = %v, want [%s]", urls, testRedirect)
	}
	if _, ok := sent["event"]; ok {
		t.Fatalf("the user access must not touch the events: %v", sent)
	}
}

// A version already under review is published but waits for the admin: the report keeps saying so.
func TestConfigureUserAccessUnderReviewAwaits(t *testing.T) {
	noBackoff(t)
	c, _, publishHits := configureSrv(t, ok0, func(map[string]any) (int, string) { return 210302, "under review" })

	rep := c.ConfigureUserAccess(t.Context(), testRedirect, testUserScopes)

	if !rep.Configured || !rep.Published || rep.Issue != IssueUnderReview {
		t.Fatalf("report = %+v, want configured, published and under review", rep)
	}
	if publishHits.Load() != 1 {
		t.Fatalf("publish called %d times", publishHits.Load())
	}
}

// Without application:application:patch the config API refuses (230002 / 230003, or the gateway's 99991672 / 99991676):
// nothing is published and the report names the missing scope.
func TestConfigureUserAccessWithoutPatchScope(t *testing.T) {
	for _, code := range []int{230002, 230003, 99991672, 99991676} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			noBackoff(t)
			c, _, publishHits := configureSrv(t, func(map[string]any) (int, string) { return code, "no permission" }, ok0)

			rep := c.ConfigureUserAccess(t.Context(), testRedirect, testUserScopes)

			if rep.Configured || rep.Published || rep.Issue != IssuePatchScope {
				t.Fatalf("%d: report = %+v, want the patch scope issue", code, rep)
			}
			if publishHits.Load() != 0 {
				t.Fatalf("%d: publish called %d times after a refused config", code, publishHits.Load())
			}
		})
	}
	// Feishu's gateway returns HTTP 400 with code 99991672 when application:application:patch is missing:
	// the call must not retry and must still map to IssuePatchScope.
	t.Run("http400_99991672", func(t *testing.T) {
		noBackoff(t)
		var configHits, publishHits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/open-apis/auth/v3/tenant_access_token/internal":
				okJSON(w, map[string]any{"code": 0, "tenant_access_token": "tok", "expire": 7200})
			case "/open-apis/application/v7/applications/cli_x/config":
				configHits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 99991672,
					"msg":  "Access denied. One of the following scopes is required: [application:application:patch]",
				})
			case "/open-apis/application/v7/applications/cli_x/publish":
				publishHits.Add(1)
				okJSON(w, map[string]any{"code": 0})
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()
		c := New("cli_x", "secret", srv.URL).WithConsole(ConsoleURL("feishu"))
		rep := c.ConfigureUserAccess(t.Context(), testRedirect, testUserScopes)
		if rep.Configured || rep.Published || rep.Issue != IssuePatchScope {
			t.Fatalf("HTTP 400 / 99991672: report = %+v, want IssuePatchScope", rep)
		}
		if configHits.Load() != 1 || publishHits.Load() != 0 {
			t.Fatalf("config %d publish %d calls, want 1 and 0", configHits.Load(), publishHits.Load())
		}
	})
}

func TestConfigureUserAccessSkips(t *testing.T) {
	if rep := New("cli_x", "secret", "https://feishu.example.internal").ConfigureUserAccess(t.Context(), testRedirect, testUserScopes); !rep.Skipped || rep.Issue != IssueNone {
		t.Fatalf("private: report = %+v, want skipped without an issue", rep)
	}
	if rep := New("", "", "feishu").ConfigureUserAccess(t.Context(), testRedirect, testUserScopes); !rep.Skipped || rep.Issue != IssueNoApp {
		t.Fatalf("no app id: report = %+v, want skipped no_app", rep)
	}
}

// WithConsole lets a client whose calls go to another address still configure an app of one of the two clouds.
func TestWithConsole(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	c := New("cli_x", "secret", srv.URL)
	if c.consoleURL() != "" {
		t.Fatalf("console of a test server = %q, want \"\"", c.consoleURL())
	}
	if got := c.WithConsole(ConsoleURL("feishu") + "/").consoleURL(); got != DomainFeishu+"/app" {
		t.Fatalf("console = %q, want %q", got, DomainFeishu+"/app")
	}
	if got := c.WithConsole("").consoleURL(); got != "" {
		t.Fatalf("an empty console = %q, want the domain's (\"\")", got)
	}
}
