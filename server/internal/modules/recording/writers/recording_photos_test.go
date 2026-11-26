package writers

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// photoUpload is photo i (its id made of i) taken at seconds into the recording: a JPEG, its digest covering the body.
func photoUpload(i int, at float64, caption, text string) PhotoUpload {
	id := fmt.Sprintf("%08x-0000-4000-8000-000000000000", i)
	data := []byte(fmt.Sprintf("image bytes %d", i))
	return PhotoUpload{ID: recordingwire.PhotoID(id), At: at, Mime: PhotoJPEG, Data: data, Width: 1600, Height: 1200, Text: text, Caption: caption,
		Digest: fmt.Sprintf("%s|%v|%s|%s|%s", id, at, caption, text, data)}
}

func addPhoto(t *testing.T, e *testEngine, id string, u PhotoUpload, queue bool) Photo {
	t.Helper()
	p, created, err := e.PhotoAdd(id, u, queue)
	if err != nil || !created {
		t.Fatalf("add photo %s: %v (created %v)", u.ID, err, created)
	}
	return p
}

// photoMeeting stores a complete meeting recording of three lines, 10 seconds apart, that ended 50 seconds after start.
func photoMeeting(t *testing.T, e *testEngine, id string, start time.Time) {
	t.Helper()
	u := meetingUpload(1, start, "第一句", "第二句", "第三句")
	end := start.Add(50 * time.Second)
	u.EndedAt = &end
	putRecording(t, e, id, u, start.Add(time.Minute))
}

// photosTold counts MeetingChanged's PartPhotos per recording.
func photosTold(e *testEngine) map[string]int {
	told := map[string]int{}
	e.MeetingChanged = func(id string, parts []string) {
		if slices.Contains(parts, PartPhotos) {
			told[id]++
		}
	}
	return told
}

