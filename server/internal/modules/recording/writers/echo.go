package writers

import (
	"encoding/json"
	"math"
	"slices"
	"unicode"

	"github.com/nwverse/persona/server/internal/recordingwire"
	"golang.org/x/text/unicode/norm"
)

// Persona's echo in a meeting it speaks in (#5905, docs/assistant/meeting-minutes.md "Persona's echo"): the meeting's
// microphone hears Persona's answers too, and the transcript cannot tell voices apart. A line is Persona's echo only
// when it overlaps a stretch Persona spoke in (widened by echoMargin) and its words are what Persona said there
// (echoContainment of its character bigrams). Echo lines are kept and marked (RecordingSegment.Echo), and left out of
// what is built from the meeting: the minutes, the summary, the follow-up. The phone's copy of the rule is
// apple/Shared/Recording/MeetingEcho.swift; both run the cases in spec/fixtures/meeting-echo.

// echoMargin widens a span by this many seconds on both sides: the recognizer's times are a little off. A starting
// value, to calibrate against labelled recordings.
const echoMargin = 0.5

// echoContainment is the share of a line's distinct character bigrams that must be among those of what Persona said.
// A starting value, to calibrate against labelled recordings.
const echoContainment = 0.6

// Limits of an upload's spoken: more spans than maxSpokenSpans ignores the list, a text over maxSpokenText bytes is cut
// at a character boundary.
const (
	maxSpokenSpans = recordingwire.MaxSpokenSpans
	maxSpokenText  = recordingwire.MaxSpokenText
)

// SpokenSpan is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type SpokenSpan = recordingwire.SpokenSpan

// readSpoken is the upload's spoken, read leniently (recordingwire.ReadSpoken).
func readSpoken(raw json.RawMessage) []SpokenSpan { return recordingwire.ReadSpoken(raw) }

// markEcho marks the segments that are Persona's echo by spans; a segment the phone marked stays marked.
func markEcho(segs []RecordingSegment, spans []SpokenSpan) {
	if len(spans) == 0 {
		return
	}
	for i := range segs {
		if !segs[i].Echo {
			segs[i].Echo = isEcho(segs[i].Start, segs[i].End, segs[i].Text, spans)
		}
	}
}

// isEcho: the line start–end saying text is Persona's echo by one of spans.
func isEcho(start, end float64, text string, spans []SpokenSpan) bool {
	for _, sp := range spans {
		if spanEchoes(sp, start, end, text) {
			return true
		}
	}
	return false
}

// spanEchoes: the line is Persona's echo by this span. A span without text keeps the time rule from before #5905 (holds).
func spanEchoes(sp SpokenSpan, start, end float64, text string) bool {
	if sp.Text == "" {
		return spanHolds(sp, start, end)
	}
	return spanOverlaps(sp, start, end) && echoMatches(text, sp.Text)
}

// spanOverlaps: the line overlaps the span widened by echoMargin on both sides; a line of no length, when it starts inside.
func spanOverlaps(sp SpokenSpan, start, end float64) bool {
	from, to := sp.Start-echoMargin, sp.End+echoMargin
	if end <= start {
		return start >= from && start <= to
	}
	return end >= from && start <= to
}

// spanHolds is the time rule from before #5905: at least half of the line inside the span (no margin); a line of no
// length, when it starts inside.
func spanHolds(sp SpokenSpan, start, end float64) bool {
	length := end - start
	if length <= 0 {
		return start >= sp.Start && start <= sp.End
	}
	inside := math.Min(end, sp.End) - math.Max(start, sp.Start)
	return inside/length >= 0.5
}

// echoMatches: the words of line are what spoken said. Both are normalized (echoNormal); then at least echoContainment of
// the line's distinct character bigrams are among the spoken text's. A line of one character is echo only when the
// spoken text holds it; a line with nothing left is never echo.
func echoMatches(line, spoken string) bool {
	l := echoNormal(line)
	if len(l) == 0 {
		return false
	}
	s := echoNormal(spoken)
	if len(l) < 2 {
		for _, r := range s {
			if r == l[0] {
				return true
			}
		}
		return false
	}
	said := map[[2]rune]bool{}
	for i := 1; i < len(s); i++ {
		said[[2]rune{s[i-1], s[i]}] = true
	}
	mine := map[[2]rune]bool{}
	found := 0
	for i := 1; i < len(l); i++ {
		b := [2]rune{l[i-1], l[i]}
		if mine[b] {
			continue
		}
		mine[b] = true
		if said[b] {
			found++
		}
	}
	return float64(found)/float64(len(mine)) >= echoContainment
}

// echoNormal is text as the echo rule compares it: Unicode NFKC, lower case, letters (L*) and decimal digits (Nd) only;
// punctuation, spaces, symbols and marks go.
func echoNormal(text string) []rune {
	var out []rune
	for _, r := range norm.NFKC.String(text) {
		r = unicode.ToLower(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		}
	}
	return out
}

// ---- hub transcription (#3862 phase 5) ----

// The hub's own sentences in hub mode are judged by the same rule, against the timeline's speaking spans
// (Timeline.Speaking: the spans the phone reports on the audio socket and in an upload's timeline, each with the
// answer said in it, as an upload's spoken has them; docs/assistant/meeting-minutes.md "No echo goes out first", "The
// timeline"). Persona's echo is marked there too, never dropped: it goes into the hub's transcript with Echo, and never
// into the live log, a line frame or the room. Everything outside this file that judges a sentence against the
// speaking spans calls EchoOf, EchoHeld and EchoSpanClosed (TestEchoRuleIsOneFunction keeps it so), so a refinement of
// the rule changes this file and no caller.

// EchoOf: the sentence start–end saying text is Persona's echo (isEcho) by timeline t's speaking spans, or by extra (a
// span the hub assumes closed for the sentences it held, never written to the timeline).
func EchoOf(t Timeline, start, end float64, text string, extra ...SpokenSpan) bool {
	spans := t.Speaking
	if len(extra) > 0 {
		spans = append(slices.Clone(spans), extra...)
	}
	return isEcho(start, end, text, spans)
}

// EchoHeld: a sentence that ends at end, after an open speaking span began at openFrom (less echoMargin), waits for the
// span to close before EchoOf judges it ("No echo goes out first"): until then the span's end is unknown.
func EchoHeld(end, openFrom float64) bool {
	return end > openFrom-echoMargin
}

// EchoSpanClosed is the end of the timeline's speaking span that closes a span open since openFrom: one that begins at
// or before it (to a sample) and ends after it (the speaking off a dropped socket lost, repaired by a socket's
// start.timeline or an upload's timeline; a span still open is never in them). False when the timeline has none.
func EchoSpanClosed(t Timeline, openFrom float64) (to float64, ok bool) {
	const slack = 1.0 / AudioRate // times are rounded to a sample
	for _, sp := range t.Speaking {
		if sp.Start <= openFrom+slack && sp.End > openFrom+slack {
			return sp.End, true
		}
	}
	return 0, false
}

// SpokenText is a speaking span's text as the hub keeps it (recordingwire.SpokenText).
func SpokenText(text string) string { return recordingwire.SpokenText(text) }
