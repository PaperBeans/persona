package kb

// The read path of recording_photos.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"os"

	"github.com/nwverse/persona/server/internal/recordingwire"
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

// PhotosRead are recording rawID's listed photos, oldest first, each cited as its current minutes say, and
// PartPhotos' revision; also on a read-only knowledge base. ErrRecordingDeleted after a delete, ErrNotFound with no
// upload of it. The writers package has its own Engine.Photos over a store shim, so this Core method stays distinct.
func (e *Engine) PhotosRead(rawID string) ([]Photo, int64, error) {
	// Under recMu, as the minutes are stored: their writer moves the revision before the minutes it is for, and a list
	// read between the two would take the new revision with the old cited.
	e.Store.recMu.Lock()
	defer e.Store.recMu.Unlock()
	r, err := e.Store.photosRecording(rawID)
	if err != nil {
		return nil, 0, err
	}
	b, err := e.Store.readPhotos(r.ID)
	if err != nil {
		return nil, 0, err
	}
	cited := e.Store.citedPhotos(r.ID)
	out := make([]Photo, 0, len(b.Photos))
	for _, p := range b.Photos {
		out = append(out, p.Photo(cited))
	}
	return out, b.Rev, nil
}

// PhotoImage is photo n of recording rawID: its mime type and bytes. ErrRecordingDeleted, ErrNotFound, then
// ErrPhotoNotFound when it is not listed (also one the owner deleted, or a trim removed).
func (e *Engine) PhotoImage(rawID string, n int) (string, []byte, error) {
	r, err := e.Store.photosRecording(rawID)
	if err != nil {
		return "", nil, err
	}
	st := e.Store
	st.recFilesMu.RLock()
	defer st.recFilesMu.RUnlock()
	b, _, err := st.readPhotosLocked(r.ID)
	if err != nil {
		return "", nil, err
	}
	i := b.Index(n)
	if i < 0 {
		return "", nil, ErrPhotoNotFound
	}
	p := b.Photos[i]
	data, err := os.ReadFile(st.path(KindRecording, r.ID, photosDir, p.FileName()))
	if os.IsNotExist(err) {
		return "", nil, ErrPhotoNotFound
	}
	if err != nil {
		return "", nil, err
	}
	return p.Mime, data, nil
}

// Re-exported from recordingwire.
var (
	ErrPhotoNotFound = recordingwire.ErrPhotoNotFound
	ErrPhotoDeleted  = recordingwire.ErrPhotoDeleted
	ErrPhotosFull    = recordingwire.ErrPhotosFull
	ErrOpIDReused    = recordingwire.ErrOpIDReused
	ErrBadPhoto      = recordingwire.ErrBadPhoto
)

// PhotoUpload is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type PhotoUpload = recordingwire.PhotoUpload

// readPhotos is readPhotosLocked for a reader, also on a read-only knowledge base.
func (s *Store) readPhotos(id string) (photoBook, error) {
	s.recFilesMu.RLock()
	defer s.recFilesMu.RUnlock()
	b, _, err := s.readPhotosLocked(id)
	return b, err
}

// photosRecording is the recording rawID names, for the photo routes: ErrRecordingDeleted after a delete, ErrNotFound
// with no upload of it.
func (s *Store) photosRecording(rawID string) (*Recording, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	if _, gone := s.tombstones()[id]; gone {
		return nil, ErrRecordingDeleted
	}
	r, ok := s.Recording(id)
	if !ok {
		return nil, ErrNotFound
	}
	return r, nil
}

// PhotoTexts are the words of recording rawID's listed photos, oldest first (a photo with none has all three ""):
// what the minutes, kb_get, the follow-up's seed and meeting.answer read of them. None for an unknown recording.
func (e *Engine) PhotoTexts(rawID string) []PhotoText {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil
	}
	b, err := e.Store.readPhotos(id)
	if err != nil {
		return nil
	}
	var out []PhotoText
	for _, p := range b.Photos {
		out = append(out, p.Words())
	}
	return out
}
