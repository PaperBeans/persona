package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A fresh Assistant start with an explicit named model passes --model even when the local Claude profile settings
// default to another model (such as Fable), including when the first message carries an image (#13567).
func TestAssistantFreshStartPinsNamedModelOverLocalSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
	}{
		{"named tier", "opus"},
		{"concrete model id", "claude-opus-5-5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeClaudeHome(t, `case " $* " in *" auth status "*) echo '{"loggedIn":true}'; exit 0 ;; esac
read line
printf '%s\n' '{"type":"system","subtype":"init"}'
sleep 30
`)
			for _, k := range append([]string{"CLAUDE_CONFIG_DIR"}, claudeModelVars...) {
				t.Setenv(k, "")
			}
			managed, managedDirs := claudeManagedSettings, claudeManagedSettingsDirs
			claudeManagedSettings, claudeManagedSettingsDirs = nil, nil
			t.Cleanup(func() { claudeManagedSettings, claudeManagedSettingsDirs = managed, managedDirs })

			n := codexTestNode(t)
			n.home = os.Getenv("HOME")
			n.dir = t.TempDir()
			withFakeHub(t, n)

			// Seed the local Claude settings profile with Fable so omitting --model would fall back to Fable.
			profile := n.assistantConfigDir("claude", "")
			if err := os.MkdirAll(profile, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profile, "settings.json"), []byte(`{"model":"fable"}`), 0o600); err != nil {
				t.Fatal(err)
			}

			cwd := t.TempDir()
			images := []proto.Image{{Mime: "image/png", Data: "iVBORw0KGgo="}}
			id, err := n.assistantSend(context.Background(), "", cwd, "claude", "describe this image", images, "", "",
				modelExecution{Model: tc.model, Fingerprint: "fp-pinned", IsMain: true})
			if err != nil {
				t.Fatalf("assistantSend(%q): %v", tc.model, err)
			}
			defer n.stopAssistant(id)

			n.mu.Lock()
			p := n.procs[id]
			n.mu.Unlock()
			if p == nil {
				t.Fatal("expected running claude process")
			}
			i := indexOf(p.cmd.Args, "--model")
			if i < 0 || i+1 >= len(p.cmd.Args) || p.cmd.Args[i+1] != tc.model {
				t.Fatalf("spawned args missing --model %q: %q", tc.model, p.cmd.Args)
			}
			st, ok := n.state.get(id)
			if !ok || st.Model != tc.model {
				t.Fatalf("persisted state model = %q (ok=%v), want %q", st.Model, ok, tc.model)
			}
		})
	}
}

// An explicit tier that a gateway provider cannot translate must stop delivery with a coded, actionable refusal before
// spawning Claude, instead of silently dropping --model and falling back to a default (#13567). Explicit follow-default
// choices ("", "default", "inherit"), requests matching a managed launcher's own default Model tier, and mapped gateway
// tiers continue to work cleanly.
func TestAssistantGatewayModelResolutionFailsClosedOnUnresolvableTier(t *testing.T) {
	const gwURL = "https://gw.example.com"
	unmapped := proto.ManagedLauncher{Name: "gw-unmapped", Kind: "api", BaseURL: gwURL, APIKey: "sk-test-k"}
	tierDefault := proto.ManagedLauncher{Name: "gw-tier-default", Kind: "api", BaseURL: gwURL, APIKey: "sk-test-k", Model: "opus"}
	mapped := proto.ManagedLauncher{Name: "gw-mapped", Kind: "api", BaseURL: gwURL, APIKey: "sk-test-k", Model: "route/claude-opus-5-5"}

	setupNode := func(t *testing.T, launchers ...proto.ManagedLauncher) (*Node, string) {
		t.Helper()
		marker := filepath.Join(t.TempDir(), "spawned")
		fakeClaudeHome(t, "touch '"+marker+"'\nread line\nprintf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
		n := codexTestNode(t)
		n.home = os.Getenv("HOME")
		n.dir = t.TempDir()
		withFakeHub(t, n)
		n.setManaged(launchers)
		return n, marker
	}

	for _, tier := range []string{"opus", "fable", "opus[1m]", "sonnet"} {
		t.Run("unresolvable_"+tier, func(t *testing.T) {
			n, marker := setupNode(t, unmapped)
			ctx := i18n.With(context.Background(), i18n.En)
			_, err := n.assistantSend(ctx, "", t.TempDir(), unmapped.Name, "hello", nil, "", "",
				modelExecution{Model: tier, Fingerprint: "fp-unmapped"})
			if err == nil {
				t.Fatalf("expected refusal for unresolvable tier %q", tier)
			}
			var re *rpc.Error
			if !errors.As(err, &re) || re.Code != "assistant_model_unresolved" {
				t.Fatalf("error = %v, want rpc.Error assistant_model_unresolved", err)
			}
			wantMsg := i18n.T(i18n.En, "rpc-errors.assistant_model_unresolved", i18n.Args{"model": tier, "launcher": unmapped.Name})
			if re.Message != wantMsg {
				t.Fatalf("message = %q, want %q", re.Message, wantMsg)
			}
			var data map[string]any
			if err := json.Unmarshal(re.Data, &data); err != nil {
				t.Fatalf("unmarshal data %q: %v", re.Data, err)
			}
			if data["delivered"] != "none" || data["retryable"] != false || data["model"] != tier || data["launcher"] != unmapped.Name {
				t.Fatalf("unexpected refusal data: %v", data)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatal("claude process was spawned despite unresolvable tier")
			}
			n.mu.Lock()
			procCount := len(n.procs)
			n.mu.Unlock()
			if procCount != 0 {
				t.Fatalf("expected 0 tracked procs, got %d", procCount)
			}
			// Direct spawnClaude callers fail closed the same way.
			if _, spawnErr := n.spawnClaude("11111111-2222-3333-4444-555555555555", t.TempDir(), unmapped.Name, false, false,
				modelExecution{Model: tier, Fingerprint: "fp-unmapped"}); !errors.As(spawnErr, &re) || re.Code != "assistant_model_unresolved" {
				t.Fatalf("spawnClaude(%q) = %v, want assistant_model_unresolved", tier, spawnErr)
			}
		})
	}

	t.Run("follow launcher default on gateway succeeds without --model", func(t *testing.T) {
		n, _ := setupNode(t, unmapped)
		for _, def := range []string{"", "default", "inherit"} {
			got, err := n.resolveClaudeModelArg(context.Background(), unmapped.Name, def)
			if err != nil || got != "" {
				t.Fatalf("resolveClaudeModelArg(%q) = (%q, %v), want (\"\", nil)", def, got, err)
			}
		}
		p, err := n.spawnClaude("11111111-2222-3333-4444-555555555556", t.TempDir(), unmapped.Name, false, false,
			modelExecution{Model: "default", Fingerprint: "fp-default"})
		if err != nil {
			t.Fatalf("spawnClaude(default): %v", err)
		}
		defer n.stopAssistant(p.id)
		if indexOf(p.cmd.Args, "--model") >= 0 {
			t.Fatalf("expected no --model for gateway default, got %q", p.cmd.Args)
		}
	})

	t.Run("managed gateway tier default accepts matching tier and refuses other unmapped tiers", func(t *testing.T) {
		n, _ := setupNode(t, tierDefault)
		for _, match := range []string{"opus", "Opus", "opus[1m]"} {
			got, err := n.resolveClaudeModelArg(context.Background(), tierDefault.Name, match)
			if err != nil || got != "" {
				t.Fatalf("resolveClaudeModelArg(%q) = (%q, %v), want (\"\", nil)", match, got, err)
			}
		}
		p, err := n.spawnClaude("11111111-2222-3333-4444-555555555558", t.TempDir(), tierDefault.Name, false, false,
			modelExecution{Model: "opus", Fingerprint: "fp-tier-default"})
		if err != nil {
			t.Fatalf("spawnClaude(tier default opus): %v", err)
		}
		defer n.stopAssistant(p.id)
		if indexOf(p.cmd.Args, "--model") >= 0 {
			t.Fatalf("expected no --model when matching launcher tier default, got %q", p.cmd.Args)
		}

		for _, other := range []string{"haiku", "sonnet", "fable"} {
			t.Run("refuses_"+other, func(t *testing.T) {
				n2, marker := setupNode(t, tierDefault)
				ctx := i18n.With(context.Background(), i18n.En)
				_, err := n2.assistantSend(ctx, "", t.TempDir(), tierDefault.Name, "hello", nil, "", "",
					modelExecution{Model: other, Fingerprint: "fp-other-tier"})
				var re *rpc.Error
				if !errors.As(err, &re) || re.Code != "assistant_model_unresolved" {
					t.Fatalf("assistantSend(%q) = %v, want assistant_model_unresolved", other, err)
				}
				if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
					t.Fatalf("claude process was spawned despite unmapped tier %q", other)
				}
			})
		}
	})

	t.Run("mapped gateway tier translates to concrete --model", func(t *testing.T) {
		n, _ := setupNode(t, mapped)
		p, err := n.spawnClaude("11111111-2222-3333-4444-555555555557", t.TempDir(), mapped.Name, false, false,
			modelExecution{Model: "opus", Fingerprint: "fp-mapped"})
		if err != nil {
			t.Fatalf("spawnClaude(mapped opus): %v", err)
		}
		defer n.stopAssistant(p.id)
		i := indexOf(p.cmd.Args, "--model")
		if i < 0 || i+1 >= len(p.cmd.Args) || p.cmd.Args[i+1] != "route/claude-opus-5-5" {
			t.Fatalf("args = %q, want --model route/claude-opus-5-5", p.cmd.Args)
		}
	})
}
