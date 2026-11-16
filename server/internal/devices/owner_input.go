package devices

import (
	"encoding/json"

	"github.com/nwverse/persona/server/internal/devicewire"
)

// ownerInputParams marks a session.send the owner typed in their own app (owner_input): the node may hand it to a
// Claude outside Persona's terminals as the person's own input, through Persona's plugin there (node/ccplugin.go,
// docs/integrations/claude-integration.md "Owner input through the Persona plugin"). The mark comes only from the
// hub's human-input submission, which it attaches to a full-role device's own message (hub/sharing_caller.go: not a
// link or answer-only token, not the master token, not a member hub); any owner_input a caller put in the params is
// dropped, so no assistant, extension, channel or relay can claim it.
func ownerInputParams(p devicewire.NodeCall, params json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return nil, err
	}
	_, claimed := fields["owner_input"]
	delete(fields, "owner_input")
	owner := p.Submission != nil && p.Submission.Kind == "human_input" && p.Submission.ID != "" &&
		p.Caller != nil && p.Caller.Kind == "device"
	if owner {
		fields["owner_input"] = json.RawMessage("true")
	} else if !claimed {
		return params, nil
	}
	return json.Marshal(fields)
}
