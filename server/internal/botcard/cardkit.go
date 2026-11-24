package botcard

import "encoding/json"

// CardEntityContent is an interactive message's content that sends card entity id.
func CardEntityContent(id string) string {
	b, _ := json.Marshal(map[string]any{"type": "card", "data": map[string]any{"card_id": id}})
	return string(b)
}