// readFile is the text of a file of recording id.
func readFile(t *testing.T, e *testEngine, id, name string) string {
	t.Helper()
	raw, err := os.ReadFile(e.Store.Path(KindRecording, id, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Photos are stored and listed oldest first, numbered from 1, their time clamped into the recording; a retry is the
// photo stored, another body under its id refused; the image, photos.json (no cited in it) and photos.md (a line
// per photo, by its words) are in the recording's directory.
func TestPhotosStoredAndListed(t *testing.T) {
	e := goldenEngine(t, nil)
	told := photosTold(e)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	photoMeeting(t, e, recID, start)
	if ps, rev, err := e.Photos(recID); err != nil || ps == nil || len(ps) != 0 || rev != 0 || e.PhotosRev(recID) != 0 {
		t.Fatalf("none yet: %v %d %v", ps, rev, err)
	}
	a := addPhoto(t, e, recID, photoUpload(1, 12.5, "白板", "Q3 预算\n  第二行"), false)
	png := photoUpload(2, 999, "", "")
	png.Mime = PhotoPNG
	b := addPhoto(t, e, strings.ToUpper(recID), png, true)
	c := addPhoto(t, e, recID, photoUpload(3, -5, "", ""), false)
	if a.N != 1 || a.At != 12.5 || b.N != 2 || b.At != 50 || c.N != 3 || c.At != 0 {
		t.Fatalf("numbers and times: %+v %+v %+v", a, b, c)
	}
	if a.ID != photoUpload(1, 0, "", "").ID || a.Mime != PhotoJPEG || b.Mime != PhotoPNG || a.Width != 1600 || a.Height != 1200 ||
		a.Text != "Q3 预算\n  第二行" || a.Caption != "白板" || a.Cited || a.Forwarded || a.AddedAt.IsZero() {
		t.Fatalf("photo %+v", a)
	}
	if told[recID] != 3 {
		t.Fatalf("told %d times, want 3", told[recID])
	}

	// A retry: the stored photo, nothing told; another body under the id is refused.
	again, created, err := e.PhotoAdd(recID, photoUpload(1, 12.5, "白板", "Q3 预算\n  第二行"), false)
	if err != nil || created || jsonOf(t, again) != jsonOf(t, a) || told[recID] != 3 {
		t.Fatalf("retry: %+v %v %v, told %d", again, created, err, told[recID])
	}
	if _, _, err := e.PhotoAdd(recID, photoUpload(1, 12.5, "白板", "改过的"), false); !errors.Is(err, ErrOpIDReused) {
		t.Fatalf("another body: %v", err)
	}

	ps, rev, err := e.Photos(recID)
	if err != nil || rev != 3 || e.PhotosRev(recID) != 3 || jsonOf(t, ps) != jsonOf(t, []Photo{a, b, c}) {
		t.Fatalf("list %+v rev %d %v", ps, rev, err)
	}
	if raw := jsonOf(t, a); !strings.Contains(raw, `"cited":false`) || !strings.Contains(raw, `"forwarded":false`) ||
		strings.Contains(raw, "digest") || strings.Contains(raw, "queued") || strings.Contains(raw, `"note"`) {
		t.Fatalf("wire photo %s", raw)
	}
	if mime, data, err := e.PhotoImage(recID, 2); err != nil || mime != PhotoPNG || string(data) != string(png.Data) {
		t.Fatalf("image: %q %q %v", mime, data, err)
	}
	if _, _, err := e.PhotoImage(recID, 4); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("no photo 4: %v", err)
	}
	for _, name := range []string{"p1.jpg", "p2.png", "p3.jpg"} {
		fi, err := os.Stat(e.Store.Path(KindRecording, recID, photosDir, name))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, fi, err)
		}
	}
	if names := recordingFileNames(t, e, recID); !slices.Contains(names, photosFile) || !slices.Contains(names, photosText) {
		t.Fatalf("files %v", names)
	}
	if book := readFile(t, e, recID, photosFile); strings.Contains(book, "cited") || !strings.Contains(book, `"digest"`) || !strings.Contains(book, `"queued": true`) {
		t.Fatalf("photos.json %s", book)
	}
	want := "[P1] 14:00:12 photo: 白板 · Q3 预算 第二行\n[P2] 14:00:50 photo:\n[P3] 14:00:00 photo:\n"
	if md := readFile(t, e, recID, photosText); md != want {
		t.Fatalf("photos.md %q, want %q", md, want)
	}
	if texts := e.PhotoTexts(recID); len(texts) != 3 || texts[0] != (PhotoText{N: 1, At: 12.5, Caption: "白板", Text: "Q3 预算\n  第二行"}) {
		t.Fatalf("texts %+v", texts)
	}

	// While it records, a photo's time stops at now.
	const live = "1a8fad5b-d9cb-469f-a165-70867728950e"
	liveStart := time.Now().Add(-time.Minute)
	putRecording(t, e, live, attendedUpload(1, liveStart, recordingwire.AttendanceListener), time.Now())
	p := addPhoto(t, e, live, photoUpload(4, 3600, "", ""), false)
	if p.At < 59 || p.At > time.Since(liveStart).Seconds() || p.N != 1 {
		t.Fatalf("recording now: %+v", p)
	}
	if _, _, err := e.PhotoAdd(live, PhotoUpload{ID: "x", Mime: "image/gif"}, false); !errors.Is(err, ErrBadPhoto) {
		t.Fatalf("bad photo: %v", err)
	}
}

