package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

// The same names as integrations/runtime-sdk/test/items.test.js: the two tables must not drift apart.
func TestSecretKey(t *testing.T) {
	for _, k := range []string{"token", "api_key", "apiKey", "APIKey", "API_KEY", "x-api-key", "authorization", "Authorization",
		"password", "PASSWORD", "passwd", "client_secret", "cookie", "Set-Cookie", "credentials", "session_id", "sessionId",
		"private_key", "privateKey", "headers", "bearer", "Bearer", "totp", "pin", "dsn", "database_dsn", "connection_string",
		"connStr", "webhook_url", "slackWebhook", "pwd", "pass", "jwt", "access_key", "accessKeyId", "key", "signature", "auth",
		"x-auth", "auth_code", "otp", "SSHKey", "GPGKey", "AWSKey", "DBPass", "HTTPAuth", "MFAPin", "PGPKeys"} {
		if !SecretKey(k) {
			t.Errorf("%s: want a credential", k)
		}
	}
	for _, k := range []string{"author", "authorName", "oauth_scope", "session_name", "keyword", "passage", "monkey",
		"repo", "user", "name", "region", "OAuthScope", "URLPath", "HTTPMethod", "IDList", ""} {
		if SecretKey(k) {
			t.Errorf("%s: not a credential", k)
		}
	}
}

func TestSecretEnvNameProblem(t *testing.T) {
	invalid := "it is not an environment variable name (capital letters, digits and _, at most 64)"
	cases := []struct {
		label, name, want string
	}{
		{"letter", "A", ""},
		{"underscore", "_", ""},
		{"digits after first", "_A1", ""},
		{"maximum length", "A" + strings.Repeat("0", 63), ""},
		{"empty", "", invalid},
		{"leading digit", "1NAME", invalid},
		{"lowercase", "Name", invalid},
		{"punctuation", "TOKEN-NAME", invalid},
		{"non ASCII", "Å_NAME", invalid},
		{"leading newline", "\nNAME", invalid},
		{"trailing newline", "NAME\n", invalid},
		{"too long", "A" + strings.Repeat("0", 64), invalid},
		{"reserved name", "PATH", "the computer or Persona sets it"},
		{"reserved prefix", "GIT_TOKEN", "names starting with GIT_ are the computer's or Persona's"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			t.Parallel()
			if got := SecretEnvNameProblem(c.name); got != c.want {
				t.Errorf("SecretEnvNameProblem(%q) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestArgGist(t *testing.T) {
	cases := []struct{ input, want string }{
		{`{"api_key":"fake-1","repo":"demo"}`, "repo: demo"},
		{`{"password":"fake-2","user":"bob"}`, "user: bob"},
		{`{"Token":"fake-3","Region":"cn"}`, "Region: cn"},
		{`{"zeta":"z","alpha":"a"}`, "zeta: z"}, // the order given, not sorted
		{`{"blank":"  ","n":3,"obj":{"s":"x"},"list":["x"],"last":"y"}`, "last: y"},
		{`{"secret":"fake-4"}`, ""},
		{`{}`, ""},
		{`["a"]`, ""},
		{`"text"`, ""},
		{``, ""},
		{`{bad`, ""},
	}
	for _, c := range cases {
		if got := ArgGist(json.RawMessage(c.input)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.input, got, c.want)
		}
	}
}

func TestRedactArgs(t *testing.T) {
	cases := []struct{ input, want string }{
		{`{"repo":"demo","n":1.50}`, `{"n":1.50,"repo":"demo"}`},
		{`{"api_key":"fake-1","repo":"demo"}`, `{"api_key":"***","repo":"demo"}`},
		{`{"ApiKey":"fake-1","PASSWORD":"fake-2","X-Api-Key":"fake-3"}`, `{"ApiKey":"***","PASSWORD":"***","X-Api-Key":"***"}`},
		{`{"cfg":{"db":{"password":"fake-1","host":"h"}}}`, `{"cfg":{"db":{"host":"h","password":"***"}}}`},
		{`{"items":[{"token":"fake-1"},{"name":"a"},"plain"]}`, `{"items":[{"token":"***"},{"name":"a"},"plain"]}`},
		{`[{"cookie":"fake-1"}]`, `[{"cookie":"***"}]`},
		{`{"headers":{"Authorization":"fake-1"},"private_key":{"pem":"fake-2"}}`, `{"headers":"***","private_key":"***"}`},
		{`{"author":"bob","keyword":"go"}`, `{"author":"bob","keyword":"go"}`},
		{`{}`, `{}`},
		{``, ``},
		{`{bad`, ``},
	}
	for _, c := range cases {
		if got := RedactArgs(json.RawMessage(c.input)); got != c.want {
			t.Errorf("%s: got %s, want %s", c.input, got, c.want)
		}
	}
}
