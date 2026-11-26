package recordingwire

import (
	"encoding/json"
	"testing"
	"time"
)

// photos.json keeps its bytes: the photo book and its photos moved here from the knowledge base and a photo's id
// became PhotoID, a named text, which is written as the text it is. The expected file is the one the knowledge base
// wrote before the move (MarshalIndent, two spaces, a newline), every field set and the optional ones left out.
func TestPhotoBookKeepsItsStoredBytes(t *testing.T) {
	added := time.Date(2026, 11, 10, 6, 0, 12, 0, time.UTC)
	full := PhotoBook{Next: 4, Rev: 7,
		Photos: []StoredPhoto{
			{N: 1, ID: "00000001-0000-4000-8000-000000000000", At: 5, Mime: PhotoJPEG, Width: 1600, Height: 1200, Text: "budget",
				Caption: "whiteboard", Note: "the Q3 budget", Forwarded: true, Queued: true, AddedAt: added, Digest: "d1"},
			{N: 3, ID: "00000003-0000-4000-8000-000000000000", At: 20.5, Mime: PhotoPNG, Width: 800, Height: 600, AddedAt: added, Digest: "d3"}},
		Deleted:  []PhotoRef{{N: 2, ID: "00000002-0000-4000-8000-000000000000"}},
		Withdraw: []PhotoRef{{N: 2, ID: "00000002-0000-4000-8000-000000000000"}}}
	const want = `{
  "next": 4,
  "rev": 7,
  "photos": [
    {
      "n": 1,
      "id": "00000001-0000-4000-8000-000000000000",
      "at": 5,
      "mime": "image/jpeg",
      "width": 1600,
      "height": 1200,
      "text": "budget",
      "caption": "whiteboard",
      "note": "the Q3 budget",
      "forwarded": true,
      "queued": true,
      "added_at": "2026-11-10T06:00:12Z",
      "digest": "d1"
    },
    {
      "n": 3,
      "id": "00000003-0000-4000-8000-000000000000",
      "at": 20.5,
      "mime": "image/png",
      "width": 800,
      "height": 600,
      "added_at": "2026-11-10T06:00:12Z",
      "digest": "d3"
    }
  ],
  "deleted": [
    {
      "n": 2,
      "id": "00000002-0000-4000-8000-000000000000"
    }
  ],
  "withdraw": [
    {
      "n": 2,
      "id": "00000002-0000-4000-8000-000000000000"
    }
  ]
}
`
	raw, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw) + "\n"; got != want {
		t.Fatalf("photos.json changed:\n%s", got)
	}
	var back PhotoBook
	if err := json.Unmarshal([]byte(want), &back); err != nil {
		t.Fatal(err)
	}
	again, _ := json.MarshalIndent(back, "", "  ")
	if string(again)+"\n" != want {
		t.Fatalf("photos.json does not read back as written:\n%s", again)
	}
	if empty, _ := json.Marshal(PhotoBook{Next: 1, Photos: []StoredPhoto{}}); string(empty) != `{"next":1,"rev":0,"photos":[]}` {
		t.Fatalf("an empty book: %s", empty)
	}
	// What the apps read of a photo (GET …/photos) keeps its bytes too.
	if p, _ := json.Marshal(full.Photos[1].Photo(map[int]bool{3: true})); string(p) !=
		`{"n":3,"id":"00000003-0000-4000-8000-000000000000","at":20.5,"mime":"image/png","width":800,"height":600,"cited":true,"forwarded":false,"added_at":"2026-11-10T06:00:12Z"}` {
		t.Fatalf("a photo as the apps read it: %s", p)
	}
}
