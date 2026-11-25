package recordingwire

import (
	"errors"
	"time"
)

// Photo is a meeting photo as the apps read it (GET …/photos): Cited follows the current minutes, Forwarded says the
// hub sent it into the room (deleting it withdraws it there).
type Photo struct {
	N         int       `json:"n"`
	ID        string    `json:"id"`
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
	ID            string
	At            float64
	Mime          string
	Data          []byte
	Width, Height int
	Text, Caption string
	Digest        string
}

// PhotoRef names a photo: its number and the phone's id.
type PhotoRef struct {
	N  int    `json:"n"`
	ID string `json:"id"`
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
