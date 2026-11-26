package transcript

import (
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The node starts an assistant message with its <persona-view-errors> feedback after the snapshot when the last
// replies drew rich views that failed their check (node/assistant_context.go turnContext). It is for the model: the
// user's message must come out as the one item it always is, never with the feedback as a user item of its own.
func TestViewErrorsAreNotTheUsersWords(t *testing.T) {
	const feedback = "<persona-view-errors>\nThe view v1 failed its check.\n</persona-view-errors>"
	cases := map[string]string{
		"after the snapshot": `{"type":"user","uuid":"u1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[` +
			`{"type":"text","text":"<persona-context>\nnow: x\n</persona-context>\n` + jsonText(feedback) + `"},{"type":"text","text":"Add a bakery stop."}]}}`,
		"alone": `{"type":"user","uuid":"u1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[` +
			`{"type":"text","text":"` + jsonText(feedback) + `"},{"type":"text","text":"Add a bakery stop."}]}}`,
		"queued mid-turn": `{"type":"attachment","uuid":"u1","timestamp":"2026-01-02T03:04:05Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":[` +
			`{"type":"text","text":"<persona-context>\nnow: x\n</persona-context>\n` + jsonText(feedback) + `"},{"type":"text","text":"Add a bakery stop."}]}}`,
		"one string": `{"type":"user","uuid":"u1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` +
			jsonText(feedback) + `\nAdd a bakery stop."}}`,
	}
	for name, line := range cases {
		var meta Meta
		items := ParseLine(1, []byte(line), &meta)
		var texts []string
		for _, it := range items {
			if it.Role == "user" && it.Kind == proto.KindText {
				texts = append(texts, it.Text)
			}
		}
		if len(texts) != 1 || texts[0] != "Add a bakery stop." {
			t.Errorf("%s: user texts %q, want just the message", name, texts)
		}
	}
	if got, _ := codexUserPart(feedback + "\nAdd a bakery stop."); got != "Add a bakery stop." {
		t.Errorf("codex: %q", got)
	}
}

func jsonText(s string) string {
	out := ""
	for _, r := range s {
		if r == '\n' {
			out += `\n`
		} else {
			out += string(r)
		}
	}
	return out
}
