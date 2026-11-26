package kb

// The writes of hub_segments.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

// recordLocked is the transcript of record of recording r (its Conv read) with device its upload's segments, for the
// commit (which holds recFilesMu).
func (s *Store) recordLocked(r *Recording, device []RecordingSegment) []RecordingSegment {
	if !r.HubTranscribed() {
		return device
	}
	h, _ := s.readHubLocked(r.ID)
	return TranscriptOfRecord(true, device, h.Segments)
}
