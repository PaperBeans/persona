package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestHealthFirstDiscoveryBeforeSessionPublication(t *testing.T) {
	testHealthFirstDiscovery(t, true)
}

func TestHealthDiscoveryAfterSessionPublicationWithoutDirectory(t *testing.T) {
	testHealthFirstDiscovery(t, false)
}

// Start is still awaiting assistant.send when the node first asks for tools.
// The fake node returns the prepare directory and later publishes only Cwd,
// matching assistantSessions rather than inventing a Space or an early row.
func testHealthFirstDiscovery(t *testing.T, directoryEvidence bool) {
	t.Helper()
	needsHealthModule(t)
	for _, space := range []string{SpaceHealth, "", "qa-plain"} {
		t.Run("space="+space, func(t *testing.T) {
			ctx := context.Background()
			s, hub, _ := spaceMDService(t)
			devices := s.devs.(spaceDevs).fleetDevs
			const sid = "00000000-0000-4000-8000-000000000124"
			var cwd, revision string
			queried := false
			query := map[string]any{"session_id": sid, "assistant": true}
			assertTools := func(tools []proto.Tool, want bool) {
				t.Helper()
				for _, name := range healthToolNames {
					if toolListed(tools, name) != want {
						t.Fatalf("space=%q directory=%v tool=%s listed=%v want=%v", space, directoryEvidence, name, toolListed(tools, name), want)
					}
				}
			}
			s.hub = &firstToolsTimingHub{Hub: hub, beforeCall: func(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
				if p.Method == DeviceAssistantPrepare {
					var in struct {
						Dir string `json:"dir"`
					}
					if err := json.Unmarshal(p.Params, &in); err != nil {
						return nil, err
					}
					return json.Marshal(map[string]any{"cwd": hub.devices[0].Home + "/" + in.Dir})
				}
				if p.Method != DeviceAssistantSend {
					return hub.DeviceCall(ctx, p)
				}
				var in struct {
					Cwd string `json:"cwd"`
				}
				if err := json.Unmarshal(p.Params, &in); err != nil {
					return nil, err
				}
				cwd, queried = in.Cwd, true
				if _, ok := s.session(ctx, "server", sid); ok {
					t.Fatal("session published before first discovery")
				}
				if got := s.agentCallerSpace(ctx, "server", sid); got != "" {
					t.Fatalf("caller space without evidence: %q", got)
				}
				callerCtx := ctx
				if directoryEvidence {
					query["caller_cwd"] = cwd
					callerCtx = withNodeCallerCwd(ctx, "server", sid, cwd)
				}
				first := privacyRequest(t, s, "server", "mcp.tools", query).(map[string]any)
				tools := first["tools"].([]proto.Tool)
				if !toolListed(tools, "ask_user") {
					t.Fatal("startup admission refused ordinary assistant tools")
				}
				want := directoryEvidence && space == SpaceHealth
				assertTools(tools, want)
				assertTools(s.ToolsFor(callerCtx, "server", sid), want)
				assertTools(s.ToolsFor(ctx, "server", sid), false)
				assertTools(s.Tools(ctx), false)
				revision = first["revision"].(string)
				for _, name := range healthToolNames {
					call := map[string]any{"session_id": sid, "assistant": true, "name": name, "args": map[string]any{}}
					if directoryEvidence {
						call["caller_cwd"] = cwd
					}
					result := privacyRequest(t, s, "server", "mcp.call", call).(proto.ToolResult)
					if !result.IsError || len(result.Content) == 0 {
						t.Fatalf("invalid arguments accepted by %s: %+v", name, result)
					}
					denied := strings.Contains(result.Content[0].Text, "known Health conversation")
					if denied == want {
						t.Fatalf("%s Health caller boundary: %+v", name, result)
					}
					if want && strings.Contains(result.Content[0].Text, "unknown tool") {
						t.Fatalf("listed %s did not reach its handler: %+v", name, result)
					}
				}
				t.Logf("before session publication: space=%q directory=%v health_tools=%v", space, directoryEvidence, want)
				return json.Marshal(map[string]any{"session_id": sid})
			}}
			c, err := s.Start(ctx, StartParams{Text: "Synthetic training request", Space: space})
			if err != nil {
				t.Fatal(err)
			}
			if !queried || c.SessionID != sid || cwd == "" || revision == "" {
				t.Fatalf("Start not exercised: %+v cwd=%q", c, cwd)
			}
			devices.setSessions("server", []proto.Session{{SessionID: sid, Cwd: cwd, Assistant: true}})
			assertTools(s.ToolsFor(ctx, "server", sid), space == SpaceHealth)
			// A legacy node's next heartbeat uses the old cursor. A newly known Health
			// space must invalidate it; equivalent attested discovery stays unchanged.
			query["revision"] = revision
			watch, cancel := context.WithCancel(ctx)
			cancel()
			raw, _ := json.Marshal(query)
			out, handled, err := s.NodeRequest(watch, "server", "mcp.tools", raw)
			if err != nil || !handled {
				t.Fatalf("watch: %v handled=%v", err, handled)
			}
			next := out.(map[string]any)
			changed := !directoryEvidence && space != ""
			if changed {
				if next["unchanged"] == true || next["revision"] == revision {
					t.Fatalf("published space reused an unknown-space cursor: %+v", next)
				}
				assertTools(next["tools"].([]proto.Tool), space == SpaceHealth)
			} else if next["unchanged"] != true || next["revision"] != revision {
				t.Fatalf("equivalent list lost its cursor: %+v", next)
			}
			current := privacyRequest(t, s, "server", "mcp.tools", map[string]any{"session_id": sid, "assistant": true}).(map[string]any)
			assertTools(current["tools"].([]proto.Tool), space == SpaceHealth)
			t.Logf("after session publication: space=%q health_tools=%v cursor_changed=%v", space, space == SpaceHealth, changed)
		})
	}
}
