package extmgr

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/proto"
)

// builtinNode is a built-in extension's node section: its program is in personad (node.command "builtin",
// node/builtin.go), on the computers picked in its field machines. No package, lease, state rows or consent of its own
// (Manifest.NodeProgram is nil for it): what it is on each machine shows as it always did (the machine page, the
// settings' rows).
func builtinNode(machines string) *extension.NodeRuntime {
	return &extension.NodeRuntime{Command: extension.Command{Path: extension.NodeBuiltin}, Machines: machines}
}

// builtinConnected are the built-in programs' part of a machine's reconcile when it connects (reconcileNodePrograms):
// what each asks of the machine again. They run whether the extension is on or not: each knows what to do when it is
// off (tellNoLock says off again, the rest ask nothing).
var builtinConnected = []func(h *Manager, ctx context.Context, m *Machine){
	func(h *Manager, ctx context.Context, m *Machine) { h.refreshComputer(ctx, m) }, // cua: only when it is on for m (computer.go)
	func(h *Manager, ctx context.Context, m *Machine) { h.tellNoLock(ctx, m) },      // cua's "Don't auto-lock while the extension is on" (computer_nolock.go)
	func(h *Manager, ctx context.Context, m *Machine) { h.tellCursor(ctx, m) },      // cua's "Show the agent's cursor" (computer_cursor.go)
	func(h *Manager, ctx context.Context, m *Machine) { h.refreshBrowser(ctx, m) },  // ego (browser.go)
	func(h *Manager, ctx context.Context, m *Machine) { h.refreshIPhone(ctx, m) },   // iphone (iphone.go)
}

// reconcileNodePrograms brings a machine that just connected in line with what should run on it: the built-in
// programs ask what they need (builtinConnected), the packaged ones are synced (plugin.node.sync). Each on its own,
// none waits for another.
func (h *Manager) reconcileNodePrograms(ctx context.Context, m *Machine) {
	for _, connected := range builtinConnected {
		go connected(h, ctx, m)
	}
	go h.syncExtensionNodes(ctx, m) // plugins' programs on this computer (extension_nodes.go)
}

// builtinPrefixes are the extensions whose programs are built into personad (cua, ego, iphone; node/builtin.go) by the prefix of
// their old method names.
var builtinPrefixes = map[string]string{"computer.": "cua", "browser.": "ego", "iphone.": "iphone"}

// callBuiltin asks m's built-in program for method, given by its old name (computer.status, browser.cookies.list…):
// over plugin.node.call {id, method} on a node that says it has the program (node.info's builtin_programs, #1830
// step 5), by the old name on one from before. Which way is m's own word, never a retry: an older node gets exactly
// the call it always got.
func (m *Machine) callBuiltin(ctx context.Context, method string, params any, out any) error {
	for prefix, id := range builtinPrefixes {
		short, ok := strings.CutPrefix(method, prefix)
		if !ok {
			continue
		}
		m.mu.Lock()
		has := slices.Contains(m.info.BuiltinPrograms, id)
		m.mu.Unlock()
		if !has {
			break
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		return m.call(ctx, "plugin.node.call", proto.PluginNodeCall{ID: id, Method: short, Params: raw}, out)
	}
	return m.call(ctx, method, params, out)
}

// callBuiltinProgram asks m's built-in program id for method, a name it has kept over plugin.node.call (imessage's
// connector.*): that way to a node that says it has the program, as is to one from before.
func (m *Machine) callBuiltinProgram(ctx context.Context, id, method string, params any, out any) error {
	m.mu.Lock()
	has := slices.Contains(m.info.BuiltinPrograms, id)
	m.mu.Unlock()
	if !has {
		return m.call(ctx, method, params, out)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return m.call(ctx, "plugin.node.call", proto.PluginNodeCall{ID: id, Method: method, Params: raw}, out)
}

// builtinProgramModules are the built-in node programs a module provides (internal/modules/all/node): the program's id
// in node.info's builtin_programs and the module's id. cua and ego are the core's.
var builtinProgramModules = map[string]string{iphoneExtensionID: "phone-extra", imessageExtensionID: "imessage"}

// switchedInfo is a node's node.info as the manager uses it: without the built-in programs of the modules its
// `modules` setting switched off (moduleswitch), so it offers and calls none of them, as for a node built without
// them. The node learns the switch too (from the hub's features), but only at its next node.configure, after its
// first node.info.
func (h *Manager) switchedInfo(info proto.NodeInfo) proto.NodeInfo {
	s := h.modules()
	off := func(id string) bool { m, ok := builtinProgramModules[id]; return ok && !s.On(m) }
	if !slices.ContainsFunc(info.BuiltinPrograms, off) {
		return info
	}
	info.BuiltinPrograms = slices.DeleteFunc(slices.Clone(info.BuiltinPrograms), off)
	return info
}
