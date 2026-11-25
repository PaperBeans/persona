package hub

import (
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// wsHelloMesh holds the mesh hubs list inside wsHello without untyped maps.
type wsHelloMesh struct {
	Hubs []meshHub `json:"hubs"`
}

// wsHello is the strongly-typed payload of the first message on every WebSocket.
// Fields are declared in sorted JSON key order so wire samples remain byte-identical, and fields added
// here must not reuse a key the iOS app decodes with a different type (its LiveConnection Envelope).
type wsHello struct {
	Build            int64                  `json:"build"`
	Channel          string                 `json:"channel"`
	ClientID         string                 `json:"client_id"`
	Endpoints        []Endpoint             `json:"endpoints"`
	ExpiresAt        string                 `json:"expires_at,omitempty"`
	Features         []string               `json:"features"`
	FP               string                 `json:"fp"`
	HubID            string                 `json:"hub_id"`
	HubName          string                 `json:"hub_name"`
	ImageLimits      proto.ImageLimitsTable `json:"image_limits"`
	Locale           string                 `json:"locale"`
	Mesh             wsHelloMesh            `json:"mesh"`
	MessageSendState int                    `json:"message_send_state"`
	MinAppBuild      int                    `json:"min_app_build"`
	Protocol         int                    `json:"protocol"`
	PublicURL        string                 `json:"public_url,omitempty"`
	PushGrading      bool                   `json:"push_grading"`
	ReadOnly         bool                   `json:"read_only,omitempty"`
	ServerVersion    string                 `json:"server_version"`
	TLSPort          int                    `json:"tls_port"`
	TodoSendState    int                    `json:"todo_send_state"`
	Type             string                 `json:"type"`
}

// wsStreamHello extends wsHello with event-stream resume metadata emitted by helloLocked.
type wsStreamHello struct {
	wsHello
	EventStream string `json:"event_stream"`
	EventSeq    int64  `json:"event_seq"`
	Resumed     bool   `json:"resumed"`
}

// health answers GET /api/v1/health, the one route without authentication.
func (h *Hub) health(w http.ResponseWriter, r *http.Request) {
	h.healthWithSource(w, r, h.sourceOf)
}

func (h *Hub) healthWithSource(w http.ResponseWriter, r *http.Request, sourceOf func(*http.Request) source) {
	port, fp := h.tlsPin()
	response := map[string]any{"ok": true, "hub_id": h.hubID(), "hub_name": h.hubName(), "tls_port": port, "fp": fp}
	if sourceOf(r) != sourcePublic {
		response["version"], response["features"] = h.version, h.features()
		response["channel"], response["build"] = h.build.Channel, h.build.Number
		// The commit it was built from ("-dirty" after it with changes not committed; "" when the build did not say):
		// a task is deployed only once this contains its PR's merge commit (assistant deploy_check.go, #3205).
		response["commit"] = h.build.Commit
		response["protocol"], response["min_app_build"] = proto.Protocol, proto.MinAppBuild
		response["update"] = map[string]any{"confirmed": h.upd == nil || h.upd.isConfirmed()} // the watchdog waits for this
	}
	writeJSON(w, response)
}

// hello is the first message on every WebSocket. Fields added here must not reuse a key the iOS app decodes with a
// different type (its LiveConnection Envelope), or the whole hello is dropped there (hub-changes.md 1.2).
func (h *Hub) hello() wsHello {
	port, fp := h.tlsPin()
	out := wsHello{
		Type:          "hello",
		ServerVersion: h.version,
		ClientID:      randomHex(6),
		Features:      h.features(),
		// Hub-authored trust markers, never merged from a manager's feature report: a session message's receipts
		// (send.go) and a to-do write's (todo_receipts.go). Legacy hubs reserve hello but can forward arbitrary
		// extension feature strings.
		MessageSendState: proto.MessageSendStateVersion,
		TodoSendState:    proto.TodoSendStateVersion,
		Channel:          h.build.Channel,
		Build:            h.build.Number,
		Protocol:         proto.Protocol,
		MinAppBuild:      proto.MinAppBuild,
		HubID:            h.hubID(),
		HubName:          h.hubName(),
		Endpoints:        h.endpointList(),
		TLSPort:          port,
		FP:               fp,
		// The hubs of this hub's mesh group, itself included (docs/archive/operations/plan-hub-mesh.md 2.6): only after authentication.
		Mesh: wsHelloMesh{Hubs: h.meshHubs()},
		// Push grading switch (hub.ln_mode / push_grading): synced on hello and on switch toggles.
		PushGrading: h.push.pushGradingOn(),
		// the user's language (feature locale, locale.go)
		Locale: string(h.userLang()),
		// The pictures a message may carry (#12973): the clients size their pickers by them, the send routes check them.
		ImageLimits: proto.HubImageLimits,
	}
	// The address the hub's own links use (hub.json public_url; the assistant's /s/…, /persona/…, /todos links, #2194): the
	// apps tell those links from other sites by it even when they reach the hub at another address (#2696). Only when set.
	if h.cfg == nil {
		return out
	}
	if u := strings.TrimRight(strings.TrimSpace(h.cfg.PublicURL), "/"); u != "" {
		out.PublicURL = u
	}
	return out
}

// helloFor returns hello populated with optional read-only session metadata for roleReadOnly connections.
func (h *Hub) helloFor(who principal) wsHello {
	out := h.hello()
	if who.Role == roleReadOnly {
		out.ReadOnly = true
		if !who.ExpiresAt.IsZero() {
			out.ExpiresAt = who.ExpiresAt.UTC().Format(time.RFC3339)
		}
	}
	return out
}