// Deleting a photo: its file and entry go, its number stays used; again it is false, and its id sent again is
// refused. On a read-only knowledge base an unknown number is still not found, any other refused.
func TestPhotoDelete(t *testing.T) {
	e := goldenEngine(t, nil)
	told := photosTold(e)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	photoMeeting(t, e, recID, start)
	addPhoto(t, e, recID, photoUpload(1, 1, "第一张", ""), false)
	addPhoto(t, e, recID, photoUpload(2, 2, "第二张", ""), false)
	told[recID] = 0
	if ok, err := e.PhotoDelete(recID, 2); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, err := e.PhotoDelete(strings.ToUpper(recID), 2); ok || err != nil {
		t.Fatalf("again: %v %v, want false", ok, err)
	}
	if told[recID] != 1 {
		t.Fatalf("told %d times", told[recID])
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID, photosDir, "p2.jpg")); !os.IsNotExist(err) {
		t.Fatalf("the file stays: %v", err)
	}
	if _, _, err := e.PhotoImage(recID, 2); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("image of a deleted photo: %v", err)
	}
	if ps, rev, _ := e.Photos(recID); len(ps) != 1 || ps[0].N != 1 || rev != 3 {
		t.Fatalf("list %+v rev %d", ps, rev)
	}
	if md := readFile(t, e, recID, photosText); md != "[P1] 14:00:01 photo: 第一张\n" {
		t.Fatalf("photos.md %q", md)
	}
	if _, _, err := e.PhotoAdd(recID, photoUpload(2, 2, "第二张", ""), false); !errors.Is(err, ErrPhotoDeleted) {
		t.Fatalf("its id again: %v", err)
	}
	if p := addPhoto(t, e, recID, photoUpload(3, 3, "", ""), false); p.N != 3 {
		t.Fatalf("n %d, want 3: never reused", p.N)
	}
	for _, c := range []struct {
		id   string
		n    int
		want error
	}{
		{recID, 9, ErrPhotoNotFound},
		{"4b8fad5b-d9cb-469f-a165-70867728950e", 1, ErrNotFound},
	} {
		if _, err := e.PhotoDelete(c.id, c.n); !errors.Is(err, c.want) {
			t.Errorf("%s %d: %v, want %v", c.id, c.n, err, c.want)
		}
	}

	ro, err := kb.OpenReadOnly(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	roe := newTestEngine(&kb.Engine{Store: ro})
	for n, want := range map[int]error{9: ErrPhotoNotFound, 1: ErrReadOnly, 2: ErrReadOnly} {
		if _, err := roe.PhotoDelete(recID, n); !errors.Is(err, want) {
			t.Errorf("read-only, photo %d: %v, want %v", n, err, want)
		}
	}
	// The reads work there.
	if ps, rev, err := roe.Photos(recID); err != nil || len(ps) != 2 || rev != 4 || roe.PhotosRev(recID) != 4 {
		t.Fatalf("read-only list %+v %d %v", ps, rev, err)
	}
	if mime, data, err := roe.PhotoImage(recID, 1); err != nil || mime != PhotoJPEG || string(data) != "image bytes 1" {
		t.Fatalf("read-only image %q %v", data, err)
	}
	if texts := roe.PhotoTexts(recID); len(texts) != 2 || texts[0].Caption != "第一张" {
		t.Fatalf("read-only texts %+v", texts)
	}

	// The recording's delete takes them all.
	if _, err := e.DeleteRecording(recID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID)); !os.IsNotExist(err) {
		t.Fatalf("files left: %v", err)
	}
	if _, _, err := e.Photos(recID); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("list after the delete: %v", err)
	}
	if _, _, err := e.PhotoImage(recID, 1); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("image after the delete: %v", err)
	}
	if _, err := e.PhotoDelete(recID, 1); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("delete after the delete: %v", err)
	}
}

