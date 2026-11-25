package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

type launcherImportToolProvider struct {
	ID         string                       `json:"id"`
	Name       string                       `json:"name"`
	Aliases    []string                     `json:"aliases"`
	BaseURL    string                       `json:"base_url"`
	Models     []proto.ProviderModel        `json:"models"`
	HasKey     bool                         `json:"has_key"`
	HasHeaders bool                         `json:"has_headers"`
	HasEnv     bool                         `json:"has_env"`
	EnvNames   []string                     `json:"env_names"`
	Existing   string                       `json:"existing"`
	CreateAs   string                       `json:"create_as"`
	Differs    []string                     `json:"differs"`
	Variants   []launcherImportToolProvider `json:"variants"`
}

func launcherImportToolService(t *testing.T, aliases []zshrcAlias, discoverErr error, stores ...Store) *Service {
	t.Helper()
	var s *Service
	if len(stores) == 0 {
		s, _, _ = modelService(t)
	} else {
		f := newFakeHub().withMachine()
		f.ownModels = true
		s = newServiceOn(t, f, stores[0])
	}
	profileToolsDevices(s)
	old := zshrcAliases
	zshrcAliases = func(context.Context, *Service, string) ([]zshrcAlias, error) { return aliases, discoverErr }
	t.Cleanup(func() { zshrcAliases = old })
	return s
}

func launcherImportToolCall(t *testing.T, s *Service, args string) proto.ToolResult {
	t.Helper()
	return s.Tool(context.Background(), ToolCall{Name: "launcher_import", Arguments: json.RawMessage(args), MachineID: "server", SessionID: "main"})
}

func requireLauncherImportRedacted(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{importKeyA, importKeyB, "hdr-test-value", importEnvSecret, importProxySecret, "team blue", "64000"} {
		if strings.Contains(text, secret) {
			t.Fatalf("launcher_import exposed %q: %s", secret, text)
		}
	}
	for _, field := range []string{`"api_key":`, `"headers":`, `"env":`, `"key_sealed"`, `"headers_sealed"`, `"env_sealed"`} {
		if strings.Contains(text, field) {
			t.Fatalf("launcher_import exposed sensitive field %s: %s", field, text)
		}
	}
}

