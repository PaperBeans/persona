package link

import "github.com/nwverse/persona/server/internal/linkwire"

// The "Needs you" summary (docs/operations/link.md "Needs you"): the manager that carries this computer tells its
// Persona Link which agents wait for the user, so the menu bar app and the Windows tray can show and notify them while
// no web page is open. A full snapshot each time, never a delta. Active mode: an attention message on the link, to a
// link whose hello listed FeatureAttention. Passive mode: POST ConnectorAttentionPath, to a connector whose info
// listed it. persona-link puts the latest into its Status; it shows and approves nothing itself.

// FeatureAttention is in a hello's features (active mode) and the connector's info features (passive mode): this
// persona-link takes the summary.
const FeatureAttention = "attention"

// ConnectorAttentionPath is passive mode's endpoint for the summary: the body is an Attention, the answer 204.
const ConnectorAttentionPath = "/api/v1/connector/attention"

// AttentionBodyMax caps a posted summary. A full one is far below it (AttentionMaxItems items of clipped text).
const AttentionBodyMax = 64 << 10

// AttentionMaxItems is how many items a summary lists at most; More counts the rest.
const AttentionMaxItems = linkwire.AttentionMaxItems

// Attention is the summary, a contract of its own (internal/linkwire) so that the counting core's adapter can return
// it.
type Attention = linkwire.Attention

// AttentionItem is one agent of the summary.
type AttentionItem = linkwire.AttentionItem
