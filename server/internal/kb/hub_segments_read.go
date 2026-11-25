package kb

// The read path of hub_segments.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The hub's own transcript of a hub-mode meeting (docs/assistant/meeting-minutes.md "The transcript of record"):
// hub_segments.json beside meta.json, the sentences of the live pass or, once it ran, of the final pass, with a
// revision of their own. The upload still owns segments.json, complete and revision. Neither file is the transcript
// people read: that is the transcript of record (TranscriptOfRecord), the hub's sentences with the phone's segments
// where the hub has none, which transcript.md (rendered on every upload and every put of the hub's transcript),
// detail.segments, the minutes, the summary and the follow-up's seed take. hub_segments.json is never searched
// (transcript.md is the searched text), is in backups like segments.json, is not in an upload's undo journal
// (recordingFiles: a crash between it and transcript.md leaves transcript.md behind, which every retention pass mends,
// sweepTranscriptions), and goes with the trim.

const hubSegmentsFile = "hub_segments.json"

// HubTranscript is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type HubTranscript = recordingwire.HubTranscript

// readHubLocked is recording id's hub_segments.json (an empty one when there is none). The caller holds recFilesMu.
func (s *Store) readHubLocked(id string) (HubTranscript, error) {
	h := HubTranscript{Segments: []RecordingSegment{}}
	raw, err := os.ReadFile(s.path(KindRecording, id, hubSegmentsFile))
	if os.IsNotExist(err) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return HubTranscript{Segments: []RecordingSegment{}}, fmt.Errorf("hub transcript of %s: %w", id, err)
	}
	if h.Segments == nil {
		h.Segments = []RecordingSegment{}
	}
	return h, nil
}

// HubTranscript is recording rawID's hub transcript (rev 0 and no segments before the first put): ErrRecordingDeleted
// after a delete, ErrNotFound with no upload of it.
func (e *Engine) HubTranscript(rawID string) (HubTranscript, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return HubTranscript{}, ErrNotFound
	}
	st := e.Store
	if _, gone := st.tombstones()[id]; gone {
		return HubTranscript{}, ErrRecordingDeleted
	}
	if _, ok := st.Recording(id); !ok {
		return HubTranscript{}, ErrNotFound
	}
	st.recFilesMu.RLock()
	defer st.recFilesMu.RUnlock()
	return st.readHubLocked(id)
}

// TranscriptOfRecord is a recording's text of record: in device mode the upload's segments; in hub mode the hub's
// sentences, and each upload segment the hub's sentences, taken together (their union), cover no more than half of,
// in time order (the hub's first at the same start), with Source set (SourceHub, SourceDevice). The phone's text is
// made on the device in the recording's language only, so in hub mode it fills only where the hub has none. Persona's
// echo stays in it, marked (Echo: the hub's by the echo rule, the phone's as uploaded); what is built from the meeting
// reads it through MeetingLines.
func TranscriptOfRecord(hubMode bool, device, hub []RecordingSegment) []RecordingSegment {
	if !hubMode {
		return slices.Clone(device)
	}
	out := make([]RecordingSegment, 0, len(hub)+len(device))
	spans := make([][2]float64, 0, len(hub))
	for _, sg := range hub {
		sg.Source = SourceHub
		out = append(out, sg)
		spans = append(spans, [2]float64{sg.Start, sg.End})
	}
	union := UnionSpans(spans)
	for _, sg := range device {
		if coveredShare(sg.Start, sg.End, union) <= 0.5 {
			sg.Source = SourceDevice
			out = append(out, sg)
		}
	}
	slices.SortStableFunc(out, func(a, b RecordingSegment) int {
		switch {
		case a.Start < b.Start:
			return -1
		case a.Start > b.Start:
			return 1
		}
		return 0
	})
	return out
}

// recordChars is how many characters segs have without Persona's echo, which is not the meeting's text (meta.json's
// chars: in hub mode the transcript of record's). A meeting of only Persona's echo has none, so it gets no minutes.
func recordChars(segs []RecordingSegment) int {
	n := 0
	for _, sg := range MeetingLines(segs) {
		n += utf8.RuneCountInString(sg.Text)
	}
	return n
}

// hubMode: m says the recording is transcribed on the hub.
func hubMode(m *Meeting) bool { return m != nil && m.Transcription.Hub() }

// recordSnapshot is recording id's committed files (recordingSnapshot: an upload's undo journal while one is pending),
// its meeting.json (nil without one, or when it cannot be read: device mode) and its hub transcript, read together.
func (s *Store) recordSnapshot(id string) (map[string][]byte, *Meeting, HubTranscript, error) {
	if s.recordingReadWait != nil {
		s.recordingReadWait()
	}
	s.recFilesMu.RLock()
	defer s.recFilesMu.RUnlock()
	undo, err := s.readRecordingUndo(id)
	if err != nil {
		return nil, nil, HubTranscript{}, err
	}
	files := map[string][]byte(nil)
	if undo != nil {
		files = undo.Files
	} else if files, err = s.recordingDiskSnapshot(id); err != nil {
		return nil, nil, HubTranscript{}, err
	}
	m, err := s.readMeeting(id)
	if err != nil {
		m = nil
	}
	h, err := s.readHubLocked(id)
	if err != nil {
		h = HubTranscript{Segments: []RecordingSegment{}}
	}
	return files, m, h, nil
}

// recordOf is the transcript of record of a recordSnapshot.
func recordOf(files map[string][]byte, m *Meeting, h HubTranscript) []RecordingSegment {
	var segs []RecordingSegment
	_ = json.Unmarshal(files["segments.json"], &segs)
	return TranscriptOfRecord(hubMode(m), segs, h.Segments)
}
