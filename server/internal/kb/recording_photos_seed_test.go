package kb

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Photos for the tests of what the knowledge base does with stored photos (the search index, the trim, the readers).
// Adding and deleting a photo is the recording module's (internal/modules/recording/writers); these write what its
// intake leaves on disk through the knowledge base's own write primitives.

// photoUpload is photo i (its id made of i) taken at seconds into the recording: a JPEG, its digest covering the body.
func photoUpload(i int, at float64, caption, text string) PhotoUpload {
	id := fmt.Sprintf("%08x-0000-4000-8000-000000000000", i)
	data := []byte(fmt.Sprintf("image bytes %d", i))
	return PhotoUpload{ID: recordingwire.PhotoID(id), At: at, Mime: PhotoJPEG, Data: data, Width: 1600, Height: 1200, Text: text, Caption: caption,
		Digest: fmt.Sprintf("%s|%v|%s|%s|%s", id, at, caption, text, data)}
}

// photoMeeting stores a complete meeting recording of three lines, 10 seconds apart, that ended 50 seconds after start.
func photoMeeting(t *testing.T, e *Engine, id string, start time.Time) {
	t.Helper()
	u := meetingUpload(1, start, "第一句", "第二句", "第三句")
	end := start.Add(50 * time.Second)
	u.EndedAt = &end
	putRecording(t, e, id, u, start.Add(time.Minute))
}

// changePhotos rewrites recording id's photo book with fn applied, its revision moved, as the module's writers do
// under the same locks.
func changePhotos(t *testing.T, e *Engine, id string, fn func(b *photoBook, dir string)) {
	t.Helper()
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	r, ok := st.Recording(id)
	if !ok {
		t.Fatalf("no recording %s", id)
	}
	zone := st.Zone()
	st.recFilesMu.Lock()
	defer st.recFilesMu.Unlock()
	b, _, err := st.readPhotosLocked(id)
	if err != nil {
		t.Fatal(err)
	}
	dir := st.path(KindRecording, id, photosDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fn(&b, dir)
	b.Rev++
	if err := st.putPhotosLocked(id, &b, r.StartedAt, zone); err != nil {
		t.Fatal(err)
	}
}

// addPhoto lists photo u of recording id and writes its image.
func addPhoto(t *testing.T, e *Engine, id string, u PhotoUpload, queue bool) {
	t.Helper()
	changePhotos(t, e, id, func(b *photoBook, dir string) {
		p := recordingwire.StoredPhoto{N: b.Next, ID: u.ID, At: u.At, Mime: u.Mime, Width: u.Width, Height: u.Height, Text: u.Text,
			Caption: u.Caption, Queued: queue, AddedAt: time.Now().UTC().Truncate(time.Second), Digest: u.Digest}
		if err := writeRecordingFile(dir+"/"+p.FileName(), u.Data); err != nil {
			t.Fatal(err)
		}
		b.Photos = append(b.Photos, p)
		b.Next++
	})
}

// deletePhoto takes photo n of recording id out of its book and removes its image, and has the search index drop
// what was deleted on purpose, as the module's delete asks through the storage port (recordingwire.Writer.PurgeSearch).
func deletePhoto(t *testing.T, e *Engine, id string, n int) {
	t.Helper()
	changePhotos(t, e, id, func(b *photoBook, dir string) {
		i := b.Index(n)
		if i < 0 {
			t.Fatalf("no photo %d", n)
		}
		p := b.Photos[i]
		b.Photos = slices.Delete(b.Photos, i, i+1)
		b.Deleted = append(b.Deleted, PhotoRef{N: p.N, ID: p.ID})
		_ = os.Remove(dir + "/" + p.FileName())
	})
	RecordingCore(e).PurgeSearch()
}

// What the search reads of a recording's files the module's writers make: photos.md and meta.json, never the
// bookkeeping beside them (photos.json, meeting.json, the hub's own transcript and its temporary file).
func TestRecordingWritersFilesSearched(t *testing.T) {
	dir := "recordings/" + recID + "/"
	for _, name := range []string{photosText, "meta.json"} {
		if !searchable(dir + name) {
			t.Errorf("%s is not searched", name)
		}
	}
	for _, name := range []string{photosFile, meetingFile, hubSegmentsFile, hubSegmentsFile + ".tmp"} {
		if searchable(dir + name) {
			t.Errorf("%s is searched", name)
		}
	}
}
