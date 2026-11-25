package recordingwire

import (
	"bytes"
	"cmp"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// An upload's spoken is at most MaxSpokenSpans spans, each text at most MaxSpokenText bytes (cut at a character
// boundary).
const (
	MaxSpokenSpans = 1000
	MaxSpokenText  = 4000
)

// SpokenSpan is a stretch Persona spoke in, seconds on the recording's clock, with what it said (the upload's spoken).
// Text is empty for a span the phone saved before #5905: such a span keeps the old time rule.
type SpokenSpan struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text,omitempty"`
}

// ReadSpoken is the upload's spoken, leniently: a value that is not a list, or a list over MaxSpokenSpans, is no spans;
// a span that is not an object with numeric, finite start and end (end not before start) and a string text, if any, is
// left out. It never fails the upload: the same request carries the transcript.
func ReadSpoken(raw json.RawMessage) []SpokenSpan {
	if jsonNull(raw) {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) > MaxSpokenSpans {
		return nil
	}
	var out []SpokenSpan
	for _, item := range items {
		var sp struct {
			Start *float64 `json:"start"`
			End   *float64 `json:"end"`
			Text  *string  `json:"text"`
		}
		if json.Unmarshal(item, &sp) != nil || sp.Start == nil || sp.End == nil || !finite(*sp.Start) || !finite(*sp.End) || *sp.End < *sp.Start {
			continue
		}
		span := SpokenSpan{Start: *sp.Start, End: *sp.End}
		if sp.Text != nil {
			span.Text = cutBytes(*sp.Text, MaxSpokenText)
		}
		out = append(out, span)
	}
	return out
}

// SpokenText is a speaking span's text as the hub keeps it (a speaking frame's, the timeline's): cut at MaxSpokenText
// bytes, at a character boundary, as an upload's spoken.
func SpokenText(text string) string {
	return cutBytes(text, MaxSpokenText)
}

// spokenUnion is the union of spans (Timeline.Merge): sorted by start, overlapping and touching spans made one, their
// texts joined in time order (spokenJoin: a span received again counts once). A span that is not one (a number that
// is not finite, end before start) is left out.
func spokenUnion(spans []SpokenSpan) []SpokenSpan {
	var out []SpokenSpan
	for _, sp := range spans {
		if finite(sp.Start) && finite(sp.End) && sp.Start <= sp.End {
			out = append(out, sp)
		}
	}
	slices.SortStableFunc(out, func(a, b SpokenSpan) int {
		if c := cmp.Compare(a.Start, b.Start); c != 0 {
			return c
		}
		return cmp.Compare(a.End, b.End)
	})
	merged := out[:0]
	for _, sp := range out {
		if n := len(merged); n > 0 && sp.Start <= merged[n-1].End {
			merged[n-1].End = max(merged[n-1].End, sp.End)
			merged[n-1].Text = spokenJoin(merged[n-1].Text, sp.Text)
			continue
		}
		merged = append(merged, sp)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// spokenJoin is the text of two spans made one, a's first: a text the other already holds counts once (every upload
// and every socket's start sends the whole timeline again), and the whole is cut at MaxSpokenText bytes like a span's.
func spokenJoin(a, b string) string {
	switch {
	case b == "" || strings.Contains(a, b):
		return a
	case a == "" || strings.Contains(b, a):
		return cutBytes(b, MaxSpokenText)
	}
	return cutBytes(a+" "+b, MaxSpokenText)
}

// jsonNull: a field absent or null.
func jsonNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// cutBytes is s cut to at most n bytes at a character boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
