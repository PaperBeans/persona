package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The node reads what a stopped turn asks off its whole last reply (#5096). A conversational agent that ends every
// turn on a question and says in its closeout that the turn is done and needs nobody asks nothing (#4110); the same
// reply without that closeout still asks.
func TestQuickMetaAsksHonoursCloseout(t *testing.T) {
	const q = "Which of these matters most to you?"
	for _, c := range []struct{ name, reply, want string }{
		{"no closeout", "Here are three directions.\n\n" + q, q},
		{"done, needs nobody", "Here are three directions.\n\n<closeout status=\"done\" merged=\"n/a\" deployed=\"n/a\"></closeout>\n\n" + q, ""},
		{"blocked on the user", "Here are three directions.\n\n<closeout status=\"blocked\">\nwaiting: your pick\n</closeout>\n\n" + q, q},
	} {
		text, err := json.Marshal(c.reply)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "s.jsonl")
		body := `{"type":"user","cwd":"/tmp/x","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"help me plan"}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"model":"claude-opus-5-5","id":"msg_1","role":"assistant","content":[{"type":"text","text":` +
			string(text) + `}]}}` + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := readQuickMeta(path, st).asks; got != c.want {
			t.Errorf("%s: asks = %q, want %q", c.name, got, c.want)
		}
	}
}
