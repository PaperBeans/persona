package node

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Work agents (docs/integrations/launchers.md "Work agents"): pi, the Cursor CLI (cursor-agent) and the Antigravity
// CLI (agy). They are Agents the user works with, started in a herdr pane the way Claude and Codex are, with a model
// the start names (`--model`) and a first message. They are not Claude Code: no permission mode, no effort, no
// ultracode, and the first message is never typed into the pane. pi's goes through its Persona extension
// (session.send); cursor-agent's and agy's go through herdr's agent.prompt, which only reaches an agent herdr detects.

const (
	cursorAgent = "cursor"
	agyAgent    = "agy"
)

// workAgent is one such agent: its herdr name, and the program a start types when the launcher is the agent itself.
func workAgent(kind string) (herdrName, program string, ok bool) {
	switch kind {
	case piAgent:
		return piAgent, "pi", true
	case cursorAgent:
		return cursorAgent, "cursor-agent", true
	case agyAgent:
		return agyAgent, "agy", true
	}
	return "", "", false
}

// workAgentKind is the work agent a launcher starts: the agent the hub named, else the launcher's own name when that
// name is the agent (pi, cursor, agy). "" for a launcher that starts Claude, Codex or another agent.
func workAgentKind(launcher, agentType string) string {
	if _, _, ok := workAgent(agentType); ok {
		return agentType
	}
	if agentType == "" {
		if _, _, ok := workAgent(launcher); ok {
			return launcher
		}
	}
	return ""
}

// workAgentCommand is what a start types into the pane: the launcher (the program, for the agent's own name) with the
// model when one was picked. The first message is not an argument: it is delivered once the agent is up.
func workAgentCommand(launcher, program, model string) string {
	cmd := launcher
	if launcher == "" || launcher == program || workAgentKind(launcher, "") == launcher {
		cmd = program
	}
	if model != "" {
		cmd += " --model " + shellQuote(model)
	}
	return cmd
}

// workAgentResume is how herdr resumes one Persona started: the same command with the session it was, when the agent
// has a session id Persona can hand back. pi's id is its session file's (herdr's pi integration); cursor-agent and agy
// name none Persona can resume, so a restored pane starts them again without one.
func workAgentResume(kind, cmd, id string) string {
	if kind == piAgent && id != "" {
		return cmd + " --session " + dquote(id)
	}
	return cmd
}

// startWorkAgent opens a tab in ws and runs the work agent there, then delivers the first message: to pi through the
// extension that lists the session (finishPiStart), to cursor-agent and agy through herdr's agent.prompt once herdr
// reports the agent. A pi whose extension never lists the session is closed: typing the message into pi is refused.
func (n *Node) startWorkAgent(ctx context.Context, launch host.Launcher, cwd, launcher, kind, model, prompt, ws, tabName string) (*proto.Session, error) {
	herdrName, program, ok := workAgent(kind)
	if !ok {
		return nil, fmt.Errorf("unsupported agent type %q", kind)
	}
	created, err := n.newTab(ctx, launch, cwd, ws, tabName, "")
	if err != nil {
		return nil, err
	}
	pane := created.Pane.String()
	if err := n.settleShell(ctx, pane); err != nil { // the fresh shell at rest first (shellsettle.go, #2570)
		return nil, n.abandonStart(ctx, launch, created, ws, err)
	}
	shells := paneShells(ctx, created)
	cmd := withHerdrAgent(typedHerdrAgent(launch, herdrName, shells...), workAgentCommand(launcher, program, model))
	// The command goes through the start file, so a line break in it cannot meet a shell that runs each line on its
	// own (typedstart.go).
	done, err := n.runAgentCommand(ctx, launch, created, ws, "", n.agentCommand(cmd, shellQuote, shells...), shells...)
	if err != nil {
		return nil, err
	}
	defer done()
	if kind == piAgent {
		return n.finishPiStart(ctx, launch, created, cwd, launcher, model, prompt)
	}
	id, agent, err := n.waitWorkAgent(ctx, pane, herdrName)
	if err != nil {
		return nil, n.abandonStart(ctx, launch, created, ws, err)
	}
	n.noteWorkAgent(ctx, launch, pane, id, cwd, launcher, kind, model)
	if prompt != "" {
		if err := n.hostPrompt(ctx, pane, prompt); err != nil {
			return nil, fmt.Errorf("send first message: %w", err)
		}
	}
	session := n.sessionFor(id, agent)
	session.Agent = herdrName
	return session, nil
}

