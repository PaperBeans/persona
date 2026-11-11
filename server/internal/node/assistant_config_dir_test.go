package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// Exercise the RPC boundary and both real fixture processes, not just a profile
// resolver. Old records deliberately contain no config_dir.
func TestAssistantProfileCompatibility(t *testing.T) {
	for _, runtime := range []string{"", proto.AssistantRuntimeAgentSDK} {
		for _, scenario := range []string{"new-default", "new-explicit", "new-legacy", "new-machine-default", "new-shell-default", "old-service", "old-home", "old-home-with-service", "old-machine", "old-shell-default", "old-discovered", "old-missing", "old-recorded"} {
			t.Run(runtime+"/"+scenario, func(t *testing.T) {
				f := sdkTest(t)
				n := f.n
				n.home = os.Getenv("HOME")
				n.dir = filepath.Join(n.home, ".persona")
				n.cdirs = newClaudeDirs()
				n.cdirs.scan = func() map[int]procEnv { return nil }
				service, other := t.TempDir(), t.TempDir()
				t.Setenv("CLAUDE_CONFIG_DIR", service)
				cwd := filepath.Join(n.dir, "assistant")
				params := map[string]any{"launcher": "claude", "cwd": cwd, "text": "hello", "runtime": runtime, "model_fingerprint": "compat-test"}
				want := filepath.Join(n.dir, "profiles", "claude")
				profile, id := want, ""
				switch scenario {
				case "new-explicit":
					want, profile = other, other
					params["config_dir"] = other
				case "new-legacy":
					want, profile = service, service
					params["config_dir"] = "legacy"
				case "new-machine-default":
					n.claudeStartDir.Store(&other)
				case "new-shell-default":
					writeFile(t, filepath.Join(n.home, ".zshrc"), "export CLAUDE_CONFIG_DIR="+shellQuote(other)+"\n")
				case "old-service", "old-missing":
					want, profile = service, service
				case "old-home":
					t.Setenv("CLAUDE_CONFIG_DIR", "")
					want, profile = "", filepath.Join(n.home, ".claude")
				case "old-home-with-service":
					want, profile = "", filepath.Join(n.home, ".claude")
				case "old-machine":
					n.claudeStartDir.Store(&other)
					want, profile = other, other
				case "old-shell-default":
					writeFile(t, filepath.Join(n.home, ".zshrc"), "export CLAUDE_CONFIG_DIR="+shellQuote(other)+"\n")
					want, profile = service, service
					if runtime == "" {
						want, profile = other, other
					}
				case "old-discovered", "old-recorded":
					want, profile = other, other
				}
				if strings.HasPrefix(scenario, "old-") {
					id = "a5500000-0000-4000-8000-000000000001"
					params["session_id"] = id
					raw, _ := json.Marshal(map[string]any{"launcher": "claude", "cwd": cwd, "config_dir": func() string {
						if scenario == "old-recorded" {
							return other
						}
						return ""
					}()})
					var st sessionState
					if err := json.Unmarshal(raw, &st); err != nil {
						t.Fatal(err)
					}
					n.state.put(id, st)
					if scenario != "old-missing" && scenario != "old-recorded" {
						writeFile(t, filepath.Join(profile, "projects", "-assistant", id+".jsonl"),
							`{"type":"user","cwd":`+jsonString(t, cwd)+`,"message":{"role":"user","content":"earlier"}}`+"\n")
					}
					n.cdirs.known = []string{other}
				}
				answer, err := nodeCall(t, n, "assistant.send", params)
				if err != nil {
					t.Fatal(err)
				}
				var res struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(answer, &res); err != nil {
					t.Fatal(err)
				}
				check := func() {
					t.Helper()
					p := f.proc(res.SessionID)
					if got := envValue(p.cmd.Environ(), "CLAUDE_CONFIG_DIR"); got != want {
						t.Fatalf("%s profile=%q, want %q", scenario, got, want)
					}
					if runtime == proto.AssistantRuntimeAgentSDK {
						settings, _ := f.start(t)["settings"].(map[string]any)
						env, _ := settings["env"].(map[string]any)
						if got, _ := env["CLAUDE_CONFIG_DIR"].(string); got != want {
							t.Fatalf("SDK child profile=%q, want %q", got, want)
						}
					} else if got := strings.TrimSuffix(f.file("cli-profile"), "\n"); got != want {
						t.Fatalf("CLI child profile=%q, want %q", got, want)
					}
				}
				check()
				if st, ok := n.state.get(res.SessionID); !ok || st.ConfigDir == "" {
					t.Fatalf("profile selection was not recorded: %+v", st)
				}
				f.idle(t, res.SessionID)
				n.stopAssistant(res.SessionID)
				// Changing a service default cannot move a recorded conversation.
				t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
				if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": res.SessionID, "text": "again"}); err != nil {
					t.Fatal(err)
				}
				check()
				n.stopAssistant(res.SessionID)
				t.Logf("%s: CLI/SDK profile retained after process restart", scenario)
			})
		}
	}
}

