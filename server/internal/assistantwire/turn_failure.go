package assistantwire

import (
	"strings"
	"unicode/utf8"
)

// AuthErrorWords are the words of a model service that refused a request because of sign-in, the key, billing, the
// certificate or the model picked: an error the user must fix, never worth a "Continue". The hub's agent triage
// counts them among its user errors (assistant/agents_work_turns.go userErrorWords); a turn whose whole answer is one
// of them failed (ErrorOnlyText, #4669). Lower case: matched against lower-cased text.
var AuthErrorWords = []string{"failed to authenticate", "oauth token has expired", "invalid api key", "not logged in", "please run /login",
	"issue with the selected model", "permission_error", "authentication_error", "credit balance", "billing",
	"ssl certificate", "self-signed certificate", "certificate verif"}

// upstreamErrorWords are a gateway's words for an upstream it could not reach. Only an answer that is nothing but
// them counts (ErrorOnlyText): a reply may talk about upstreams.
var upstreamErrorWords = []string{"no eligible upstream", "upstream connect error", "upstream request timeout"}

// errorOnlyMax is the longest answer ErrorOnlyText takes for an error line: Claude Code's own error lines are one
// short line ("Invalid API key · Fix external API key"); a longer answer is a reply that may mention one.
const errorOnlyMax = 300

// ErrorOnlyText: answer, the whole of what a turn said, is only a model-service error line (an auth error, a
// gateway's upstream error, an API error as Claude Code writes it), not a reply: the turn failed even when the
// runtime did not flag it (#4669). One short line only.
func ErrorOnlyText(answer string) bool {
	t := strings.TrimSpace(answer)
	if t == "" || strings.Contains(t, "\n") || utf8.RuneCountInString(t) > errorOnlyMax {
		return false
	}
	if CutOff(t) {
		return true
	}
	return AuthErrorText(t) || containsAny(strings.ToLower(t), upstreamErrorWords)
}

// AuthErrorText: text has one of AuthErrorWords.
func AuthErrorText(text string) bool {
	return containsAny(strings.ToLower(text), AuthErrorWords)
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
