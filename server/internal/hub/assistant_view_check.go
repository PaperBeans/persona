package hub

import (
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

func (m *Machine) checkAssistantView(params json.RawMessage) (assistantwire.ViewCheckResponse, error) {
	var result assistantwire.ViewCheckResponse
	var input assistantwire.ViewCheckRequest
	if err := json.Unmarshal(params, &input); err != nil {
		return result, err
	}
	session, known := m.session(input.SessionID)
	if !known || !m.isAssistant(session) || !wire.HasAssistantView(input.Message) {
		return result, nil
	}
	var problems []string
	if len(input.Message) > 1<<20 {
		problems = []string{"message: at most 1 MB for assistant card validation"}
	} else {
		problems = wire.AssistantBlockErrors(input.Message)
	}
	if len(problems) == 0 {
		return result, nil
	}
	bounded := make([]string, min(len(problems), 4))
	for i := range bounded {
		text := []rune(problems[i])
		bounded[i] = string(text[:min(len(text), 2000)])
	}
	result.Reason = prompts.RenderAssistantViewRepair(prompts.AssistantViewRepair{Errors: bounded})
	return result, nil
}
