package kb

// The writes of recording_photos.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Re-exported from recordingwire.
const (
	MaxPhotos       = recordingwire.MaxPhotos
	MaxPhotoText    = recordingwire.MaxPhotoText
	MaxPhotoCaption = recordingwire.MaxPhotoCaption
	MaxPhotoNote    = recordingwire.MaxPhotoNote
)

// Re-exported from recordingwire.
const (
	PhotoJPEG = recordingwire.PhotoJPEG
	PhotoPNG  = recordingwire.PhotoPNG
)

// Photo is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type Photo = recordingwire.Photo

// PhotoRef is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type PhotoRef = recordingwire.PhotoRef

// PhotoText is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type PhotoText = recordingwire.PhotoText

// photoBook is photos.json.
type photoBook struct {
	Next   int           `json:"next"` // the next n; n is never reused
	Rev    int64         `json:"rev"`  // PartPhotos' revision: only goes up
	Photos []storedPhoto `json:"photos"`
	// Deleted are the photos the owner deleted: deleting one again answers false, adding its id again ErrPhotoDeleted.
	Deleted []PhotoRef `json:"deleted,omitempty"`
	// Withdraw are the photos deleted after they went into the room, which the room is still to be told of.
	Withdraw []PhotoRef `json:"withdraw,omitempty"`
}