// A photo's words are searched with the recording, in photos.md: never photos.json's bookkeeping, never a file in
// attachments/.
func TestPhotosSearched(t *testing.T) {
	e := goldenEngine(t, nil)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	photoMeeting(t, e, recID, start)
	u := photoUpload(1, 5, "白板", "zebrawhiteboard plan")
	u.Digest = "digestonlyword"
	addPhoto(t, e, recID, u, false)
	if err := os.WriteFile(e.Store.Path(KindRecording, recID, photosDir, "notes.md"), []byte("attachmentword\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	md := "recordings/" + recID + "/" + photosText
	if hits := e.Search("zebrawhiteboard", false, 40); len(hits) != 1 || hits[0].Path != md {
		t.Fatalf("search %+v", hits)
	}
	if hits := e.SearchKind("zebrawhiteboard", KindRecording, false, 40); len(hits) != 1 || hits[0].Path != md {
		t.Fatalf("search recordings %+v", hits)
	}
	if found := e.Find("zebrawhiteboard", 10); len(found) != 1 || found[0].Entry.ID != recID || found[0].Hits[0].Path != md {
		t.Fatalf("find %+v", found)
	}
	if ms := e.Matches(KindRecording, recID, "zebrawhiteboard", 10); len(ms) != 1 || ms[0].Path != md || !strings.Contains(ms[0].Line, "[P1]") {
		t.Fatalf("matches %+v", ms)
	}
	for _, word := range []string{"digestonlyword", "attachmentword"} {
		if hits := e.Search(word, true, 40); len(hits) != 0 {
			t.Errorf("%s found: %+v", word, hits)
		}
		if found := e.Find(word, 10); len(found) != 0 {
			t.Errorf("%s found: %+v", word, found)
		}
	}
	// Deleted: no longer found, and photos.md goes with the last photo.
	if _, err := e.PhotoDelete(recID, 1); err != nil {
		t.Fatal(err)
	}
	if hits := e.Search("zebrawhiteboard", false, 40); len(hits) != 0 {
		t.Fatalf("found after the delete: %+v", hits)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID, photosText)); !os.IsNotExist(err) {
		t.Fatalf("photos.md without photos: %v", err)
	}
}

// What the room is to get: the photos queued for it until they went in, the room's note only on a photo that went in,
// a withdrawal for one deleted after it went in (or while it was being sent), and none for one that never did.
func TestPhotosRoomBookkeeping(t *testing.T) {
	e := goldenEngine(t, nil)
	told := photosTold(e)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	photoMeeting(t, e, recID, start)
	a := addPhoto(t, e, recID, photoUpload(1, 5, "", "预算"), true)
	addPhoto(t, e, recID, photoUpload(2, 6, "", ""), false)
	if queued, withdraw := e.PhotosForRoom(recID); len(queued) != 1 || queued[0].ID != a.ID || len(withdraw) != 0 {
		t.Fatalf("queued %+v withdraw %+v", queued, withdraw)
	}
	if err := e.PhotoNote(recID, 1, "a note"); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("note before it went in: %v", err)
	}
	rev := e.PhotosRev(recID)
	// Being sent: forwarded (its revision moves), still queued until the send went.
	for range 2 {
		if err := e.PhotoSending(recID, 1); err != nil {
			t.Fatal(err)
		}
	}
	if queued, _ := e.PhotosForRoom(recID); len(queued) != 1 || !queued[0].Forwarded || e.PhotosRev(recID) != rev+1 {
		t.Fatalf("sending: queued %+v rev %d", queued, e.PhotosRev(recID))
	}
	if err := e.PhotoForwarded(recID, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.PhotoForwarded(recID, 1); err != nil {
		t.Fatal(err)
	}
	if queued, _ := e.PhotosForRoom(recID); len(queued) != 0 || e.PhotosRev(recID) != rev+1 {
		t.Fatalf("forwarded: queued %+v rev %d", queued, e.PhotosRev(recID))
	}
	for _, err := range []error{e.PhotoForwarded(recID, 7), e.PhotoSending(recID, 7)} {
		if !errors.Is(err, ErrPhotoNotFound) {
			t.Fatalf("no photo 7: %v", err)
		}
	}
	told[recID] = 0
	if err := e.PhotoNote(recID, 1, "  白板上写着 Q3 预算  "); err != nil {
		t.Fatal(err)
	}
	if ps, rev2, _ := e.Photos(recID); !ps[0].Forwarded || ps[0].Note != "白板上写着 Q3 预算" || rev2 != rev+2 || told[recID] != 1 {
		t.Fatalf("note: %+v rev %d told %d", ps[0], rev2, told[recID])
	}
	if md := readFile(t, e, recID, photosText); !strings.HasPrefix(md, "[P1] 14:00:05 photo: 预算 · 白板上写着 Q3 预算\n") {
		t.Fatalf("photos.md %q", md)
	}
	for n, text := range map[int]string{2: "not forwarded", 9: "no such photo"} {
		if err := e.PhotoNote(recID, n, text); !errors.Is(err, ErrPhotoNotFound) {
			t.Errorf("note on %d: %v", n, err)
		}
	}
	for _, text := range []string{" ", strings.Repeat("长", MaxPhotoNote+1)} {
		if err := e.PhotoNote(recID, 1, text); !errors.Is(err, ErrBadPhoto) {
			t.Errorf("note of %d runes: %v", len([]rune(text)), err)
		}
	}

	// Deleted after it went in: withdrawn there, once.
	if _, err := e.PhotoDelete(recID, 1); err != nil {
		t.Fatal(err)
	}
	if _, withdraw := e.PhotosForRoom(recID); len(withdraw) != 1 || withdraw[0] != (PhotoRef{N: 1, ID: a.ID}) {
		t.Fatalf("withdraw %+v", withdraw)
	}
	rev = e.PhotosRev(recID)
	told[recID] = 0
	if err := e.PhotoWithdrawn(recID, 1); err != nil {
		t.Fatal(err)
	}
	if _, withdraw := e.PhotosForRoom(recID); len(withdraw) != 0 || e.PhotosRev(recID) != rev || told[recID] != 0 {
		t.Fatalf("withdrawn: %+v rev %d told %d", withdraw, e.PhotosRev(recID), told[recID])
	}
	if err := e.PhotoNote(recID, 1, "too late"); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("note on a deleted photo: %v", err)
	}

	// Deleted while it was being sent (its answer lost, or not there yet): withdrawn too, and the room may note it
	// meanwhile. Deleted before it was sent: never sent, never withdrawn.
	c := addPhoto(t, e, recID, photoUpload(3, 7, "", ""), true)
	if err := e.PhotoSending(recID, 3); err != nil {
		t.Fatal(err)
	}
	if err := e.PhotoNote(recID, 3, "noted while in doubt"); err != nil {
		t.Fatalf("note on a photo being sent: %v", err)
	}
	if _, err := e.PhotoDelete(recID, 3); err != nil {
		t.Fatal(err)
	}
	if queued, withdraw := e.PhotosForRoom(recID); len(queued) != 0 || len(withdraw) != 1 || withdraw[0] != (PhotoRef{N: 3, ID: c.ID}) {
		t.Fatalf("deleted while it was sent: %+v %+v", queued, withdraw)
	}
	if err := e.PhotoForwarded(recID, 3); err != nil {
		t.Fatal(err)
	}
	d := addPhoto(t, e, recID, photoUpload(4, 8, "", ""), true)
	if _, err := e.PhotoDelete(recID, d.N); err != nil {
		t.Fatal(err)
	}
	if err := e.PhotoSending(recID, d.N); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("sending a deleted photo: %v", err)
	}
	if _, err := e.PhotoDelete(recID, 2); err != nil {
		t.Fatal(err)
	}
	if _, withdraw := e.PhotosForRoom(recID); len(withdraw) != 1 || withdraw[0] != (PhotoRef{N: 3, ID: c.ID}) {
		t.Fatalf("withdraw %+v", withdraw)
	}
	for _, err := range []error{e.PhotoForwarded("4b8fad5b-d9cb-469f-a165-70867728950e", 1), e.PhotoNote("x", 1, "n")} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown recording: %v", err)
		}
	}
}

