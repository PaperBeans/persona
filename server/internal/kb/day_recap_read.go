package kb

// The read path of day_recap.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
	"os"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The daily recap (docs/assistant/day-recap.md "What the hub stores and returns"): a day recording's recap.json
// (DayRecap, what detail.recap is) and recap.md (the same as Markdown with clock times, what the searches and kb_get
// read) in its directory, beside summary.md (the recap's summary, what detail.summary shows). Like minutes they survive
// the trim (recording_trim.go) until minutes_days; recap_notes.json, the notes the recap is merged from
// (day_recap_write.go), is made of the transcript and goes with it. A recap becomes due for a reason (RecapTarget),
// persisted with the recording until a recap is written or the model failed at it; recap_state says which.

const (
	recapFile      = recordingwire.RecapFile
	recapMarkdown  = recordingwire.RecapMarkdown
	recapNotesFile = recordingwire.RecapNotesFile
)

// DayRecap is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type DayRecap = recordingwire.DayRecap

// hasRecap: recording id has a recap that can be read.
func (s *Store) hasRecap(id string) bool {
	_, err := s.readRecap(id)
	return err == nil
}

// readRecap is recording id's (a stored id) recap.json, ErrNotFound without one.
func (s *Store) readRecap(id string) (*DayRecap, error) {
	raw, err := os.ReadFile(s.path(KindRecording, id, recapFile))
	if err != nil {
		return nil, ErrNotFound
	}
	var rc DayRecap
	if err := json.Unmarshal(raw, &rc); err != nil {
		return nil, err
	}
	return &rc, nil
}

// ReadDayRecap is day recording id's recap, ErrNotFound when it has none (or there is no such recording).
func (e *Engine) ReadDayRecap(rawID string) (*DayRecap, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	return e.Store.readRecap(id)
}

// dayDetail is detail.meta and detail.recap of recording id, given its meta.json: a day recording's meta always says
// next and base (0 included, as reconciliation does), and its recap.json is read once it has one; nil for any other
// recording.
func (e *Engine) dayDetail(id string, meta []byte) ([]byte, []byte) {
	var head struct {
		Kind string           `json:"kind"`
		Next *json.RawMessage `json:"next"`
		Base *json.RawMessage `json:"base"`
	}
	if json.Unmarshal(meta, &head) != nil || head.Kind != RecordingDay {
		return meta, nil
	}
	if head.Next == nil || head.Base == nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(meta, &m) == nil && m != nil {
			for _, k := range []string{"next", "base"} {
				if _, ok := m[k]; !ok {
					m[k] = json.RawMessage("0")
				}
			}
			if out, err := json.Marshal(m); err == nil {
				meta = out
			}
		}
	}
	raw, err := os.ReadFile(e.Store.path(KindRecording, id, recapFile))
	if err != nil || !json.Valid(raw) {
		return meta, nil
	}
	return meta, raw
}

// Re-exported from recordingwire.
const (
	RecapDueEvening  = recordingwire.RecapDueEvening
	RecapDueComplete = recordingwire.RecapDueComplete
	RecapDueCutoff   = recordingwire.RecapDueCutoff
	RecapDueAsked    = recordingwire.RecapDueAsked
	RecapDueRepair   = recordingwire.RecapDueRepair
)

// Re-exported from recordingwire.
const (
	FinalComplete = recordingwire.FinalComplete
	FinalCutoff   = recordingwire.FinalCutoff
)

// RecapSpan is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecapSpan = recordingwire.RecapSpan

// Re-exported from recordingwire.
const (
	RecapMeeting = recordingwire.RecapMeeting
	RecapOutside = recordingwire.RecapOutside
)

// DayRecapEntry is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type DayRecapEntry = recordingwire.DayRecapEntry

// RecapTodo is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecapTodo = recordingwire.RecapTodo
