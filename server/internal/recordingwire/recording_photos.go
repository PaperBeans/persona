package recordingwire

import (
	"errors"
	"slices"
	"strconv"
	"time"
)

// PhotoID is a photo's id: the phone's, a lowercase UUID, the same in every retry of its upload. It is stored and sent
// as the text it is.
type PhotoID string

// Photo is a meeting photo as the apps read it (GET …/photos): Cited follows the current minutes, Forwarded says the
// hub sent it into the room (deleting it withdraws it there).
type Photo struct {
	N         int       `json:"n"`
	ID        PhotoID   `json:"id"`
	At        float64   `json:"at"`
	Mime      string    `json:"mime"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Text      string    `json:"text,omitempty"`
	Caption   string    `json:"caption,omitempty"`
	Note      string    `json:"note,omitempty"`
	Cited     bool      `json:"cited"`
	Forwarded bool      `json:"forwarded"`
	AddedAt   time.Time `json:"added_at"`
}

// PhotoUpload is a photo to add, as the HTTP layer checked it: ID the phone's (a UUID, lowercase), At seconds from the
// recording's start (clamped into it here), Data the decoded image of Mime, Width × Height pixels, Text and Caption
// trimmed ("" when blank), Digest of the request's body, so that a retry with the same body is the same photo.
type PhotoUpload struct {
	ID            PhotoID
	At            float64
	Mime          string
	Data          []byte
	Width, Height int
	Text, Caption string
	Digest        string
}

// PhotoRef names a photo: its number and the phone's id.
type PhotoRef struct {
	N  int     `json:"n"`
	ID PhotoID `json:"id"`
}

// PhotoText is a listed photo's words, all the hub's models read of it: the owner's caption, the phone's recognized
// text and the room's note, each "" when there is none.
type PhotoText struct {
	N                   int
	At                  float64
	Caption, Text, Note string
}

// Limits of meeting photos (the HTTP layer answers 400 past the text ones; PhotoAdd refuses them too).
const (
	MaxPhotos       = 50   // listed at a time
	MaxPhotoText    = 8000 // runes of a photo's recognized text
	MaxPhotoCaption = 200  // runes of its caption
	MaxPhotoNote    = 1000 // runes of the room's note
)

// Photo mime types.
const (
	PhotoJPEG = "image/jpeg"
	PhotoPNG  = "image/png"
)

var (
	// ErrPhotoNotFound: no photo of that number is listed (there never was one, the owner deleted it, or a trim
	// removed it).
	ErrPhotoNotFound = errors.New("no such photo")
	// ErrPhotoDeleted: the owner deleted the photo with that id; a retry never brings it back.
	ErrPhotoDeleted = errors.New("this photo was deleted")
	// ErrPhotosFull: the recording has MaxPhotos photos.
	ErrPhotosFull = errors.New("this meeting has as many photos as it can keep")
	// ErrOpIDReused: a photo was added under that id with another body.
	ErrOpIDReused = errors.New("this id was used for another photo")
	// ErrBadPhoto wraps what is wrong with a photo to add.
	ErrBadPhoto = errors.New("invalid photo")
)

// PhotoBook is photos.json.
type PhotoBook struct {
	Next   int           `json:"next"` // the next n; n is never reused
	Rev    int64         `json:"rev"`  // PartPhotos' revision: only goes up
	Photos []StoredPhoto `json:"photos"`
	// Deleted are the photos the owner deleted: deleting one again answers false, adding its id again ErrPhotoDeleted.
	Deleted []PhotoRef `json:"deleted,omitempty"`
	// Withdraw are the photos deleted after they went into the room, which the room is still to be told of.
	Withdraw []PhotoRef `json:"withdraw,omitempty"`
}

// Index is the position of the listed photo n, -1 when none is.
func (b *PhotoBook) Index(n int) int {
	return slices.IndexFunc(b.Photos, func(p StoredPhoto) bool { return p.N == n })
}

// StoredPhoto is a photo in photos.json: Photo without Cited (the minutes say that), with the request's digest and
// Queued while it waits to go into the room (until its send succeeded: Queued and Forwarded is a send in doubt).
type StoredPhoto struct {
	N         int       `json:"n"`
	ID        PhotoID   `json:"id"`
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

// Photo is p as the apps read it, cited as the minutes say.
func (p StoredPhoto) Photo(cited map[int]bool) Photo {
	return Photo{N: p.N, ID: p.ID, At: p.At, Mime: p.Mime, Width: p.Width, Height: p.Height, Text: p.Text, Caption: p.Caption,
		Note: p.Note, Cited: cited[p.N], Forwarded: p.Forwarded, AddedAt: p.AddedAt}
}

// Words is p's words.
func (p StoredPhoto) Words() PhotoText {
	return PhotoText{N: p.N, At: p.At, Caption: p.Caption, Text: p.Text, Note: p.Note}
}

// FileName is the photo's file in attachments/.
func (p StoredPhoto) FileName() string {
	ext := ".jpg"
	if p.Mime == PhotoPNG {
		ext = ".png"
	}
	return "p" + strconv.Itoa(p.N) + ext
}
