package extmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// terminalLocale sets the environment i18n.Terminal reads to exactly env (the rest unset), whatever the machine
// running the tests has.
func terminalLocale(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range i18n.TerminalEnv {
		t.Setenv(name, env[name])
	}
}

// writeHubJSON points the command at listen with token, as hub.json on the hub's machine does.
func writeHubJSON(t *testing.T, listen, token string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PERSONA_HOME", home)
	t.Setenv("PERSONA_LISTEN", "")
	b, err := json.Marshal(map[string]string{"listen": listen, "token": token})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hub.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// `personad extension …` speaks the terminal's language (#5520): what it prints itself, and what the hub words for
// it, which it asks for in the same language instead of the user language an app last set.
func TestExtensionCommandSpeaksTheTerminalLanguage(t *testing.T) {
	const token = "extension-cli-lang-token"
	var mu sync.Mutex
	var asked []string // Accept-Language of each call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		asked = append(asked, r.Header.Get("Accept-Language"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/extensions":
			_, _ = w.Write([]byte(`{"extensions":[]}`))
		case "/api/v1/extensions/-/markets":
			_ = json.NewEncoder(w).Encode(map[string]any{"markets": []MarketView{{ID: "m1", Name: "Example", Extensions: 1,
				Spec: "github:example/plugins", Official: true, Signature: MarketSignatureView{State: extension.SigSigned, KeyID: "k1"}}}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not here"}`))
		}
	}))
	defer srv.Close()
	han := regexp.MustCompile(`\p{Han}`)
	for _, tt := range []struct {
		name   string
		env    map[string]string
		accept string
		want   []string
	}{
		{"no locale", nil, "en", []string{"No extensions", "1 extension ", "signed k1", "official"}},
		{"C", map[string]string{"LANG": "C.UTF-8"}, "en", []string{"No extensions", "1 extension ", "signed k1"}},
		{"Japanese", map[string]string{"LANG": "ja_JP.UTF-8"}, "ja", []string{"拡張機能はありません", "拡張機能1件", "署名済み：k1", "公式"}},
		{"Chinese", map[string]string{"LC_ALL": "zh_CN.UTF-8", "LANG": "en_US.UTF-8"}, "zh-Hans", []string{"没有扩展", "1 个扩展", "已签名 k1", "官方"}},
		{"PERSONA_LANG", map[string]string{"PERSONA_LANG": "en", "LANG": "zh_CN.UTF-8"}, "en", []string{"No extensions"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			terminalLocale(t, tt.env)
			writeHubJSON(t, strings.TrimPrefix(srv.URL, "http://"), token)
			mu.Lock()
			asked = nil
			mu.Unlock()
			var out bytes.Buffer
			for _, args := range [][]string{{"list"}, {"market", "list"}} {
				if err := ExtensionCommand(context.Background(), args, strings.NewReader(""), &out, &out); err != nil {
					t.Fatalf("%v: %v", args, err)
				}
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("no %q in:\n%s", want, out.String())
				}
			}
			if tt.accept == string(i18n.En) && han.MatchString(out.String()) {
				t.Errorf("Chinese in %s output:\n%s", tt.accept, out.String())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(asked) != 2 {
				t.Fatalf("calls %v, want 2", asked)
			}
			for _, a := range asked {
				if a != tt.accept {
					t.Errorf("Accept-Language %q, want %q", a, tt.accept)
				}
			}
		})
	}
}

// Its own errors read in the terminal's language too: no hub here, a hub that is not running, a hub's refusal.
func TestExtensionCommandErrorsInTheTerminalLanguage(t *testing.T) {
	terminalLocale(t, map[string]string{"LANG": "en_US.UTF-8"})
	t.Setenv("PERSONA_HOME", t.TempDir())
	err := ExtensionCommand(context.Background(), []string{"list"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "There is no hub configuration on this machine") {
		t.Fatalf("no hub.json: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	writeHubJSON(t, addr, "t")
	err = ExtensionCommand(context.Background(), []string{"list"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "The hub isn’t running. Start the hub first" {
		t.Fatalf("hub down: %v", err)
	}

	terminalLocale(t, map[string]string{"LANG": "ja_JP.UTF-8"})
	err = ExtensionCommand(context.Background(), []string{"install", "market:no-slash"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "の形で指定してください") {
		t.Fatalf("bad market source: %v", err)
	}
}

// The install preview: what the command words itself is in the terminal's language, around the permission lines the
// hub wrote in that language.
func TestPrintPreviewInTheTerminalLanguage(t *testing.T) {
	pv := &extension.Preview{
		Manifest:        &extension.Manifest{ID: "example-notes", Name: "Notes", Version: "1.0.0", Publisher: "Example"},
		Permissions:     []extension.PermissionLine{{Title: "Run a program on the hub machine", Detail: "bin/notes"}},
		Warnings:        []string{"unsigned package"},
		MorePermissions: true,
	}
	var b strings.Builder
	printPreview(i18n.En, &b, pv)
	want := "Install example-notes 1.0.0 (Notes)\nPublisher: Example\nIt can:\n  · Run a program on the hub machine: bin/notes\n" +
		"The new version asks for more permissions than the current one.\nNote: unsigned package\n"
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}

	var out bytes.Buffer
	if confirm(i18n.Ja, strings.NewReader("n\n"), &out) || out.String() != "続行しますか？ [y/N] " {
		t.Fatalf("confirm: %q", out.String())
	}
	out.Reset()
	if !confirm(i18n.En, strings.NewReader("YES\n"), &out) || out.String() != "Continue? [y/N] " {
		t.Fatalf("confirm: %q", out.String())
	}
}
