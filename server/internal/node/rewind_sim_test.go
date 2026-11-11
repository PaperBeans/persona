package node

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/herdr"
)

// claudeSim is a Claude Code pane that behaves the way /rewind was recorded to (screen/testdata/
// claude-2.1.318-rewind-*): the idle screen with its input box, the list of prompts in a window of two rows that
// scrolls under the cursor, the confirmation screen with the choices that depend on whether code changed, and what
// a confirmed rewind leaves (the prompt in the box). TestClaudeSimMatchesRecordedScreens holds its drawing to the
// recorded screens, so the flows tested against it run on what the real TUI shows.
type claudeSim struct {
	mu      sync.Mutex
	prompts []simPrompt
	mode    string // idle, list, options, nothing
	box     string // what the input box holds
	cursor  int    // list: index into prompts, len(prompts) is (current)
	choice  int    // options: index into choices()
	age     string
	// keys is every key received, text every text: what a test asserts the node pressed.
	keys []string
	text []string
	// rewound is the prompt (index) a confirmed rewind went back to, -1 before.
	rewound  int
	restored string
	// Faults: the final enter leaves the panel open; the confirmation quotes another message than the list.
	stuck      bool
	quoteOther string
	// boxAfter is what the box holds after a rewind instead of the prompt (something typed at the computer).
	boxAfter string
	// restoreNothing leaves the box empty after a rewind, as when Claude has no prompt to restore.
	restoreNothing bool
	// restoreGate holds the restored prompt back after the panel closes. restoreSeenEmpty is closed by the first read
	// of that temporarily empty box; together they reproduce Claude restoring the prompt a moment after answering.
	restoreGate      chan struct{}
	restoreSeenEmpty chan struct{}
	restorePending   bool
	pendingBox       string
	// ansiReads counts the reads with colors.
	ansiReads int
}

type simPrompt struct {
	text    string
	changes string // "No code changes", "notes.txt +1 -1"
}

func newClaudeSim(prompts ...simPrompt) *claudeSim {
	return &claudeSim{prompts: prompts, mode: "idle", age: "3s ago", rewound: -1}
}

func (c *claudeSim) hasCode(i int) bool { return c.prompts[i].changes != "No code changes" }

func (c *claudeSim) choices() []string {
	if c.cursor < len(c.prompts) && c.hasCode(c.cursor) {
		return []string{"Restore code and conversation", "Restore conversation", "Restore code", "Summarize from here", "Summarize up to here"}
	}
	return []string{"Restore conversation", "Summarize from here", "Summarize up to here", "Never mind"}
}

// key applies one key press.
func (c *claudeSim) key(k string) {
	c.keys = append(c.keys, k)
	switch c.mode {
	case "idle":
		switch k {
		case "enter":
			if c.box == "/rewind" {
				c.box = ""
				if len(c.prompts) == 0 {
					c.mode = "nothing"
				} else {
					c.mode, c.cursor = "list", len(c.prompts)
				}
			}
		case "ctrl+c", "ctrl+u":
			c.box = ""
		case "ctrl+k": // the rest of the row: with ctrl+u after it, a one-row box is empty
		}
	case "nothing":
		if k == "esc" {
			c.mode = "idle"
		}
	case "list":
		switch k {
		case "up":
			c.cursor = max(0, c.cursor-1)
		case "down":
			c.cursor = min(len(c.prompts), c.cursor+1)
		case "esc":
			c.mode = "idle"
		case "enter":
			if c.cursor == len(c.prompts) {
				c.mode = "idle"
			} else {
				c.mode, c.choice = "options", 0
			}
		}
	case "options":
		ch := c.choices()
		switch k {
		case "up":
			c.choice = max(0, c.choice-1)
		case "down":
			c.choice = min(len(ch)-1, c.choice+1)
		case "esc":
			c.mode = "list"
		case "enter":
			switch label := ch[c.choice]; label {
			case "Never mind":
				c.mode = "list"
			case "Restore conversation", "Restore code and conversation":
				if c.stuck {
					return
				}
				c.rewound, c.restored = c.cursor, label
				c.pendingBox = c.prompts[c.cursor].text
				if c.boxAfter != "" {
					c.pendingBox = c.boxAfter
				}
				if c.restoreNothing {
					c.pendingBox = ""
				}
				if c.restoreGate != nil {
					c.restorePending = true
					c.box = ""
				} else {
					c.box = c.pendingBox
				}
				c.prompts = c.prompts[:c.cursor]
				c.mode = "idle"
			}
		}
	}
}

// wrap breaks text at spaces into rows of at most width characters.
func wrap(text string, width int) []string {
	var rows []string
	row := ""
	for _, w := range strings.Fields(text) {
		if row != "" && utf8.RuneCountInString(row)+1+utf8.RuneCountInString(w) > width {
			rows, row = append(rows, row), ""
		}
		if row != "" {
			row += " "
		}
		row += w
	}
	return append(rows, row)
}

const simRule = "▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔"
const simLine = "───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────"