func TestLauncherImportToolPreviewRedactedAndSelectable(t *testing.T) {
	s := launcherImportToolService(t, importAliases, nil)
	result := launcherImportToolCall(t, s, `{"action":"preview","machine":"m1"}`)
	text := managedToolText(t, result)
	requireLauncherImportRedacted(t, text)
	var out struct {
		Machine   string                       `json:"machine"`
		Providers []launcherImportToolProvider `json:"providers"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Machine != "m1" || len(out.Providers) != 2 {
		t.Fatalf("preview: %+v", out)
	}
	g := out.Providers[0]
	if g.Name != "gcc" || g.BaseURL != "https://gw.example.com" || !slices.Equal(g.Aliases, []string{"gcc", "gcc2"}) ||
		!g.HasKey || !g.HasHeaders || !g.HasEnv || len(g.Models) != 2 || len(g.Variants) != 2 ||
		!slices.Contains(g.EnvNames, "GATEWAY_LABEL") || !slices.Equal(g.Differs, []string{"headers", "env"}) {
		t.Fatalf("group: %+v", g)
	}
	for _, name := range []string{"gcc", "gcc2"} {
		result = launcherImportToolCall(t, s, `{"action":"preview","machine":"m1","names":["`+name+`"]}`)
		text = managedToolText(t, result)
		requireLauncherImportRedacted(t, text)
		out.Providers = nil
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		if name == "gcc" {
			if len(out.Providers) != 1 || !slices.Equal(out.Providers[0].Aliases, []string{"gcc", "gcc2"}) || len(out.Providers[0].Variants) != 2 {
				t.Fatalf("provider-name selection: %+v", out.Providers)
			}
		} else if len(out.Providers) != 1 || out.Providers[0].Name != "gcc2" || !slices.Equal(out.Providers[0].Aliases, []string{"gcc2"}) || len(out.Providers[0].Variants) != 0 {
			t.Fatalf("alias selection: %+v", out.Providers)
		}
	}

	sealed, err := s.sealKey(importKeyB)
	if err != nil {
		t.Fatal(err)
	}
	putLaunchers(t, s,
		storedLauncher{KeySealed: sealed, ManagedLauncher: proto.ManagedLauncher{Name: "kimi", BaseURL: "https://k.example.com"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gcc", BaseURL: "https://elsewhere.example.com"}},
	)
	before := s.storedLaunchers(context.Background())
	result = launcherImportToolCall(t, s, `{"action":"preview","machine":"m1","names":["gcc","kcc"]}`)
	text = managedToolText(t, result)
	requireLauncherImportRedacted(t, text)
	out.Providers = nil
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Providers) != 2 || out.Providers[0].CreateAs != "gcc-2" || out.Providers[0].Existing != "" ||
		out.Providers[1].Name != "kcc" || out.Providers[1].Existing != "kimi" || out.Providers[1].CreateAs != "" {
		t.Fatalf("preview landing: %+v", out.Providers)
	}
	if len(s.pendingQuestions()) != 0 || !reflect.DeepEqual(s.storedLaunchers(context.Background()), before) {
		t.Fatal("valid preview created a card or changed stored providers")
	}
}

func TestLauncherImportToolStrictInputBeforeDiscovery(t *testing.T) {
	s := launcherImportToolService(t, importAliases, nil)
	var discoveries atomic.Int32
	old := zshrcAliases
	zshrcAliases = func(context.Context, *Service, string) ([]zshrcAlias, error) {
		discoveries.Add(1)
		return importAliases, nil
	}
	t.Cleanup(func() { zshrcAliases = old })
	for _, args := range []string{
		`{"action":"preview","machine":"m1","api_key":"strict-override-secret"}`,
		`{"action":"preview","machine":"m1","headers":{"X-Test":"strict-override-secret"}}`,
		`{"action":"preview","machine":"m1","env":{"TOKEN":"strict-override-secret"}}`,
		`{"action":"preview","machine":"m1","base_url":"https://override.invalid"}`,
		`{"action":"preview","machine":"m1","unknown":true}`,
		`{"action":"preview","machine":"m1"} {}`,
		`null`,
		`{"action":"commit","machine":"m1","names":[]}`,
		`{"action":"commit","machine":"m1"}`,
	} {
		result := launcherImportToolCall(t, s, args)
		if !result.IsError {
			t.Errorf("invalid input succeeded: %s => %+v", args, result)
		}
		if strings.Contains(toolText(result), "strict-override-secret") {
			t.Errorf("input error reflected an override value: %s", toolText(result))
		}
	}
	if discoveries.Load() != 0 {
		t.Fatalf("invalid input discovered aliases %d times", discoveries.Load())
	}
	if len(s.pendingQuestions()) != 0 || len(s.storedLaunchers(context.Background())) != 0 {
		t.Fatal("invalid input created a card or stored a provider")
	}
}

func TestLauncherImportToolErrorsAreRedacted(t *testing.T) {
	s := launcherImportToolService(t, nil, errors.New("node failed with "+importKeyA+" and "+importEnvSecret))
	for _, args := range []string{
		`{"action":"preview","machine":"m1"}`,
		`{"action":"commit","machine":"m1","names":["gcc"]}`,
	} {
		result := launcherImportToolCall(t, s, args)
		if !result.IsError || toolText(result) != errLauncherImportDiscovery.Error() {
			t.Fatalf("discovery error result: %+v", result)
		}
		requireLauncherImportRedacted(t, toolText(result))
	}
	if len(s.pendingQuestions()) != 0 || len(s.storedLaunchers(context.Background())) != 0 {
		t.Fatal("failed commit discovery created a card or stored a provider")
	}
}

func TestLauncherImportToolPanicsAreRedacted(t *testing.T) {
	s := launcherImportToolService(t, nil, nil)
	old := zshrcAliases
	zshrcAliases = func(context.Context, *Service, string) ([]zshrcAlias, error) {
		panic(errors.New("panic with " + importKeyA + " and " + importEnvSecret))
	}
	t.Cleanup(func() { zshrcAliases = old })
	result := launcherImportToolCall(t, s, `{"action":"preview","machine":"m1"}`)
	if !result.IsError || !strings.Contains(toolText(result), "without diagnostic details") {
		t.Fatalf("panic result: %+v", result)
	}
	requireLauncherImportRedacted(t, toolText(result))
}

func TestLauncherImportToolCommitFailuresAreSpecificRedactedAndUnlocked(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failRead  int32
		panicRead bool
		want      error
		committed bool
	}{
		{name: "apply", failRead: 2, want: errLauncherImportCommit},
		{name: "readback", failRead: 3, want: errLauncherImportReadback, committed: true},
		{name: "readback panic", failRead: 3, panicRead: true, want: errLauncherImportReadback, committed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newSettingsFaultStore()
			s := launcherImportToolService(t, importAliases, nil, st)
			var armed atomic.Bool
			var reads atomic.Int32
			st.onRead = func(st *settingsFaultStore, key string, _ int) {
				if !armed.Load() || key != launchersManagedKey || reads.Add(1) != tc.failRead {
					return
				}
				if tc.panicRead {
					panic(errors.New("readback panic with " + importKeyA + " and " + importEnvSecret))
				}
				st.failRead = key
			}
			result := runManagedProfileTool(t, s, "launcher_import", map[string]any{
				"action": "commit", "machine": "m1", "names": []string{"gcc2"},
			}, true, nil, func() { armed.Store(true) })
			text := toolText(result)
			if !result.IsError || text != tc.want.Error() {
				t.Fatalf("commit failure result: %+v, want %q", result, tc.want)
			}
			requireLauncherImportRedacted(t, text)
			st.onRead = nil
			st.failRead = ""
			list := s.storedLaunchers(context.Background())
			committed := len(list) == 1 && list[0].Name == "gcc2" && list[0].KeySealed != ""
			if committed != tc.committed {
				t.Fatalf("committed=%v, want %v", committed, tc.committed)
			}
			unlocked := make(chan struct{})
			go func() {
				s.models.mu.Lock()
				s.models.mu.Unlock()
				close(unlocked)
			}()
			select {
			case <-unlocked:
			case <-time.After(5 * time.Second):
				t.Fatal("models mutex remained locked after the commit failure")
			}
		})
	}
}

func TestLauncherImportToolCommitSealsMergesAndReadsBack(t *testing.T) {
	s := launcherImportToolService(t, importAliases, nil)
	ctx := context.Background()
	sealed, err := s.sealKey(importKeyB)
	if err != nil {
		t.Fatal(err)
	}
	putLaunchers(t, s,
		storedLauncher{KeySealed: sealed, ManagedLauncher: proto.ManagedLauncher{Name: "kimi", BaseURL: "https://k.example.com", Model: "kept", Models: []proto.ProviderModel{{ID: "kept"}}}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gcc", BaseURL: "https://elsewhere.example.com"}},
	)
	result := runManagedProfileTool(t, s, "launcher_import", map[string]any{
		"action": "commit", "machine": "m1", "names": []string{"gcc", "kcc"},
	}, true, func(card proto.Interaction) {
		if card.ToolName != "launcher_import" {
			t.Errorf("card tool = %q", card.ToolName)
		}
		for _, want := range []string{"gcc-2", "kimi"} {
			if !strings.Contains(card.Summary, want) {
				t.Errorf("card lacks %q: %s", want, card.Summary)
			}
		}
		requireLauncherImportRedacted(t, card.Summary)
	}, nil)
	text := managedToolText(t, result)
	requireLauncherImportRedacted(t, text)
	var out struct {
		Created   []string                     `json:"created"`
		Updated   []string                     `json:"updated"`
		Hidden    []string                     `json:"hidden"`
		Providers []launcherImportToolProvider `json:"providers"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Created, []string{"gcc-2"}) || !slices.Equal(out.Updated, []string{"kimi"}) || len(out.Hidden) != 0 || len(out.Providers) != 2 {
		t.Fatalf("readback: %+v", out)
	}
	created := out.Providers[0]
	if created.ID != "launcher.gcc-2" || created.Name != "gcc-2" || created.BaseURL != "https://gw.example.com" ||
		!created.HasKey || !created.HasHeaders || !created.HasEnv || len(created.Models) != 2 {
		t.Fatalf("created readback: %+v", created)
	}
	list := s.storedLaunchers(ctx)
	if len(list) != 3 || list[2].Name != "gcc-2" || list[2].KeySealed == "" || list[2].HeadersSealed == "" || list[2].EnvSealed == "" ||
		list[2].APIKey != "" || list[2].Headers != "" || list[2].Env != "" {
		t.Fatalf("stored providers: %+v", list)
	}
	if !reflect.DeepEqual(list[0].Models, []proto.ProviderModel{{ID: "kept"}, {ID: "kimi-k3", ContextWindow: 1000000}}) {
		t.Fatalf("existing merge: %+v", list[0].Models)
	}
	if key, err := s.openKey(list[2].KeySealed); err != nil || key != importKeyA {
		t.Fatalf("created key was not sealed correctly: %v", err)
	}
	if len(s.pendingQuestions()) != 0 {
		t.Fatal("commit left another confirmation card")
	}
}

