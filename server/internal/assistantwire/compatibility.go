package assistantwire

import (
	"encoding/json"

	"github.com/nwverse/persona/server/internal/proto"
)

// Persist the pre-cutover enum so an older manager can resume the same bot threads (the Feishu chat's state). The
// typed reader canonicalizes only this exact enum, not user data.
type chatTargetJSON ChatTarget

func (t ChatTarget) MarshalJSON() ([]byte, error) {
	copy := chatTargetJSON(t)
	copy.Kind = proto.LegacyLauncherSource(copy.Kind)
	return json.Marshal(copy)
}

func (t *ChatTarget) UnmarshalJSON(raw []byte) error {
	var copy chatTargetJSON
	if err := json.Unmarshal(raw, &copy); err != nil {
		return err
	}
	copy.Kind = proto.CanonicalLauncherSource(copy.Kind)
	*t = ChatTarget(copy)
	return nil
}