// storedPhoto is a photo in photos.json: Photo without Cited (the minutes say that), with the request's digest and
// Queued while it waits to go into the room (until its send succeeded: Queued and Forwarded is a send in doubt).
type storedPhoto struct {
	N         int       `json:"n"`
	ID        string    `json:"id"`
	At        float64   `json:"at"`
	Mime      string    `json:"mime"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Text      string    `json:"text,omitempty"`
	Caption   string    `json:"caption,omitempty"`
	Note      string    `json:"note,omitempty"`
	Forwarded bool      `json:"forwarded,omitempty"`
	Queued    bool      `json:"queued,omitempty"`
	AddedAt   time.Time `json:"added_at"`
	Digest    string    `json:"digest"`
}

func (p storedPhoto) photo(cited map[int]bool) Photo {
	return Photo{N: p.N, ID: p.ID, At: p.At, Mime: p.Mime, Width: p.Width, Height: p.Height, Text: p.Text, Caption: p.Caption,
		Note: p.Note, Cited: cited[p.N], Forwarded: p.Forwarded, AddedAt: p.AddedAt}
}

func (p storedPhoto) text() PhotoText {
	return PhotoText{N: p.N, At: p.At, Caption: p.Caption, Text: p.Text, Note: p.Note}
}

// fileName is the photo's file in attachments/.
func (p storedPhoto) fileName() string {
	ext := ".jpg"
	if p.Mime == PhotoPNG {
		ext = ".png"
	}
	return "p" + strconv.Itoa(p.N) + ext
}

// readPhotosLocked is recording id's photos.json, empty without one (exists false). The caller holds recFilesMu.
func (s *Store) readPhotosLocked(id string) (b photoBook, exists bool, err error) {
	exists, err = readJSON(s.path(KindRecording, id, photosFile), &b)
	if err != nil {
		return photoBook{}, true, fmt.Errorf("photos of %s: %w", id, err)
	}
	// Past every number in it: a file written by hand or cut short must not hand out one again.
	b.Next = max(b.Next, 1)
	for _, p := range b.Photos {
		b.Next = max(b.Next, p.N+1)
	}
	for _, d := range b.Deleted {
		b.Next = max(b.Next, d.N+1)
	}
	return b, exists, nil
}

// putPhotosLocked writes recording id's photos.json and photos.md from b: start and zone place the photos' clock
// times. The caller holds recFilesMu and checked photosHereLocked.
func (s *Store) putPhotosLocked(id string, b *photoBook, start time.Time, zone *time.Location) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if b.Photos == nil {
		b.Photos = []storedPhoto{}
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRecordingFile(s.path(KindRecording, id, photosFile), append(raw, '\n')); err != nil {
		return err
	}
	_, err = s.putPhotosTextLocked(id, b, start, zone)
	return err
}

// putPhotosTextLocked makes recording id's photos.md what b lists (none without a listed photo); changed when it was
// not so (photos.json is written first: a crash or a failure between leaves photos.md behind it). The caller holds
// recFilesMu.
func (s *Store) putPhotosTextLocked(id string, b *photoBook, start time.Time, zone *time.Location) (changed bool, err error) {
	md := s.path(KindRecording, id, photosText)
	if len(b.Photos) == 0 {
		if err := os.Remove(md); os.IsNotExist(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, nil
	}
	var out strings.Builder
	for _, p := range b.Photos {
		out.WriteString(photoLine(p.N, clockAt(start, p.At, zone), photoWords(p.text(), false)))
		out.WriteByte('\n')
	}
	if raw, err := os.ReadFile(md); err == nil && string(raw) == out.String() {
		return false, nil
	}
	return true, writeRecordingFile(md, []byte(out.String()))
}

// photoLabel marks a photo's line in photos.md and in the minutes' input.
const photoLabel = "photo:"

// photoLine is a photo as photos.md and the minutes' input have it: "[P3] 14:32:05 photo: caption · text · note".
func photoLine(n int, clock, words string) string {
	return strings.TrimRight("[P"+strconv.Itoa(n)+"] "+clock+" "+photoLabel+" "+words, " ")
}

// photoWords is a photo's caption, recognized text and note on one line, " · " between those it has; clipped, each
// at most what the minutes' input reads of it (the text as long as a transcript line). Other people's words: nothing
// in them can close a context block (prompts.ContextBody), in the minutes' input as in photos.md (kb_get, the searches).
func photoWords(p PhotoText, clipped bool) string {
	var parts []string
	for _, w := range []struct {
		s string
		n int
	}{{p.Caption, MaxPhotoCaption}, {p.Text, minutesLineRunes}, {p.Note, MaxPhotoNote}} {
		t := prompts.ContextBody(strings.Join(strings.Fields(w.s), " "))
		if clipped {
			t = clip(t, w.n)
		}
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " · ")
}

// minutesPhotos are the photos m cites (all four sections, and the overview's bullets and conclusions, which cite only
// photos an item cites too); empty without minutes.
func minutesPhotos(m *Minutes) map[int]bool {
	out := map[int]bool{}
	if m == nil {
		return out
	}
	add := func(n int) {
		if n > 0 {
			out[n] = true
		}
	}
	for _, p := range m.Points {
		add(p.Photo)
	}
	for _, d := range m.Decisions {
		add(d.Photo)
	}
	for _, a := range m.Actions {
		add(a.Photo)
	}
	for _, o := range m.Open {
		add(o.Photo)
	}
	if o := m.Overview; o != nil {
		for _, c := range o.Conclusions {
			add(c.Photo)
		}
		for _, g := range o.Groups {
			for _, t := range g.Topics {
				for _, b := range t.Bullets {
					add(b.Photo)
				}
			}
		}
	}
	return out
}

// citedPhotos are the photos recording id's current minutes cite.
func (s *Store) citedPhotos(id string) map[int]bool {
	m, _ := s.ReadMinutes(id)
	return minutesPhotos(m)
}

// trimPhotosLocked keeps of recording r's photos only those its minutes cite, entry and file, for the trim: the others
// go with the transcript, the room (deleted too) is told of nothing more, and attachments/ keeps only the kept photos'
// files (no unlisted or temporary one). The owner's deletions stay, so a retry still gets ErrPhotoDeleted. photos.md
// is made what the kept book lists at every pass, also when the book needs no change: a trim cut short between
// photos.json and photos.md is finished by the next. Idempotent: changed when it removed or rewrote anything, rev when
// PartPhotos' revision moved. The caller holds recFilesMu.
func (s *Store) trimPhotosLocked(r *Recording, zone *time.Location) (changed, rev bool, err error) {
	b, exists, readErr := s.readPhotosLocked(r.ID)
	if readErr != nil {
		// Unreadable: no photo can be told cited, so none stays (nothing of the meeting outlives its transcript
		// unless the minutes need it).
		b, exists, rev = photoBook{Next: 1, Rev: 1}, true, true
	}
	if exists {
		cited := s.citedPhotos(r.ID)
		kept := []storedPhoto{}
		dirty := readErr != nil || len(b.Withdraw) > 0
		for _, p := range b.Photos {
			if !cited[p.N] {
				continue
			}
			dirty = dirty || p.Queued
			p.Queued = false
			kept = append(kept, p)
		}
		if len(kept) != len(b.Photos) {
			dirty, rev = true, true
			b.Rev++
		}
		if dirty {
			b.Photos, b.Withdraw = kept, nil
			if err := s.putPhotosLocked(r.ID, &b, r.StartedAt, zone); err != nil {
				return false, false, err
			}
			changed = true
		}
	}
	if !changed {
		md, err := s.putPhotosTextLocked(r.ID, &b, r.StartedAt, zone)
		if err != nil {
			return false, rev, err
		}
		changed = md
	}
	keep := map[string]bool{}
	for _, p := range b.Photos {
		keep[p.fileName()] = true
	}
	dir := s.path(KindRecording, r.ID, photosDir)
	info, err := os.Lstat(dir)
	switch {
	case os.IsNotExist(err):
		return changed, rev, nil
	case err != nil:
		return changed, rev, err
	case !info.IsDir() || len(keep) == 0:
		return true, rev, os.RemoveAll(dir)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return changed, rev, err
	}
	for _, f := range files {
		// Only a kept photo's regular file stays: anything else (attachments/audio/ above all) goes with the transcript.
		if keep[f.Name()] && f.Type().IsRegular() {
			continue
		}
		if err := os.RemoveAll(s.path(KindRecording, r.ID, photosDir, f.Name())); err != nil {
			return changed, rev, err
		}
		changed = true
	}
	if changed {
		syncRecordingDir(dir)
	}
	return changed, rev, nil
}