// The trim keeps the photos the minutes cite, entry and file, and nothing else of attachments/; the owner's deletions
// stay refused. Again it changes nothing; a cited photo deleted then makes no minutes and withdraws nothing.
func TestPhotosTrimKeepsCited(t *testing.T) {
	e := goldenEngine(t, trimModel{t})
	told := photosTold(e)
	e.RecordingDaysFunc = func() int { return 14 }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	photoMeeting(t, e, recID, start)
	addPhoto(t, e, recID, photoUpload(1, 5, "白板", "keptword"), false)
	addPhoto(t, e, recID, photoUpload(2, 10, "", "goneword"), true)
	png := photoUpload(3, 20, "提纲", "")
	png.Mime = PhotoPNG
	addPhoto(t, e, recID, png, false)
	addPhoto(t, e, recID, photoUpload(4, 30, "", ""), false)
	if err := e.PhotoForwarded(recID, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PhotoDelete(recID, 4); err != nil {
		t.Fatal(err)
	}
	for _, debris := range []string{"p9.jpg", "p2.jpg.tmp"} {
		if err := os.WriteFile(e.Store.Path(KindRecording, recID, photosDir, debris), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// With a summary card (#6563): its bullet and its conclusion cite photo 1 as the point does.
	card := &MinutesOverview{Conclusions: []MinutesConclusion{{Text: "照白板办", At: 5, Photo: 1}},
		Groups: []MinutesGroup{{Topics: []MinutesTopic{{Title: "白板", Icon: "design", Bullets: []MinutesBullet{{Text: "白板", At: 5, Photo: 1}}}}}}}
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Revision: 1, Title: "Design review", When: "w",
		Points: []MinutesPoint{{Text: "白板", At: 5, Photo: 1}}, Actions: []MinutesAction{{ID: "a1", What: "提纲", At: 20, Photo: 3}},
		Overview: card, Markdown: "# Design review"})
	rev := e.PhotosRev(recID)
	asks := recordingNow(t, e, recID).MinutesAsks
	told[recID] = 0

	if n := e.PruneRecordings(now); n != 0 {
		t.Fatalf("pruned %d", n)
	}
	if names := recordingFileNames(t, e, recID); !slices.Equal(names, []string{photosDir, "meta.json", "minutes.json", "minutes.md", photosFile, photosText}) {
		t.Fatalf("a trimmed recording keeps %v", names)
	}
	files, _ := os.ReadDir(e.Store.Path(KindRecording, recID, photosDir))
	if len(files) != 2 || files[0].Name() != "p1.jpg" || files[1].Name() != "p3.png" {
		t.Fatalf("attachments %v", files)
	}
	ps, rev2, err := e.Photos(recID)
	if err != nil || len(ps) != 2 || ps[0].N != 1 || ps[1].N != 3 || !ps[0].Cited || !ps[1].Cited || rev2 != rev+1 || told[recID] != 1 {
		t.Fatalf("kept %+v rev %d (was %d) told %d %v", ps, rev2, rev, told[recID], err)
	}
	if queued, withdraw := e.PhotosForRoom(recID); len(queued) != 0 || len(withdraw) != 0 {
		t.Fatalf("for the room %+v %+v", queued, withdraw)
	}
	if md := readFile(t, e, recID, photosText); md != "[P1] 12:00:05 photo: 白板 · keptword\n[P3] 12:00:20 photo: 提纲\n" {
		t.Fatalf("photos.md %q", md)
	}
	if hits := e.Search("goneword", false, 10); len(hits) != 0 {
		t.Fatalf("a photo the trim removed is found: %+v", hits)
	}
	if hits := e.Search("keptword", false, 10); len(hits) != 1 {
		t.Fatalf("a kept photo: %+v", hits)
	}
	if _, _, err := e.PhotoImage(recID, 2); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("image of a removed photo: %v", err)
	}
	if _, err := e.PhotoDelete(recID, 2); !errors.Is(err, ErrPhotoNotFound) {
		t.Fatalf("delete of a removed photo: %v", err)
	}
	if _, _, err := e.PhotoAdd(recID, photoUpload(4, 30, "", ""), false); !errors.Is(err, ErrPhotoDeleted) {
		t.Fatalf("a deleted photo sent again: %v", err)
	}
	if _, _, err := e.PhotoAdd(recID, photoUpload(2, 10, "", "goneword"), false); !errors.Is(err, ErrTranscriptExpired) {
		t.Fatalf("a removed photo sent again: %v", err)
	}

	// Again: nothing to do.
	told[recID] = 0
	e.PruneRecordings(now.Add(time.Hour))
	if e.PhotosRev(recID) != rev+1 || told[recID] != 0 {
		t.Fatalf("again: rev %d told %d", e.PhotosRev(recID), told[recID])
	}

	// A cited photo deleted: no minutes made, no withdrawal; the minutes keep citing it.
	if ok, err := e.PhotoDelete(recID, 1); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if r := recordingNow(t, e, recID); r.MinutesAsks != asks || r.MinutesState != MinutesReady {
		t.Fatalf("minutes asked again: %d %q", r.MinutesAsks, r.MinutesState)
	}
	if _, withdraw := e.PhotosForRoom(recID); len(withdraw) != 0 {
		t.Fatalf("withdraw %+v", withdraw)
	}
	// The summary card too: on a trimmed recording the minutes stay as they are, the overview included.
	if m, _ := e.Store.ReadMinutes(recID); m.Points[0].Photo != 1 || !jsonEqual[MinutesOverview](t, m.Overview, card) || m.Version != 1 {
		t.Fatalf("minutes %+v", m)
	}

	// Past minutes_days the rest goes with the recording.
	e.MinutesDaysFunc = func() int { return 30 }
	if n := e.PruneRecordings(now.AddDate(0, 0, 30)); n != 1 {
		t.Fatalf("at minutes_days: pruned %d", n)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID)); !os.IsNotExist(err) {
		t.Fatalf("files left: %v", err)
	}
}

