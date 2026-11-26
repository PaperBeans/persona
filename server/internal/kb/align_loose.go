package kb

import (
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// The loose tier of aligning the Lark user sync with the Lark gateway (align_gateway.go).
//
// The fingerprint and format tiers compare two renderings of one message after reconciling known renderer
// differences. Four kinds of difference were still seen on live archives after repair version 3: an image placeholder
// on one side only (the other renders the image as an attachment, or not at all), a leading @mention written another
// way or spaced differently, a card or rich post that one side expands in full and the other gives only its title,
// and two otherwise identical texts one character apart. The loose tier accepts those, and only those, and only
// within the tightest frame the earlier tiers use:
//
//   - the same chat (the crosswalk's pair, or the one gateway chat of that name), the same known sender (on a paired
//     chat an unknown sender may stand for any non-owner, as in the format tier), both times exact from the receipts
//     and in the same or a neighbouring 2-second bucket;
//   - exactly one gateway line in that frame passes; two or more is ambiguous and nothing is dropped;
//   - the texts, with media placeholders removed (both sides' "[image]", "[附件](…)" and the like), are equal ignoring
//     white space, or after one leading @mention (at most looseMentionMost characters, written otherwise or left out
//     on one side) followed by the same text of at least looseTitleMin characters; or the shorter one is the longer
//     one's first lines (a card's or post's title, line by line, at least looseTitleMin characters); or both are at
//     least looseSimilarMin characters, differ in at most one character per looseSimilarPer (98 % alike), and carry
//     the same numbers and the same links.
//
// Why 98 % and not lower: two different messages from one sender in one 2-second bucket that share 98 % of at least
// 40 characters, every number and every link are not two messages a reader tells apart (a resend, an edit, one
// renderer clipping the last character). A lower ratio would start to accept template messages (alerts, bot reports)
// that differ by a word; requiring equal numbers and links keeps those apart even at 98 % (a build number, an amount,
// a host index or a URL is what usually distinguishes them). Short texts never match by similarity at all.
//
// Which copy stays: the fuller one (more characters once placeholders are removed, then more attachments). When the
// gateway's line is the shorter one (it kept a card's title only), the sync message is written and the gateway line is
// dropped instead, keeping its receipt ID, so the archive keeps the full card once.

// Thresholds of the loose tier (see above).
const (
	looseTitleMin    = 4  // the shorter text must have at least this many characters to stand for the longer's title
	looseSimilarMin  = 40 // both texts must have at least this many characters to match by similarity
	looseSimilarPer  = 50 // at most one differing character per this many characters of the longer text (98 %)
	looseMentionMost = 40 // the longest leading mention (white space removed) the mention rule sets aside
)

var (
	looseMedia   = lazyre.New(`\[(?:image|video|audio|图片|视频|语音|附件)\](?:\([^()\s]*\))?`) // i18n-ignore: archive data
	looseAtTag   = lazyre.New(`<at\b[^>]*>([^<]*)</at>`)
	looseMention = lazyre.New(`^(?:@\S+(?:\s+|$))+`)
	looseDigits  = lazyre.New(`[0-9]+`)
	looseURL     = lazyre.New(`https?://[^\s()<>]+`)
)

// gatewayDrop is a gateway chat's line to drop: its chat id, its day and its receipt key (gatewayLine.key).
type gatewayDrop struct {
	chat, day, key string
}

// alignVerdict is what comparing a sync message with a gateway chat found: dup, the sync copy is a duplicate; drop,
// the gateway line is the shorter copy of the same message and goes instead; neither, no match.
type alignVerdict struct {
	dup  bool
	drop *gatewayDrop
}

var dupVerdict = alignVerdict{dup: true}

func (v alignVerdict) matched() bool { return v.dup || v.drop != nil }

// looseText is a message's text as the loose tier compares it.
type looseText struct {
	lines  []string // its non-empty lines, white space collapsed, the first without its leading @mentions
	flat   string   // lines joined without any white space
	full   string   // the same with the leading @mentions kept
	runes  int      // characters of flat
	digits []string // its numbers, in order
	urls   []string // its links, in order
}

func looseForm(raw string, mediaCount int) looseText {
	var lines []string
	for _, l := range strings.Split(raw, "\n") {
		l = looseAtTag.ReplaceAllString(l, "@$1")
		l = equivalentGatewayBody(l, mediaCount)
		l = strings.Join(strings.Fields(looseMedia.ReplaceAllString(l, " ")), " ")
		if l != "" {
			lines = append(lines, l)
		}
	}
	var t looseText
	t.full = noSpace(strings.Join(lines, ""))
	if len(lines) > 0 {
		if first := strings.TrimSpace(looseMention.ReplaceAllString(lines[0], "")); first == "" {
			lines = lines[1:]
		} else {
			lines[0] = first
		}
	}
	t.lines = lines
	t.flat = noSpace(strings.Join(lines, ""))
	t.runes = utf8.RuneCountInString(t.flat)
	joined := strings.Join(lines, " ")
	t.digits = looseDigits.FindAllString(joined, -1)
	t.urls = looseURL.FindAllString(joined, -1)
	return t
}

func noSpace(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// looseMatch says whether a and b are one message by the loose tier's text rules (match), and whether one holds less
// of it than the other (shorter: a title, a character fewer) rather than the same text written another way (white
// space, placeholders, mentions).
func looseMatch(a, b looseText) (match, shorter bool) {
	if a.full == "" || b.full == "" {
		return false, false
	}
	if a.full == b.full || sameAfterMention(a.full, b.full) {
		return true, false
	}
	short, long := a, b
	if short.runes > long.runes {
		short, long = b, a
	}
	if n := len(short.lines); n > 0 && n < len(long.lines) && short.runes >= looseTitleMin {
		title := true
		for i := range short.lines {
			if noSpace(short.lines[i]) != noSpace(long.lines[i]) {
				title = false
				break
			}
		}
		if title {
			return true, true
		}
	}
	ar, br := []rune(a.flat), []rune(b.flat)
	if min(len(ar), len(br)) < looseSimilarMin || !slices.Equal(a.digits, b.digits) || !slices.Equal(a.urls, b.urls) {
		return false, false
	}
	if editsWithin(ar, br, max(1, max(len(ar), len(br))/looseSimilarPer)) {
		return true, true
	}
	return false, false
}

// sameAfterMention says whether a and b (white space removed) are the same text after a leading @mention that one
// side writes otherwise or leaves out: what follows their longest common ending is, on each side, nothing or one
// mention ("@" and at most looseMentionMost characters, a display name may hold spaces), and that ending is at least
// looseTitleMin characters.
func sameAfterMention(a, b string) bool {
	if !strings.HasPrefix(a, "@") && !strings.HasPrefix(b, "@") {
		return false
	}
	ar, br := []rune(a), []rune(b)
	n := 0
	for n < len(ar) && n < len(br) && ar[len(ar)-1-n] == br[len(br)-1-n] {
		n++
	}
	if n < looseTitleMin {
		return false
	}
	head := func(r []rune) bool {
		h := r[:len(r)-n]
		return len(h) == 0 || (h[0] == '@' && len(h) <= looseMentionMost)
	}
	return head(ar) && head(br)
}

// editsWithin reports whether a and b are at most k insertions, deletions or substitutions apart (Levenshtein,
// stopping as soon as every path is over k).
func editsWithin(a, b []rune, k int) bool {
	if d := len(a) - len(b); d > k || -d > k {
		return false
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			best = min(best, cur[j])
		}
		if best > k {
			return false
		}
		prev, cur = cur, prev
	}
	return prev[len(b)] <= k
}

// syncFuller says whether the sync copy (s, with sMedia attachments) holds more than the gateway's (g): when one
// holds less of the message (shorter), the one with more characters once placeholders and leading
// mentions are set aside; otherwise, and on a tie, the one with more attachments. Texts that differ only in white
// space, placeholders or how a mention is written are equally full, and the gateway's copy stays.
func syncFuller(shorter bool, s, g looseText, sMedia, gMedia int) bool {
	if shorter && s.runes != g.runes {
		return s.runes > g.runes
	}
	return sMedia > gMedia
}

// withinLoose looks for src (a sync message, or an archived sync line) in gateway chat gw by the loose tier's rules.
// paired is true when gw came from chats/.crosswalk.json (L1).
func (a *gatewayAligner) withinLoose(gw string, src gatewayLine, paired bool) alignVerdict {
	if src.exact.IsZero() {
		return alignVerdict{}
	}
	srcUnknown := src.sender == "" || src.sender == unknownSender
	if !paired && srcUnknown {
		return alignVerdict{}
	}
	srcRaw := src.formatBody
	if srcRaw == "" {
		srcRaw = src.body
	}
	sf := looseForm(srcRaw, max(src.mediaCount, 1))
	if sf.full == "" {
		return alignVerdict{}
	}
	var found gatewayLine
	var foundDay string
	var foundText looseText
	var foundShorter bool
	matches := 0
	for _, day := range alignDays(src.exact, a.s.Zone()) {
		for _, dst := range a.day(gw, day) {
			if dst.exact.IsZero() {
				continue
			}
			dstUnknown := dst.sender == "" || dst.sender == unknownSender
			senderMatch := !srcUnknown && !dstUnknown && normalizeText(dst.sender) == normalizeText(src.sender)
			if !senderMatch && paired {
				senderMatch = (srcUnknown && dst.sender != ownerSender) || (dstUnknown && src.sender != ownerSender)
			}
			if !senderMatch {
				continue
			}
			if bucket := dst.exact.Unix()/2 - src.exact.Unix()/2; bucket < -1 || bucket > 1 {
				continue
			}
			dstRaw := dst.formatBody
			if dstRaw == "" {
				dstRaw = dst.body
			}
			df := looseForm(dstRaw, max(dst.mediaCount, 1))
			if match, shorter := looseMatch(sf, df); match {
				matches++
				found, foundDay, foundText, foundShorter = dst, day, df, shorter
			}
		}
	}
	if matches != 1 {
		return alignVerdict{}
	}
	if syncFuller(foundShorter, sf, foundText, src.mediaCount, found.mediaCount) {
		if !found.unique || found.key == "" {
			return alignVerdict{} // the gateway line cannot be told from an identical one: keep both copies
		}
		return alignVerdict{drop: &gatewayDrop{chat: gw, day: foundDay, key: found.key}}
	}
	return dupVerdict
}

// settleSuperseded drops the gateway lines the aligner found shorter than sync messages of chat syncID it let through
// (gatewayAligner.settle), for those messages that are now written in the chat; the rest are forgotten (a message
// the selection did not keep leaves the gateway's copy alone). Caller holds e.Store.ingestMu, not chatMu.
func (e *Engine) settleSuperseded(syncID string) {
	s := e.Store
	pending := s.superseded[syncID]
	if len(pending) == 0 {
		return
	}
	delete(s.superseded, syncID)
	var r messageReceipts
	if ok, err := readJSON(s.path(KindChat, syncID, ingestReceipts), &r); err != nil || !ok {
		return
	}
	drops := map[string]map[string]bool{}
	for id, d := range pending {
		if _, written := r.Lines[id]; !written {
			continue
		}
		if drops[d.chat] == nil {
			drops[d.chat] = map[string]bool{}
		}
		drops[d.chat][d.day+"\x00"+d.key] = true
	}
	dropped := 0
	for _, gw := range slices.Sorted(maps.Keys(drops)) {
		n, err := e.dropGatewayLines(gw, drops[gw])
		if err != nil {
			e.logf("drop superseded gateway lines %s: %v", gw, err)
			continue
		}
		dropped += n
	}
	if dropped > 0 {
		s.SearchIndexPurge()
	}
}
