package hub

import "github.com/nwverse/persona/server/internal/assistant/fleetcount" // path-no-core

// surface is the badge, decided in the hub.
func surface() fleetcount.Surface { return fleetcount.SurfaceBadge() }