func TestLauncherImportToolCommitSplit(t *testing.T) {
	s := launcherImportToolService(t, importAliases, nil)
	result := runManagedProfileTool(t, s, "launcher_import", map[string]any{
		"action": "commit", "machine": "m1", "names": []string{"gcc"}, "split": true,
	}, true, nil, nil)
	text := managedToolText(t, result)
	requireLauncherImportRedacted(t, text)
	var out struct {
		Created   []string                     `json:"created"`
		Providers []launcherImportToolProvider `json:"providers"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Created, []string{"gcc", "gcc2"}) || len(out.Providers) != 2 || out.Providers[0].Models[0].ID != "glm-5" || out.Providers[1].Models[0].ID != "glm-air" {
		t.Fatalf("split readback: %+v", out)
	}
}

func TestLauncherImportToolCommitHideOriginals(t *testing.T) {
	s, _ := hideTestHub(t, []zshrcAlias{hideAlias}, map[string][]proto.Launcher{
		"m1": {{Name: "acc", BaseURL: "https://a.example.com", Model: "a1"}},
	})
	result := runManagedProfileTool(t, s, "launcher_import", map[string]any{
		"action": "commit", "machine": "m1", "names": []string{"acc"}, "hide_originals": true,
	}, true, nil, nil)
	text := managedToolText(t, result)
	requireLauncherImportRedacted(t, text)
	var out struct {
		Created []string `json:"created"`
		Hidden  []string `json:"hidden"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Created, []string{"acc-2"}) || !slices.Equal(out.Hidden, []string{"acc"}) ||
		!slices.Equal(s.hiddenLaunchers(context.Background()), []string{"acc"}) {
		t.Fatalf("hide_originals readback=%+v hidden=%v", out, s.hiddenLaunchers(context.Background()))
	}
}

