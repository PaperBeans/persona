package transcript

import (
	"bytes"
	"math"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// The tokens of the model requests a chat's items stand for (proto.Item.Usage, #5426), so a client can sum a turn's
// usage. A Claude Code request's usage rides on the first item its reply lines made; a Codex turn's total on its
// turn_duration item (or its interrupted notice); a finished subagent's or workflow run's on its report, set by the node.

// claudeUsage is a reply line's message.usage as Claude Code writes it.
type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreation            *struct {
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	Speed string `json:"speed"` // "fast": Claude Code's fast mode
}

// tokens is the usage as a TokenUsage of model; ok is false when it is all zeros (a gateway's on a turn's other
// replies, an error Claude Code wrote itself): no request to count.
func (u *claudeUsage) tokens(model string) (proto.TokenUsage, bool) {
	t := proto.TokenUsage{Model: model, Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens, Fast: u.Speed == "fast"}
	if u.CacheCreation != nil {
		t.CacheWrite1h = u.CacheCreation.Ephemeral1h
	}
	return t, t.Input > 0 || t.Output > 0 || t.CacheRead > 0 || t.CacheWrite > 0
}

// replyTokens is a reply line's request usage, ok false for one that counts none: no usage, all zeros, or a reply
// Claude Code wrote itself for a failed request (model "<synthetic>", isApiErrorMessage).
func replyTokens(model string, u *claudeUsage, apiError bool) (proto.TokenUsage, bool) {
	if u == nil || apiError || model == "<synthetic>" {
		return proto.TokenUsage{}, false
	}
	return u.tokens(model)
}

// RequestUsage follows the request a run of Claude Code reply lines belongs to, so that each request counts once:
// every content block of a reply is a line of its own repeating the request's usage. Claude Code up to 2.1.306 wrote
// the output generated so far on a reply's earlier blocks, more on each later one: a later line adds what grew, and
// the request's lines add up to its last (largest) usage.
type RequestUsage struct {
	id  string
	got proto.TokenUsage // the most of each count its lines gave so far
}

// Add returns what a line of request id with usage u adds to what the request's earlier lines gave; ok is false when
// it adds nothing (a repeat). A line without an id is a request of its own.
func (r *RequestUsage) Add(id string, u proto.TokenUsage) (proto.TokenUsage, bool) {
	if id == "" || id != r.id {
		r.id, r.got = id, proto.TokenUsage{}
	}
	g := &r.got
	add := proto.TokenUsage{Model: u.Model, Fast: u.Fast, Input: max(u.Input-g.Input, 0), Output: max(u.Output-g.Output, 0),
		CacheRead: max(u.CacheRead-g.CacheRead, 0), CacheWrite: max(u.CacheWrite-g.CacheWrite, 0),
		CacheWrite1h: max(u.CacheWrite1h-g.CacheWrite1h, 0)}
	most(g, u)
	return add, add.Input > 0 || add.Output > 0 || add.CacheRead > 0 || add.CacheWrite > 0 || add.CacheWrite1h > 0
}

// most raises each count of g to u's where u's is larger.
func most(g *proto.TokenUsage, u proto.TokenUsage) {
	g.Input, g.Output, g.CacheRead = max(g.Input, u.Input), max(g.Output, u.Output), max(g.CacheRead, u.CacheRead)
	g.CacheWrite, g.CacheWrite1h = max(g.CacheWrite, u.CacheWrite), max(g.CacheWrite1h, u.CacheWrite1h)
}

// replyUsage puts the usage of the request a reply line belongs to on the first item the line made (first; the caller
// passes it only when there is one), as far as the request's earlier lines did not already. A line that made no item
// (a thinking block with nothing shown) leaves it to the request's next line. A subagent's lines in the session's
// transcript (sidechain) carry none: the node reports a subagent's usage on its result. In a subagent's own transcript
// (Meta.Sidechain) they do, but not a fork's copy of its launcher's reply that launched it, before the fork's prompt
// (its first user line): that request is the launcher's.
func (m *Meta) replyUsage(e *entry, first *proto.Item) {
	if e.IsSidechain && !m.Sidechain || m.Sidechain && !m.sawUser {
		return
	}
	u, ok := replyTokens(e.Message.Model, e.Message.Usage, e.IsAPIErrorMessage)
	if !ok {
		return
	}
	if add, ok := m.usage.Add(e.Message.ID, u); ok {
		first.Usage = []proto.TokenUsage{add}
	}
}

// ReplyUsage reads a Claude Code transcript line as a reply's: its request's id and usage. ok is false for any other
// line and for a reply that counts none (replyTokens). Sidechain lines read like any other: the node sums a subagent's
// own transcript with it.
func ReplyUsage(line []byte) (id string, u proto.TokenUsage, ok bool) {
	var e struct {
		Type     string `json:"type"`
		APIError bool   `json:"isApiErrorMessage"`
		Message  *struct {
			ID    string       `json:"id"`
			Model string       `json:"model"`
			Usage *claudeUsage `json:"usage"`
		} `json:"message"`
	}
	if unmarshalLine(line, &e) != nil || e.Type != "assistant" || e.Message == nil {
		return "", proto.TokenUsage{}, false
	}
	u, ok = replyTokens(e.Message.Model, e.Message.Usage, e.APIError)
	return e.Message.ID, u, ok
}

// codexRequest counts a rollout's token_count in the turn's usage the way the usage meter does (node/usage.go): only a
// grown running total is a new request (rate-limit updates repeat the last one); its input without the cached part,
// the cached part as a cache read, its output (reasoning included).
func (m *Meta) codexRequest(total, input, cached, output int64) {
	if total <= m.codexTotal {
		return
	}
	m.codexTotal = total
	u := &m.codexUsage
	u.Input += max(input-cached, 0)
	u.CacheRead += max(cached, 0)
	u.Output += max(output, 0)
}

// codexTurnUsage takes the usage of the turn that just ended (Item.Usage of the item that ends it): nil when it used
// none.
func (m *Meta) codexTurnUsage() []proto.TokenUsage {
	u := m.codexUsage
	m.codexUsage = proto.TokenUsage{}
	if u.Input == 0 && u.Output == 0 && u.CacheRead == 0 {
		return nil
	}
	u.Model = m.codexTurnModel
	return []proto.TokenUsage{u}
}

// AddUsage adds u to sum: into the entry of the same model and speed when u has no ID, as its own entry when it has
// one (a subagent's report, counted once by its ID).
func AddUsage(sum []proto.TokenUsage, u proto.TokenUsage) []proto.TokenUsage {
	if u.ID == "" {
		for i := range sum {
			if s := &sum[i]; s.ID == "" && s.Model == u.Model && s.Fast == u.Fast {
				s.Input += u.Input
				s.Output += u.Output
				s.CacheRead += u.CacheRead
				s.CacheWrite += u.CacheWrite
				s.CacheWrite1h += u.CacheWrite1h
				return sum
			}
		}
	}
	return append(sum, u)
}

// foldUsage is what a folded run's items used (proto.Fold.Usage), nil when they carried none.
func foldUsage(run []proto.Item) []proto.TokenUsage {
	var sum []proto.TokenUsage
	for _, it := range run {
		for _, u := range it.Usage {
			sum = AddUsage(sum, u)
		}
	}
	return sum
}

// seedUsage looks back at most seedUsageLines lines and seedUsageBytes of them for the request a window's first lines
// go on from. A line over seedUsageLine bytes is read only as far as seedUsageHead (a request blob repeats the whole
// conversation; a reply with a huge tool input says its request's id near its start). A rollout's latest token_count
// further back than that is found by a byte search over at most seedCodexSearch bytes.
const (
	seedUsageLines  = 64
	seedUsageBytes  = 2 << 20
	seedUsageLine   = 256 << 10
	seedUsageHead   = 64 << 10
	seedCodexSearch = 64 << 20
)

var (
	usageMark      = []byte(`"usage"`)
	tokenCountMark = []byte(`"token_count"`)
	messageMark    = []byte(`"message":{`)
	sidechainMark  = []byte(`"isSidechain":true`)
	apiRequestHead = []byte(`{"type":"api-request`)
)

// seedLines reads the lines before a window for seedUsage (window.go seeded).
type seedLines struct {
	line func(n int) []byte            // line n whole
	head func(n int, max int64) []byte // its first max bytes
	size func(n int) int64
	// tokenCount is the latest line before line before that mentions a token_count, found by a byte search back at
	// most seedCodexSearch bytes; 0 when there is none.
	tokenCount func(before int) int
}

// saturated is a request's usage that no later line of it adds to (Add gives nothing more).
var saturated = proto.TokenUsage{Input: math.MaxInt64, Output: math.MaxInt64, CacheRead: math.MaxInt64,
	CacheWrite: math.MaxInt64, CacheWrite1h: math.MaxInt64}

// seedUsage is the usage state a full read has by line b (window.go seeded), from the lines just before it: a window
// may start among a Claude reply's lines, whose request's usage an earlier one already put on an item; a rollout's
// token_count only counts when its running total grew. A request's reply lines are next to each other (the results of
// its tool calls between them at most), and the latest token_count holds the total: the nearest lines say it.
func (f *File) seedUsage(r seedLines, b int, codex bool) (RequestUsage, int64) {
	if codex {
		return RequestUsage{}, seedCodexTotal(r, b)
	}
	var req RequestUsage
	found := false
	budget := int64(seedUsageBytes)
	for n := b - 1; n >= 1 && n >= b-seedUsageLines; n-- {
		sz := r.size(n)
		if sz > seedUsageLine {
			// A reply this big says its request's id near its start, and put the request's usage on an item: a later
			// line of the request adds nothing (Claude Code repeats a request's usage on every line of it).
			switch id := replyHeadID(r.head(n, seedUsageHead), f.meta.Sidechain); {
			case id == "":
				continue // no reply: further back
			case !found || id == req.id:
				return RequestUsage{id: id, got: saturated}, 0
			default:
				return req, 0
			}
		}
		if budget -= sz; budget < 0 {
			break
		}
		line := r.line(n)
		if !bytes.Contains(line, usageMark) {
			continue
		}
		scratch := Meta{Sidechain: f.meta.Sidechain, sawUser: true}
		if items := ParseLine(n, line, &scratch); len(items) == 0 || items[0].Usage == nil {
			continue // no reply that put usage on an item: further back
		}
		switch {
		case !found:
			req, found = scratch.usage, true
			if req.id == "" { // a request of its own: nothing goes on from it
				return RequestUsage{}, 0
			}
		case scratch.usage.id != req.id: // another request's: req's lines do not go back past it
			return req, 0
		default: // an earlier line of req's: what the request had given by b is the most of them
			most(&req.got, scratch.usage.got)
		}
	}
	return req, 0
}

// seedCodexTotal is a rollout's running total of tokens by line b: its latest token_count's, among the lines just
// before or, further back, where a byte search finds one.
func seedCodexTotal(r seedLines, b int) int64 {
	total := func(n int) int64 {
		if r.size(n) > seedUsageLine { // a token_count is a short line
			return 0
		}
		line := r.line(n)
		if !bytes.Contains(line, tokenCountMark) {
			return 0
		}
		scratch := Meta{Codex: true}
		ParseLine(n, line, &scratch)
		return scratch.codexTotal
	}
	budget := int64(seedUsageBytes)
	n := b - 1
	for ; n >= 1 && n >= b-seedUsageLines; n-- {
		if budget -= min(r.size(n), seedUsageLine); budget < 0 {
			break
		}
		if t := total(n); t > 0 {
			return t
		}
	}
	for before := n + 1; r.tokenCount != nil; {
		m := r.tokenCount(before)
		if m == 0 {
			return 0
		}
		if t := total(m); t > 0 {
			return t
		}
		before = m // it only mentioned one (a tool's output quoting it): further back
	}
	return 0
}

// replyHeadID is the request id of a Claude Code reply line read only as far as head, "" when head is not one's: the
// line's message (the first one in it, before any content) says role "assistant" and an id among the keys before its
// content. In a session's transcript a sidechain line is no reply of the session's (sidechain: the file is a
// subagent's own, every line sidechain).
func replyHeadID(head []byte, sidechain bool) string {
	if bytes.HasPrefix(head, apiRequestHead) {
		return ""
	}
	i := bytes.Index(head, messageMark)
	if i < 0 || !sidechain && bytes.Contains(head[:i], sidechainMark) {
		return ""
	}
	var id, role string
	rest := head[i+len(messageMark):]
	for {
		rest = rest[skipJSONSpace(rest, 0):]
		if len(rest) == 0 || rest[0] != '"' {
			break
		}
		end := jsonStringEnd(rest, 0)
		if end < 0 {
			break
		}
		key := rest[1:end]
		rest = rest[skipJSONSpace(rest, end+1):]
		if len(rest) == 0 || rest[0] != ':' {
			break
		}
		rest = rest[skipJSONSpace(rest, 1):]
		if len(rest) == 0 || rest[0] != '"' { // content, usage: the keys with a string value come first
			break
		}
		end = jsonStringEnd(rest, 0)
		if end < 0 {
			break
		}
		switch string(key) {
		case "id":
			id = string(rest[1:end])
		case "role":
			role = string(rest[1:end])
		}
		rest = rest[skipJSONSpace(rest, end+1):]
		if len(rest) > 0 && rest[0] == ',' {
			rest = rest[1:]
		}
	}
	if role != "assistant" || strings.ContainsRune(id, '\\') {
		return ""
	}
	return id
}
