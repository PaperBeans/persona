package recordingwire

import (
	"github.com/nwverse/persona/server/internal/kbwire"
)

// HubTranscript is hub_segments.json: the hub's own sentences, with their own revision.
type HubTranscript struct {
	Rev      int64                     `json:"rev"`
	Pass     string                    `json:"pass"` // HubPassLive | HubPassFinal
	Segments []kbwire.RecordingSegment `json:"segments"`
}
