// Package screenport is how the screen-share module reaches the node (docs/architecture/core-and-modules.md "The
// node's modules"): a live screen whose phone asks for a session (#6398) gets one, with its RFB and video channels
// resumed across the hub's relays and the computer's direct routes. The node's composition root
// (internal/modules/all/node) provides the module's sessions; a build without the module (persona_without_screen_share,
// persona_core_only) provides none, and a live screen runs as the plain relayed stream it already uses when the phone
// asks for no session; so does a node whose hub announced the module switched off (moduleswitch.FromFeatures), from
// then on. It is its own package so that the composition root need not import internal/node.
package screenport

import (
	"io"

	"github.com/nwverse/persona/server/internal/moduleswitch"
)

// Session is one pair's live screen session.
type Session interface {
	// Video joins the helper's video stream to the session as a channel; the relay stream to start, false when the
	// session ended.
	Video(helper io.ReadWriteCloser) (io.ReadWriteCloser, bool)
	// HasVideo says whether the session has a live video channel.
	HasVideo() bool
	// Reattach is a hub relay rejoining the session's RFB channel, or with video its current video channel.
	Reattach(video bool) (io.ReadWriteCloser, bool)
}

// Links are the node's screen sessions, one per pair.
type Links interface {
	// Get is the pair's live session, nil when there is none.
	Get(pair string) Session
	// Open starts the pair's session over Screen Sharing's connection rfb, which it owns from then on, and returns
	// the stream the hub's relay carries.
	Open(pair string, rfb io.ReadWriteCloser) io.ReadWriteCloser
	// Len is how many sessions are live.
	Len() int
}

// Factory makes the node's screen sessions; logf is the node's log.
type Factory func(logf func(string, ...any)) Links

var port = moduleswitch.NewPort[Factory]("node/screenport")

// Provide sets the screen-share module's factory under its module id; the node's composition root calls it from init.
func Provide(module string, f Factory) { port.Provide(module, f) }

// New is the module's factory, nil when this build has no screen-share module.
func New() Factory {
	f, _ := port.Get(moduleswitch.Set{})
	return f
}

// On reports whether this build has the module and s has it on: the node asks it before it uses a session.
func On(s moduleswitch.Set) bool { return port.On(s) }
