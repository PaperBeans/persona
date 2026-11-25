package extension

import (
	"encoding/json"
	"fmt"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// TrackKindItem is a track kind an extension contributes. The assistant owns
// execution, directories and information policy; an extension never changes
// those by supplying executable configuration.
type TrackKindItem struct {
	ID     string                        `json:"id"`
	Title  string                        `json:"title"`
	Config assistantwire.TrackKindConfig `json:"config"`
}

// TrackKinds are the manifest's declarative track kinds.
func (m *Manifest) TrackKinds() []TrackKindItem {
	var out []TrackKindItem
	_ = json.Unmarshal(m.Contributes["track_kinds"], &out)
	return out
}

func init() {
	Points["track_kinds"] = Point{Name: "track_kinds", Namespace: "track_kinds", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var item TrackKindItem
		if err := strict(raw, &item); err != nil {
			return "", err
		}
		if !idPattern.MatchString(item.ID) {
			return item.ID, fmt.Errorf("track kind id %q: a-z0-9_-", item.ID)
		}
		if item.Title == "" {
			return item.ID, fmt.Errorf("track kind %s: a title", item.ID)
		}
		if item.Config.Schema != assistantwire.TrackKindSchema {
			return item.ID, fmt.Errorf("track kind %s: config schema %d", item.ID, assistantwire.TrackKindSchema)
		}
		return item.ID, nil
	}}
}
