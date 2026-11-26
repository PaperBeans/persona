package node

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestMCPCallerCwdFromProcessRecord(t *testing.T) {
	n, _ := testNode(t)
	n.procs["health"] = &assistantProc{id: "health", cwd: "/home/u/.persona/health", cmd: &exec.Cmd{Process: &os.Process{Pid: 100}}}
	n.procs["nested"] = &assistantProc{id: "nested", cwd: "/home/u/project", cmd: &exec.Cmd{Process: &os.Process{Pid: 200}}}
	n.procs["unstarted"] = &assistantProc{id: "unstarted", cwd: "/home/u/.persona/health"}
	exited := make(chan struct{})
	close(exited)
	n.procs["exited"] = &assistantProc{id: "exited", cwd: "/home/u/.persona/health", cmd: &exec.Cmd{Process: &os.Process{Pid: 400}}, exited: exited}
	n.akeys = map[string]*assistantProc{}
	n.akeys["proc-key"] = &assistantProc{cx: &codexConn{key: "proc-key"}, cwd: "/home/u/.persona/health", cmd: &exec.Cmd{Process: &os.Process{Pid: 500}}}
	n.spare = &assistantProc{id: "spare", cwd: "/home/u/.persona/assistant", cmd: &exec.Cmd{Process: &os.Process{Pid: 600}}}
	for _, tc := range []struct {
		name, session, want string
		pids                []int
	}{
		{"own process", "health", "/home/u/.persona/health", []int{100}},
		{"descendant", "health", "/home/u/.persona/health", []int{300, 100}},
		{"nearest process", "nested", "/home/u/project", []int{300, 200, 100}},
		{"cannot claim parent", "health", "", []int{300, 200, 100}},
		{"unknown process", "health", "", []int{999}},
		{"unstarted", "unstarted", "", []int{999}},
		{"missing session", "", "", []int{100}},
		{"exited process", "exited", "", []int{400}},
		{"startup key", "proc-key", "/home/u/.persona/health", []int{501, 500}},
		{"warm spare", "spare", "/home/u/.persona/assistant", []int{601, 600}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := n.mcpCallerCwdIn(tc.pids, tc.session); got != tc.want {
				t.Fatalf("cwd = %q, want %q", got, tc.want)
			}
		})
	}
}

// Use the real socket bridge: input, arguments and top-level fields must never
// supply caller evidence, even when a request names a known conversation.
func TestMCPCallerCwdRelayIgnoresRequestClaims(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no peer process lookup")
	}
	n, hub := runtimeNode(t)
	ask := mcpRelay(t, n, hub)
	const cwd = "/home/u/.persona/health"
	for _, owned := range []bool{false, true} {
		if owned {
			n.mu.Lock()
			n.procs["health"] = &assistantProc{id: "health", cwd: cwd, cmd: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}}
			n.mu.Unlock()
		}
		for _, req := range []map[string]any{mcpTools(true, "health"), mcpCall(true, "health")} {
			req["caller_cwd"], req["cwd"] = "/forged", "/forged"
			req["input"] = map[string]any{"caller_cwd": "/forged", "session": "health", "revision": "cursor"}
			req["args"] = map[string]any{"caller_cwd": "/forged"}
			got := ask(req)
			if owned {
				if got["caller_cwd"] != cwd {
					t.Fatalf("owned process directory missing: %v", got)
				}
			} else if _, ok := got["caller_cwd"]; ok {
				t.Fatalf("unverified process attested a directory: %v", got)
			}
			if req["kind"] == "mcp.tools" && got["revision"] != "cursor" {
				t.Fatalf("directory attestation broke the watch cursor: %v", got)
			}
		}
	}
}
