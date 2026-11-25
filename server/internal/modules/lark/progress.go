package lark

// The Feishu progress card's content, the folded process panel and the pieces note, drawn from what the core hands over
// (assistantwire.RunProgress).

import (
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// missingPiecesNote says, in l, which pieces of an answer did not get to Feishu (1-based) and where the whole is.
func missingPiecesNote(l i18n.Lang, missing []int) string {
	nums := make([]string, len(missing))
	for i, n := range missing {
		nums[i] = strconv.Itoa(n)
	}
	return i18n.T(l, "assistant-feishu.missing_pieces", i18n.Args{"count": len(missing), "pieces": strings.Join(nums, listSep(l))})
}

// listSep joins the items of a list in l: Chinese and Japanese use an enumeration comma, English ", ".
func listSep(l i18n.Lang) string {
	if strings.HasPrefix(string(l), "en") {
		return ", "
	}
	return "、" // i18n-ignore: the CJK enumeration comma between list items, punctuation not prose
}

// progressCard is a run's card while it goes on; v.Waiting is the note while its message is not found in the
// conversation yet, "" once it is. A zero v.Target means no live run: the card carries no Stop button.
func progressCard(l i18n.Lang, label string, took time.Duration, steps []string, live string, v assistantwire.RunProgress) *botcard.Card {
	summary := i18n.T(l, "assistant-feishu.working_summary_took", i18n.Args{"took": channel.Duration(l, took)})
	state, color := i18n.T(l, "assistant-feishu.state_working"), botcard.Blue
	switch v.Waiting {
	case "":
	case "queued":
		summary, state, color = i18n.T(l, "assistant-feishu.queued_summary"), i18n.T(l, "assistant-feishu.state_queued"), botcard.Neutral
	default:
		summary, state, color = i18n.T(l, "assistant-feishu.preparing_summary"), i18n.T(l, "assistant-feishu.state_preparing"), botcard.Neutral
	}
	c := botcard.NewCard("", "", summary)
	// Its state as a coloured tag first, then who works and for how long.
	c.Markdown(botcard.InlineTag(state, color) + " <font color='grey'>" + botcard.EscapeMarkdown(label+" · "+channel.Duration(l, took)) + "</font>")
	for _, md := range progressLines(l, steps, v) {
		c.Markdown(md)
	}
	if n := v.Calls; n > 0 {
		parts := []string{i18n.T(l, "assistant-feishu.count_calls", i18n.Args{"count": n})}
		for _, f := range []struct {
			key string
			n   int
		}{{"assistant-feishu.count_read", v.Read}, {"assistant-feishu.count_edited", v.Edited}} {
			if f.n == 0 {
				continue
			}
			shown := strconv.Itoa(f.n)
			if f.n >= assistantwire.RunFilesMost {
				shown += "+" // noteFile stops counting there
			}
			parts = append(parts, i18n.T(l, f.key, i18n.Args{"count": f.n, "n": shown}))
		}
		c.Note(strings.Join(parts, " · "))
	}
	// What the conversation is writing now, in an element of its own: a card entity streams it (the lark module's stream.go).
	c.MarkdownID(progressLiveID, progressLive(live))
	// The button is the /stop command on this run's conversation; it is only on the card of a run still in flight.
	if v.Target.Kind != "" {
		c.Buttons(botcard.Btn(i18n.T(l, "assistant-feishu_cards.stop"), botcard.Danger,
			map[string]any{"a": "stop", "k": v.Target.Kind, "m": v.Target.MachineID, "s": v.Target.SessionID}))
	}
	return c
}

// progressLines are the progress card's Markdown blocks: the newest call still running and for how long, the last
// steps, the subagents.
func progressLines(l i18n.Lang, steps []string, v assistantwire.RunProgress) []string {
	var out []string
	if n := len(v.Open); n > 0 {
		o := v.Open[n-1]
		now := i18n.T(l, "assistant-feishu.now_running", i18n.Args{"step": o.Step, "took": channel.Duration(l, max(v.Now.Sub(o.Since), 0))})
		if n > 1 {
			now += " " + i18n.T(l, "assistant-feishu.and_more", i18n.Args{"count": n - 1})
		}
		out = append(out, now)
	}
	if n := len(steps); n > 0 {
		var b strings.Builder
		if n > 5 {
			b.WriteString(i18n.T(l, "assistant-feishu.earlier_steps", i18n.Args{"count": n - 5}))
			steps = steps[n-5:]
		}
		for _, st := range steps {
			b.WriteString("- " + st + "\n")
		}
		out = append(out, strings.TrimSpace(b.String()))
	}
	if n := len(v.Subs); n > 0 {
		var b strings.Builder
		b.WriteString(i18n.T(l, "assistant-feishu.subtasks") + "\n")
		subs := v.Subs
		if n > 6 {
			subs = subs[n-6:]
		}
		for _, st := range subs {
			line := botcard.EscapeMarkdown(st.Desc)
			if line == "" {
				line = i18n.T(l, "assistant-feishu.step_subtask")
			}
			if st.Kind != "" {
				line += " " + botcard.Code(st.Kind)
			}
			state := "assistant-feishu.subtask_running"
			if st.Done {
				state = "assistant-feishu.subtask_done"
			}
			b.WriteString("- " + line + " · " + i18n.T(l, state) + "\n")
		}
		if more := n - len(subs) + v.SubsMore; more > 0 {
			b.WriteString(i18n.T(l, "assistant-feishu.and_more", i18n.Args{"count": more}))
		}
		out = append(out, strings.TrimSpace(b.String()))
	}
	return out
}

// processPanel is the fallback card's folded process, self-bounded for the quiet end card outside answerCards
// (its foot/note are short).
func processPanel(l i18n.Lang, c *botcard.Card, steps []string) {
	addProcessPanel(l, c, steps, fitPanelSteps(l, steps, min(processPanelStepsMost, len(steps)), processPanelMaxBytes))
}

// fitPanelSteps halves from start until the panel's JSON inside an empty card envelope is at most maxBytes.
func fitPanelSteps(l i18n.Lang, steps []string, start, maxBytes int) int {
	empty := len(botcard.NewCard("", "", "").JSON())
	for n := start; n > 0; n /= 2 {
		probe := botcard.NewCard("", "", "")
		addProcessPanel(l, probe, steps, n)
		if len(probe.JSON())-empty <= maxBytes {
			return n
		}
	}
	return 0
}

// addProcessPanel shows the last maxSteps clipped lines; title counts all steps, a line notes hidden earlier ones.
func addProcessPanel(l i18n.Lang, c *botcard.Card, steps []string, maxSteps int) {
	if maxSteps <= 0 || len(steps) == 0 {
		return
	}
	maxSteps = min(maxSteps, len(steps))
	var b strings.Builder
	if hidden := len(steps) - maxSteps; hidden > 0 {
		b.WriteString(i18n.T(l, "assistant-feishu.earlier_steps", i18n.Args{"count": hidden}))
	}
	for _, st := range steps[len(steps)-maxSteps:] {
		b.WriteString("- " + clipPanelLine(st, processPanelLineRunes) + "\n")
	}
	c.Collapsible(i18n.T(l, "assistant-feishu.cot_tools", i18n.Args{"count": len(steps)}), strings.TrimSpace(b.String()))
}

// panelEntityStart matches a dangling entity prefix the card escapers emit ("&#124;"-style number or "&sim") at
// the cut; a literal '&' before other text stays.
var panelEntityStart = lazyre.New(`&(?:#[0-9]{0,5}|s(?:i(?:m)?)?)?$`)

// clipPanelLine cuts an already card-escaped line to n runes plus an ellipsis, never splitting an escape entity;
// a literal '&' stays.
func clipPanelLine(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return panelEntityStart.ReplaceAllString(string(r[:n]), "") + "…"
}

// progressLive is the conversation's words as the progress card shows them: escaped, and only their end when long.
func progressLive(live string) string {
	live = strings.TrimSpace(live)
	if r := []rune(live); len(r) > progressLiveMost {
		live = "…" + string(r[len(r)-progressLiveMost:])
	}
	return botcard.EscapeMarkdown(live)
}

// Folded panel limits: newest calls shown, per-line rune cap (escape entities kept whole), own JSON byte budget.
const (
	processPanelStepsMost = 40
	processPanelLineRunes = 120
	processPanelMaxBytes  = 4000
)

const (
	// progressLiveID is the element of the progress card the conversation's words are streamed into.
	progressLiveID = botcard.ProgressLiveID
	// progressLiveMost is how much of the words the card shows (their end): text that has outgrown it is written over
	// rather than streamed on, as it no longer starts the same.
	progressLiveMost = 2000
)