// panel is the rows of /rewind from its header on.
func (c *claudeSim) panel() []string {
	rows := []string{"   Rewind", ""}
	switch c.mode {
	case "nothing":
		return append(rows, "   Nothing to rewind to yet.", "", "   Esc to cancel")
	case "list":
		total := len(c.prompts) + 1
		start := min(max(c.cursor-1, 0), max(total-2, 0))
		rows = append(rows, "   Restore the code and/or conversation to the point before…", "")
		if start > 0 {
			rows = append(rows, "     ↑ "+strconv.Itoa(start)+" more")
		}
		for i := start; i < min(total, start+2); i++ {
			mark := "     "
			if i == c.cursor {
				mark = "   ❯ "
			}
			if i == len(c.prompts) {
				rows = append(rows, mark+"(current)")
				continue
			}
			text := strings.Join(strings.Fields(c.prompts[i].text), " ")
			if r := []rune(text); len(r) > 109 {
				text = string(r[:108]) + "…"
			}
			rows = append(rows, mark+text, "     "+c.prompts[i].changes, "")
		}
		if below := total - (start + 2); below > 0 {
			rows = append(rows, "", "     ↓ "+strconv.Itoa(below)+" more")
		}
		return append(rows, "", "   Enter to continue · Esc to cancel")
	}
	// options
	msg := c.prompts[c.cursor].text
	if c.quoteOther != "" {
		msg = c.quoteOther
	}
	rows = append(rows, "   Confirm you want to restore to the point before you sent this message:", "")
	for _, l := range wrap(msg, 109) {
		rows = append(rows, "   │ "+l)
	}
	rows = append(rows, "   │ ("+c.age+")", "")
	ch := c.choices()
	switch {
	case strings.HasPrefix(ch[c.choice], "Summarize"):
		rows = append(rows, "   Preceding messages will be summarized. This and subsequent messages will remain unchanged — you will stay at the", "   end of the conversation.")
	default:
		rows = append(rows, "   This message and everything after it will be removed.")
		if c.hasCode(c.cursor) && c.choice == 0 {
			rows = append(rows, "   The code will be restored +1 -1 in notes.txt.")
		} else {
			rows = append(rows, "   The code will be unchanged.")
		}
	}
	rows = append(rows, "")
	for i, l := range ch {
		mark := "     "
		switch {
		case i == c.choice:
			mark = "   ❯ "
		case i == 4:
			mark = "   ↓ "
		}
		if i == c.choice && strings.HasPrefix(l, "Summarize up to here") {
			l += ": add context (optional)"
		}
		rows = append(rows, mark+strconv.Itoa(i+1)+". "+l)
	}
	if len(ch) == 5 {
		rows = append(rows, "", "   ⚠ Restored files go back whole, losing later changes by anyone. Bash changes to other files are not undone.")
	}
	return rows
}

// screen is what the pane shows.
func (c *claudeSim) screen() string {
	if c.restorePending {
		select {
		case <-c.restoreGate:
			c.box = c.pendingBox
			c.restorePending = false
		default:
		}
	}
	if c.mode == "idle" && c.rewound >= 0 && c.box == "" && c.restoreSeenEmpty != nil {
		close(c.restoreSeenEmpty)
		c.restoreSeenEmpty = nil
	}
	var rows []string
	for _, p := range c.prompts {
		rows = append(rows, "❯ "+p.text, "● ok", "")
	}
	switch c.mode {
	case "idle":
		box := strings.Split(c.box, "\n")
		rows = append(rows, simLine, "❯ "+box[0])
		for _, l := range box[1:] {
			rows = append(rows, "  "+l)
		}
		rows = append(rows, simLine)
		if c.box == "/rewind" {
			rows = append(rows, "  ❯ /rewind                     Restore the code and/or conversation to a previous point")
		} else {
			rows = append(rows, "  ⏵⏵ bypass permissions on (shift+tab to cycle)")
		}
	default:
		rows = append(rows, simRule)
		rows = append(rows, c.panel()...)
	}
	return strings.Join(rows, "\n")
}

// serve runs the pane as a herdr socket.
func (c *claudeSim) serve(t *testing.T) *herdr.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := herdr.Listen(filepath.Join(dir, "h.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var req struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			_ = json.Unmarshal(line, &req)
			c.mu.Lock()
			var result any = map[string]any{}
			switch req.Method {
			case "pane.read":
				text := c.screen()
				if req.Params["format"] == "ansi" { // the rules grey, as Claude draws them: a read with colors
					c.ansiReads++
					text = strings.ReplaceAll(text, simLine, "\x1b[38;2;136;136;136m"+simLine+"\x1b[0m")
				}
				result = map[string]any{"read": map[string]any{"text": text}}
			case "pane.send_keys":
				for _, k := range req.Params["keys"].([]any) {
					c.key(k.(string))
				}
			case "pane.send_text":
				s, _ := req.Params["text"].(string)
				c.text = append(c.text, s)
				if c.mode == "idle" {
					c.box += s
				}
			}
			c.mu.Unlock()
			b, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
			_, _ = conn.Write(append(b, '\n'))
			conn.Close()
		}
	}()
	return &herdr.Client{Socket: filepath.Join(dir, "h.sock")}
}

func (c *claudeSim) pressed() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.keys, " ")
}
