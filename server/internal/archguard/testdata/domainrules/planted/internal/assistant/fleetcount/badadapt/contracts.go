package badadapt

import (
	"context"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

// SaveWhy writes the reason into a contract's interface whose method takes any value: the caller reads it back.
func SaveWhy(w fleetcount.Why, s assistantwire.Settings) error { // adapter-exported-result
	return s.PutSetting(context.Background(), "why", WhyWord(w))
}

// Titles hands the reason out as a contract's interface whose method returns raw text.
func Titles(w fleetcount.Why) assistantwire.TitleSource { // adapter-exported-result
	return titled{why: WhyWord(w)}
}

// titled is a title source that answers with the reason.
type titled struct{ why string }

// Title is the reason, whatever the session.
func (t titled) Title(assistantwire.SessionRef) string { return t.why }

// Hooked hands the reason out through a callback a contract's struct holds.
func Hooked(w fleetcount.Why, h assistantwire.Hooks) { // adapter-exported-result
	h.OnWhy(WhyWord(w))
}

// Lazy hands the reason out as a contract's func type.
func Lazy(w fleetcount.Why) assistantwire.Lookup { // adapter-exported-result
	return func(string) string { return WhyWord(w) }
}

// PutRow writes a row to a contract's interface whose method takes only a contract, as an adapter may.
func PutRow(row assistantwire.NeedsYouRow, rows assistantwire.RowSink) error {
	return rows.Put(row)
}

// AskTitle calls what a contract's interface returns with the reason: the caller's lookup reads it.
func AskTitle(w fleetcount.Why, d assistantwire.Documents) { // adapter-exported-result
	d.Titles().DocTitle(WhyWord(w))
}

// OpenTitle calls what a contract's func returns with the reason: the caller's lookup reads it.
func OpenTitle(w fleetcount.Why, open assistantwire.Opener) { // adapter-exported-result
	open().DocTitle(WhyWord(w))
}

// PutRowVia writes a row to the sink a contract's interface returns, whose method takes only a contract, as an
// adapter may.
func PutRowVia(row assistantwire.NeedsYouRow, src assistantwire.RowSource) error {
	return src.Rows().Put(row)
}
