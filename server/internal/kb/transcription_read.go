package kb

// The read path of transcription.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Hub transcription (#3862 phase 5, docs/assistant/meeting-minutes.md "Phase 5: hub transcription"): a meeting
// uploaded with transcription hub is transcribed on the hub from the audio the phone streams. What the knowledge base
// keeps of it: the mode, the state and the timeline in meeting.json (never meta.json, as the room: an older manager
// after a rollback rewrites meta.json without fields it does not know), the hub's own transcript in hub_segments.json
// (hub_segments.go), the audio in attachments/audio/ until the minutes from the final transcript are written
// (recording_audio.go). The transcript of record (TranscriptOfRecord) is what everything that reads the transcript
// reads: transcript.md, detail.segments, search, the minutes, the summary and the follow-up's seed. The manager owns
// the socket, the jobs and the final pass; it changes the state through TranscriptionUpdate.

// Re-exported from recordingwire.
const (
	TranscriptionDevice      = recordingwire.TranscriptionDevice
	TranscriptionHub         = recordingwire.TranscriptionHub
	TranscriptionLive        = recordingwire.TranscriptionLive
	TranscriptionFinishing   = recordingwire.TranscriptionFinishing
	TranscriptionFinal       = recordingwire.TranscriptionFinal
	TranscriptionFailed      = recordingwire.TranscriptionFailed
	TranscriptionUnavailable = recordingwire.TranscriptionUnavailable
	TranscriptionNotMeeting  = recordingwire.TranscriptionNotMeeting
	PartTranscription        = recordingwire.PartTranscription
	PartInterim              = recordingwire.PartInterim
	MaxTimelineRoles         = recordingwire.MaxTimelineRoles
	MaxTimelineSpans         = recordingwire.MaxTimelineSpans
	AudioRate                = recordingwire.AudioRate
	MaxAudioSeconds          = recordingwire.MaxAudioSeconds
	HubPassLive              = recordingwire.HubPassLive
	HubPassFinal             = recordingwire.HubPassFinal
	SourceHub                = recordingwire.SourceHub
	SourceDevice             = recordingwire.SourceDevice
)

// Re-exported from recordingwire.
var (
	ErrHubTranscribes     = recordingwire.ErrHubTranscribes
	ErrNotHub             = recordingwire.ErrNotHub
	ErrTranscriptionFinal = recordingwire.ErrTranscriptionFinal
	ErrAudioFull          = recordingwire.ErrAudioFull
	ErrBadAudio           = recordingwire.ErrBadAudio
)

// TimelineRole is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type TimelineRole = recordingwire.TimelineRole

// Timeline is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type Timeline = recordingwire.Timeline

// TranscriptionView is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type TranscriptionView = recordingwire.TranscriptionView

// hubTranscription is r's hub-mode transcription, nil when r is not transcribed on the hub.
func hubTranscription(r *Recording) *MeetingTranscription { return recordingwire.HubTranscription(r) }