// A trim cut short (meta.json written, nothing more) finishes the photos at the next pass; an unreadable photos.json
// keeps none.
func TestPhotosTrimFinished(t *testing.T) {
	e := goldenEngine(t, nil)
	days := 14
	e.RecordingDaysFunc = func() int { return days }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	const broken = "1c8fad5b-d9cb-469f-a165-70867728950e"
	for _, id := range []string{recID, broken} {
		photoMeeting(t, e, id, start)
		addPhoto(t, e, id, photoUpload(1, 5, "", "kept"), false)
		addPhoto(t, e, id, photoUpload(2, 6, "", "gone"), false)
		putMeetingMinutes(t, e, id, Minutes{Version: 1, Revision: 1, Title: "t", When: "w", Points: []MinutesPoint{{Text: "x", At: 5, Photo: 1}}})
	}
	if err := os.WriteFile(e.Store.Path(KindRecording, broken, photosFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	markTrimmed(t, e, recID, now)
	days = 60
	e.PruneRecordings(now)
	if ps, _, _ := e.Photos(recID); len(ps) != 1 || ps[0].N != 1 {
		t.Fatalf("finished: %+v", ps)
	}
	if files, _ := os.ReadDir(e.Store.Path(KindRecording, recID, photosDir)); len(files) != 1 || files[0].Name() != "p1.jpg" {
		t.Fatalf("attachments %v", files)
	}
	days = 14
	e.PruneRecordings(now)
	if _, err := os.Stat(e.Store.Path(KindRecording, broken, photosDir)); !os.IsNotExist(err) {
		t.Fatalf("an unreadable photos.json kept the files: %v", err)
	}
	if ps, rev, err := e.Photos(broken); err != nil || len(ps) != 0 || rev == 0 {
		t.Fatalf("after an unreadable photos.json: %+v %d %v", ps, rev, err)
	}
}

// The answering model's material has the photos' words (those that have any), never a deleted photo's, the latest
// within its bound.
func TestMeetingMaterialPhotos(t *testing.T) {
	e := goldenEngine(t, nil)
	photoMeeting(t, e, recID, time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone))
	addPhoto(t, e, recID, photoUpload(1, 5, "白板", "预算"), false)
	addPhoto(t, e, recID, photoUpload(2, 6, "", ""), false)
	addPhoto(t, e, recID, photoUpload(3, 7, "", "SECRET-DELETED-PHOTO"), false)
	if _, err := e.PhotoDelete(recID, 3); err != nil {
		t.Fatal(err)
	}
	m, err := e.MeetingMaterial(recID, "预算多少")
	if err != nil || len(m.Photos) != 1 || m.Photos[0] != (PhotoText{N: 1, At: 5, Caption: "白板", Text: "预算"}) {
		t.Fatalf("material photos %+v %v", m.Photos, err)
	}
}

// kb_get's photos: photos.md as the searches read it, by the recording's id in any case; nothing without photos.
func TestPhotosMarkdown(t *testing.T) {
	e := goldenEngine(t, nil)
	photoMeeting(t, e, recID, time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone))
	if got := e.PhotosMarkdown(recID); got != "" {
		t.Fatalf("no photos: %q", got)
	}
	addPhoto(t, e, recID, photoUpload(1, 5, "白板", "预算"), false)
	if got := e.PhotosMarkdown(strings.ToUpper(recID)); got != "[P1] 14:00:05 photo: 白板 · 预算\n" {
		t.Fatalf("photos.md %q", got)
	}
	if got := e.PhotosMarkdown("not-a-recording"); got != "" {
		t.Fatalf("unknown recording: %q", got)
	}
}

