package node

import (
	"github.com/nwverse/persona/server/internal/node/screenport"
	"sync/atomic"

	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// hubModules is the run-time module switch as this computer's hub announced it (node.configure's hub_features, by the
// clients' rule: moduleswitch.FromFeatures). A node has no settings row; its hub's manager reads the `modules` row and
// announces a module.<id> flag for each module it loaded. The node's provider seams (imessageport, …) ask it. It is the
// process's, not one connection's: a node serves one hub, and a reconnect's node.configure replaces it. Before the
// first node.configure, or from a hub that publishes no module flags, every module is on.
var hubModules atomic.Pointer[moduleswitch.Set]

// nodeModules is the switch the node's seams read now.
func nodeModules() moduleswitch.Set {
	if s := hubModules.Load(); s != nil {
		return *s
	}
	return moduleswitch.Set{}
}

// setHubModules records the hub's features: the modules whose seams this build has (moduleswitch.Seams) and whose flag
// the hub does not announce are off.
func setHubModules(features []string) {
	var known []string
	for _, s := range moduleswitch.Seams() {
		known = append(known, s.Module)
	}
	s := moduleswitch.FromFeatures(features, known)
	hubModules.Store(&s)
}

// screenLinks are the screen-share module's sessions (screenport), nil when this build has none or the hub switched
// the module off: a live screen is then the plain relayed stream, as in a build without it.
func (n *Node) screenLinks() screenport.Links {
	if n.links == nil || !screenport.On(nodeModules()) {
		return nil
	}
	return n.links
}