func TestAssistantProfileSelectionBoundaries(t *testing.T) {
	t.Run("scope cannot escape", func(t *testing.T) {
		n := scopedTestNode(t)
		if _, err := n.assistantProfileSelection(context.Background(), "", "claude", "", t.TempDir(), false); err == nil {
			t.Fatal("an external profile escaped the node's session scope")
		}
		if got, err := n.assistantProfileSelection(context.Background(), "", "claude", "", "", false); err != nil || got != n.sessionScope.claude {
			t.Fatalf("scoped default=%q: %v", got, err)
		}
	})
	t.Run("wrapper keeps its own directory", func(t *testing.T) {
		f := sdkTest(t)
		profile := t.TempDir()
		writeFile(t, filepath.Join(os.Getenv("HOME"), ".zshrc"), `function work { CLAUDE_CONFIG_DIR=`+shellQuote(profile)+` command claude "$@"; }`+"\n")
		if got, err := f.n.assistantProfileSelection(context.Background(), "", "work", "", "", false); err != nil || got != profile {
			t.Fatalf("wrapper default=%q: %v", got, err)
		}
		if _, err := f.n.assistantProfileSelection(context.Background(), "", "work", "", t.TempDir(), false); err == nil {
			t.Fatal("a conflicting wrapper was accepted")
		}
	})
	t.Run("warm spare cannot select another profile", func(t *testing.T) {
		f := sdkTest(t)
		cwd, first, second := t.TempDir(), t.TempDir(), t.TempDir()
		if err := f.n.assistantWarm(cwd, "claude", modelExecution{ConfigDir: first}); err != nil {
			t.Fatal(err)
		}
		f.n.mu.Lock()
		p := f.n.spare
		f.n.mu.Unlock()
		t.Cleanup(p.stop)
		if got := f.n.adoptSpare(cwd, "claude", modelExecution{ConfigDir: second}); got != nil {
			t.Fatal("a new conversation adopted another profile's warm process")
		}
		if got := f.n.adoptSpare(cwd, "claude", modelExecution{ConfigDir: first}); got != p {
			t.Fatal("a matching warm process was not reused")
		}
	})
}

func TestAssistantProfileCannotMoveExistingConversation(t *testing.T) {
	f := sdkTest(t)
	first, second := t.TempDir(), t.TempDir()
	answer, err := nodeCall(t, f.n, "assistant.send", map[string]any{"cwd": t.TempDir(), "text": "hello", "config_dir": first})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(answer, &res); err != nil {
		t.Fatal(err)
	}
	f.idle(t, res.SessionID)
	f.n.stopAssistant(res.SessionID)
	_, err = nodeCall(t, f.n, "assistant.send", map[string]any{"session_id": res.SessionID, "text": "again", "config_dir": second})
	if err == nil || !strings.Contains(err.Error(), "config_dir") {
		t.Fatalf("moving a conversation was not refused: %v", err)
	}
}

func TestAssistantProfileRollbackKeepsTheOriginalTranscript(t *testing.T) {
	f := sdkTest(t)
	n := f.n
	id := "a5500000-0000-4000-8000-000000000002"
	profile := filepath.Join(n.dir, "profiles", "claude")
	path := filepath.Join(profile, "projects", "-assistant", id+".jsonl")
	writeFile(t, path, `{"type":"user","message":{"role":"user","content":"keep this history"}}`+"\n")
	// The documented downgrade step selects the saved directory using the old
	// service/launcher environment. Old readers need not understand new metadata.
	t.Setenv("CLAUDE_CONFIG_DIR", profile)
	cmd := loginShell(context.Background(), `cat "$CLAUDE_CONFIG_DIR/projects/-assistant/`+id+`.jsonl"`)
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "keep this history") {
		t.Fatalf("old env-only lookup lost the transcript: %q, %v", out, err)
	}
	if got := n.transcriptPath(id); got != path {
		t.Fatalf("old directory discovery cannot open %q: %q", path, got)
	}
}