// A trim cut short between photos.json and photos.md (photos.md.tmp in the way) is finished by the next pass, though
// the book needs no change then: photos.md has only the kept photo, and the searches no other. A book left with no
// photo (a crash right after photos.json) leaves no photos.md either.
func TestPhotosTrimProjectionFinished(t *testing.T) {
	e := goldenEngine(t, nil)
	e.RecordingDaysFunc = func() int { return 14 }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	const bare = "2c8fad5b-d9cb-469f-a165-70867728950e"
	for _, id := range []string{recID, bare} {
		photoMeeting(t, e, id, now.AddDate(0, 0, -20))
		addPhoto(t, e, id, photoUpload(1, 5, "", "keptword"), false)
		addPhoto(t, e, id, photoUpload(2, 6, "", "trimcanary"), false)
	}
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Revision: 1, Title: "t", When: "w", Points: []MinutesPoint{{Text: "x", At: 5, Photo: 1}}})
	putMeetingMinutes(t, e, bare, Minutes{Version: 1, Revision: 1, Title: "t", When: "w", Points: []MinutesPoint{{Text: "x", At: 0}}})
	obstacle := e.Store.Path(KindRecording, recID, photosText+".tmp")
	if err := os.Mkdir(obstacle, 0o700); err != nil {
		t.Fatal(err)
	}
	markTrimmed(t, e, bare, now)
	st := ws(e.Store)
	b, err := st.readPhotos(bare)
	if err == nil {
		b.Photos, b.Rev = nil, b.Rev+1
		st.recFilesMu.Lock()
		err = st.writeRecordingFile(e.Store.Path(KindRecording, bare, photosFile), []byte(jsonOf(t, b)))
		st.recFilesMu.Unlock()
	}
	if err != nil {
		t.Fatal(err)
	}

	e.PruneRecordings(now)
	if ps, _, _ := e.Photos(recID); len(ps) != 1 || !strings.Contains(readFile(t, e, recID, photosText), "trimcanary") {
		t.Fatalf("not cut short: %+v", ps)
	}
	if err := os.Remove(obstacle); err != nil {
		t.Fatal(err)
	}
	e.PruneRecordings(now.Add(time.Hour))
	if md := readFile(t, e, recID, photosText); md != "[P1] 12:00:05 photo: keptword\n" {
		t.Fatalf("photos.md %q", md)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, bare, photosText)); !os.IsNotExist(err) {
		t.Fatalf("photos.md of a book with no photo: %v", err)
	}
	if got := e.PhotosMarkdown(bare); got != "" {
		t.Fatalf("kb_get's photos of a book with no photo: %q", got)
	}
	if hits := e.Search("trimcanary", false, 10); len(hits) != 0 {
		t.Fatalf("a photo the trim removed is found: %+v", hits)
	}
	if hits := e.Search("keptword", false, 10); len(hits) != 1 {
		t.Fatalf("the kept photo: %+v", hits)
	}
}