// finishPiStart waits for the extension in the pi just started to list its session, then sends the first message
// through it. The tab is closed when the extension never lists it: the message is not typed into pi.
func (n *Node) finishPiStart(ctx context.Context, launch host.Launcher, created *host.Created, cwd, launcher, model, prompt string) (*proto.Session, error) {
	pane := created.Pane.String()
	deadline := time.Now().Add(piResumeWait)
	for {
		if id := n.runtimes.sessionInPane(pane, n.ownPane); id != "" {
			if err := n.refreshHosts(ctx); err != nil {
				return nil, err
			}
			n.noteWorkAgent(ctx, launch, pane, id, cwd, launcher, piAgent, model)
			if prompt != "" {
				if err := n.sendToPi(ctx, id, prompt); err != nil {
					return nil, fmt.Errorf("send first message: %w", err)
				}
			}
			a, _ := n.agent(id)
			if a.Agent == "" {
				a = host.Agent{Agent: piAgent, PaneID: pane}
			}
			session := n.sessionFor(id, a)
			session.Agent = piAgent
			return session, nil
		}
		if time.Now().After(deadline) {
			screen, _ := n.readPane(ctx, pane, "recent-unwrapped", 30)
			err := refusal("pi_no_extension", fmt.Sprintf("pi 起来了，但 %d 秒内没有 Persona 扩展报这个会话，首条消息没有发送。装上扩展（pi install）再开。\n%s",
				int(piResumeWait.Seconds()), lastLines(screen, 12)), nil)
			return nil, n.abandonStart(ctx, launch, created, created.SpaceID, err)
		}
		select {
		case <-ctx.Done():
			return nil, n.abandonStart(ctx, launch, created, created.SpaceID, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// noteWorkAgent records the session as Persona's, bound to its pane, so herdr resumes it the way it was started.
func (n *Node) noteWorkAgent(ctx context.Context, launch host.Launcher, pane, id, cwd, launcher, kind, model string) {
	n.state.put(id, sessionState{Pane: pane, Launcher: launcher, Cwd: cwd, Model: model, StartedAt: time.Now().UTC()})
	n.bindPane(pane, id)
	n.holdResume(ctx, launch, pane, id, agentLaunch{Agent: kind, Launcher: launcher, Model: model, ID: id})
}

// sendToPi delivers text to a pi session through the extension that serves it. Refused, with nothing typed, when no
// extension does.
func (n *Node) sendToPi(ctx context.Context, id, text string) error {
	if _, _, ok := n.runtimes.rowOf(id); !ok {
		return refusal("pi_no_extension", piNoExtension+"。这次没有发送。", nil)
	}
	owner := n.runtimes.ownerOf(id)
	if owner == nil {
		return refusal("pi_no_extension", piNoExtension+"。这次没有发送。", nil)
	}
	params, _ := json.Marshal(map[string]any{"session_id": id, "text": text})
	_, err, _ := n.runtimeHandle(ctx, owner, "session.send", params)
	return err
}

// waitWorkAgent waits until herdr reports an agent of herdrName in pane, and answers its session id (its own when it
// names one, else the pane's terminal row).
func (n *Node) waitWorkAgent(ctx context.Context, pane, herdrName string) (string, host.Agent, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			screen, _ := n.readPane(context.Background(), pane, "recent-unwrapped", 30)
			return "", host.Agent{}, fmt.Errorf("wait for %s: %w\n%s", herdrName, ctx.Err(), lastLines(screen, 12))
		case <-ticker.C:
		}
		if err := n.refreshHosts(ctx); err != nil {
			continue
		}
		n.mu.Lock()
		var found host.Agent
		var id string
		for candidate, a := range n.agents {
			if a.PaneID == pane && a.Agent == herdrName {
				found, id = a, candidate
				break
			}
		}
		n.mu.Unlock()
		if id != "" {
			return id, found, nil
		}
	}
}

// abandonStart closes the tab a start opened and returns err. A pane the user picked is left open.
func (n *Node) abandonStart(ctx context.Context, launch host.Launcher, created *host.Created, ws string, err error) error {
	if created != nil {
		n.closeNewTab(ctx, launch, created, ws) // the one way a start that never ran closes its tab (terminal_guard_test.go)
	}
	return err
}

// workAgentCalls serves a hub call for a cursor or agy session herdr runs. A message goes through herdr's
// agent.prompt (never typed blind), an interrupt is Esc, and a page is one notice: Persona does not read these
// conversations. The pane's own calls (keys, screen, close) fall through to the node.
func (n *Node) workAgentCalls(ctx context.Context, method, id string, a host.Agent, params json.RawMessage) (any, error, bool) {
	switch method {
	case "session.items":
		notice := fmt.Sprintf("Persona 还不读 %s 的对话：手机上能发消息、看终端。", a.Agent)
		return map[string]any{"items": []proto.Item{{ID: a.Agent + "#0", Seq: 1, Role: "system", Kind: "notice", Text: notice,
			TS: time.Now().UTC()}}, "has_more": false, "history": a.Agent + "-pane:" + id, "to_seq": 1}, nil, true
	case "session.item", "session.image":
		return nil, fmt.Errorf("no item"), true
	case "session.watch", "session.unwatch":
		return okResult(nil), nil, true
	case "session.send":
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err, true
		}
		if err := n.hostPrompt(ctx, a.PaneID, p.Text); err != nil {
			return nil, fmt.Errorf("send: %w", err), true
		}
		return okResult(nil), nil, true
	case "session.interrupt":
		return okResult(n.sendKeys(ctx, a.PaneID, "escape")), nil, true
	default:
		return nil, nil, false
	}
}
