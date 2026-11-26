package node

import "net"

// putMCPCallerCwd attests only the launch directory of a process this node owns.
// A session name, runtime row, hook cwd or tool argument cannot supply it.
func (n *Node) putMCPCallerCwd(c net.Conn, params map[string]any, session string) {
	pid, err := peerPID(c)
	if err != nil {
		return
	}
	if cwd := n.mcpCallerCwdIn(lineage(pid, parentPID), session); cwd != "" {
		params["caller_cwd"] = cwd
	}
}

func (n *Node) mcpCallerCwdIn(pids []int, session string) string {
	if session == "" {
		return ""
	}
	type caller struct{ session, cwd string }
	roots := map[int]caller{}
	n.mu.Lock()
	add := func(id string, p *assistantProc) {
		if p == nil || p.cmd == nil || p.cmd.Process == nil {
			return
		}
		select {
		case <-p.exited:
			return
		default:
		}
		if id == "" && p.cx != nil {
			id = p.cx.key
		}
		if id != "" {
			roots[p.cmd.Process.Pid] = caller{id, p.cwd}
		}
	}
	for id, p := range n.procs {
		add(id, p)
	}
	for _, p := range n.akeys {
		if p != nil {
			add(p.id, p)
		}
	}
	if n.spare != nil {
		add(n.spare.id, n.spare)
	}
	n.mu.Unlock()
	for _, pid := range pids {
		if p, ok := roots[pid]; ok {
			if n.canonicalAssistantSession(n.resolveAssistantKey(p.session)) == session {
				return p.cwd
			}
			return "" // a nested conversation cannot borrow its parent's directory
		}
	}
	return ""
}