// A delete that stopped between photos.json and photos.md (here: photos.md can't be replaced) is finished by its
// repeat: photos.md loses the photo before the repeat answers, and the image goes.
func TestPhotoDeleteRepeatFinishesPhotosMarkdown(t *testing.T) {
	e := goldenEngine(t, nil)
	photoMeeting(t, e, recID, time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone))
	addPhoto(t, e, recID, photoUpload(1, 1, "", "deletedphotocanary"), false)
	addPhoto(t, e, recID, photoUpload(2, 2, "", "kept"), false)
	obstacle := e.Store.Path(KindRecording, recID, photosText+".tmp")
	if err := os.Mkdir(obstacle, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PhotoDelete(recID, 1); err == nil {
		t.Fatal("a delete that could not write photos.md answered")
	}
	if err := os.Remove(obstacle); err != nil {
		t.Fatal(err)
	}
	if deleted, err := e.PhotoDelete(recID, 1); deleted || err != nil {
		t.Fatalf("repeat: %v %v", deleted, err)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID, photosDir, "p1.jpg")); !os.IsNotExist(err) {
		t.Fatalf("image kept: %v", err)
	}
	if got := e.PhotosMarkdown(recID); strings.Contains(got, "deletedphotocanary") || !strings.Contains(got, "kept") {
		t.Errorf("photos.md after the repeat: %q", got)
	}
	if hits := e.Search("deletedphotocanary", false, 10); len(hits) > 0 {
		t.Errorf("still found: %+v", hits)
	}
}
