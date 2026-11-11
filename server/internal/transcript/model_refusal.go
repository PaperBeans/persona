package transcript

import (
	"bytes"

	"github.com/nwverse/persona/server/internal/proto"
)

// ObserveModelFallback follows only refusal fallback state in a transcript scan that otherwise skips ordinary
// conversation lines. Once a fallback is active, prompts and replies can clear it, so marker-only scans must read
// those lines too. The scratch parser keeps the same command, sidechain and turn rules without changing the scan's
// other metadata. TurnError travels with its code so a reader can match it against a separately read tail. Call
// ParseLine instead on lines that the scan already parses.
func (m *Meta) ObserveModelFallback(line []byte) {
	if m.ModelFallback == nil && !bytes.Contains(line, []byte("model_refusal_")) {
		return
	}
	scratch := Meta{Sidechain: m.Sidechain, ModelFallback: m.ModelFallback, TurnError: m.TurnError, TurnErrorCode: m.TurnErrorCode}
	ParseLine(0, line, &scratch)
	if scratch.ModelFallbackObserved {
		m.ModelFallback, m.ModelFallbackObserved = scratch.ModelFallback, true
	}
	m.TurnError, m.TurnErrorCode = scratch.TurnError, scratch.TurnErrorCode
}

// claudeModelRefusal keeps even unknown refusal records visible. Only a known fallback with a target model is
// evidence that Claude Code changed models; an absent or new subtype must not invent such a switch.
func (m *Meta) claudeModelRefusal(subtype string, line []byte, it proto.Item) []proto.Item {
	var fields struct {
		OriginalModel string `json:"originalModel"`
		FallbackModel string `json:"fallbackModel"`
		Category      string `json:"apiRefusalCategory"`
		Scope         string `json:"scope"`
		Explanation   string `json:"apiRefusalExplanation"`
	}
	// Malformed or future fields keep the notice, with what could be read.
	err := unmarshalLine(line, &fields)
	r := &proto.ModelRefusal{Subtype: subtype, OriginalModel: fields.OriginalModel, FallbackModel: fields.FallbackModel,
		Category: fields.Category, Scope: fields.Scope, Explanation: fields.Explanation, Text: refusalWords(line), Raw: string(line)}
	// The notice says the refusal's own words, never the line (#2528): the rest of the record is not for clients.
	it.ModelRefusal, it.Text = r, refusalNoticeText(r)
	if err == nil && subtype == "model_refusal_fallback" && r.FallbackModel != "" {
		m.ModelFallback, m.ModelFallbackObserved = r, true
	}
	return []proto.Item{it}
}

func (m *Meta) clearModelFallback() {
	m.ModelFallback, m.ModelFallbackObserved = nil, true
	if m.TurnErrorCode == proto.TurnErrorModelFallbackUnavailable {
		m.TurnErrorCode = ""
	}
}

// A real reply tells us which model ran. Synthetic error replies cannot clear a fallback merely by naming
// <synthetic>, and a successful reply from the fallback keeps session-scoped evidence for later turns.
func (m *Meta) observeReplyModel(model string) {
	if m.ModelFallback == nil || m.ModelFallback.FallbackModel != model {
		m.clearModelFallback()
	}
}

// refusalWords is the record's own refusal text: its content when that is a string (a structured content is not
// shown: its parts are not known to be meant for anyone).
func refusalWords(line []byte) string {
	var c struct {
		Content any `json:"content"`
	}
	if unmarshalLine(line, &c) != nil {
		return ""
	}
	s, _ := c.Content.(string)
	return s
}

// refusalNoticeText is a refusal notice's text: its words, else its explanation, else its subtype. Clients draw the
// fields; the hub writes the text again in the reader's language (hub/transcript_text.go).
func refusalNoticeText(r *proto.ModelRefusal) string {
	switch {
	case r.Text != "":
		return r.Text
	case r.Explanation != "":
		return r.Explanation
	}
	return r.Subtype
}
