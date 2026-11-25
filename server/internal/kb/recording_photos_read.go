package kb

// The read path of recording_photos.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"os"
)

// Meeting photos (docs/assistant/meeting-minutes.md "Phase 3: photos"): the phone adds the photos taken during a
// meeting (POST /api/v1/recordings/{id}/photos). Each is attachments/p<n>.jpg (or .png) in the recording's directory,
// so a delete takes it with it, listed in photos.json beside meta.json with what the phone sent (its id, time,
// recognized text and caption) and what the hub adds (the room's note, whether it went into the room). photos.json is
// bookkeeping (the requests' digests, the photos the owner deleted, those the room is still to be told of) and never
// searched; photos.md, a line per listed photo, is the text the searches read. n counts from 1 per recording and is
// never reused, also after a deletion. Photos are the hub's, never in an upload's undo journal (recordingFiles). Adding
// or deleting one makes the minutes again without a second push (minutes.go); the trim keeps the photos the minutes
// cite (recording_trim.go). The hub's models read the photos' words only, never an image.

const (
	photosFile = "photos.json"
	photosText = "photos.md"
	photosDir  = "attachments"
)

// PhotosMarkdown is recording rawID's photos.md, a line per listed photo as the searches find it ("[P3] 14:32:05
// photo: caption · text · note"): what kb_get prints of its photos. "" without photos (or an unknown recording).
func (e *Engine) PhotosMarkdown(rawID string) string {
	id, ok := RecordingID(rawID)
	if !ok {
		return ""
	}
	e.Store.recFilesMu.RLock()
	defer e.Store.recFilesMu.RUnlock()
	raw, err := os.ReadFile(e.Store.path(KindRecording, id, photosText))
	if err != nil {
		return ""
	}
	return string(raw)
}