func TestLauncherImportToolDeniedOrStaleCommitWritesNothing(t *testing.T) {
	for _, when := range []string{"denied", "stale"} {
		t.Run(when, func(t *testing.T) {
			s := launcherImportToolService(t, importAliases, nil)
			allow := when != "denied"
			result := runManagedProfileTool(t, s, "launcher_import", map[string]any{
				"action": "commit", "machine": "m1", "names": []string{"gcc"},
			}, allow, nil, func() {
				if when == "stale" {
					putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gcc", BaseURL: "https://elsewhere.example.com"}})
				}
			})
			text := toolText(result)
			requireLauncherImportRedacted(t, text)
			if when == "denied" && !strings.Contains(text, "declined") {
				t.Fatalf("denial result: %s", text)
			}
			if when == "stale" && !strings.Contains(text, "changed after this card") {
				t.Fatalf("stale result: %+v", result)
			}
			list := s.storedLaunchers(context.Background())
			if when == "denied" && len(list) != 0 {
				t.Fatalf("denied commit stored providers: %+v", list)
			}
			if when == "stale" && (len(list) != 1 || list[0].BaseURL != "https://elsewhere.example.com" || list[0].KeySealed != "") {
				t.Fatalf("stale commit applied an old proposal: %+v", list)
			}
		})
	}
}
