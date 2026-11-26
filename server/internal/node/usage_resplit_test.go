package node

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// resplitManager answers the re-split's calls as a manager that keeps directories does (status: the range), or as an
// older one (an unknown method), and hands over each usage.resplit request.
type resplitManager struct {
	answers       io.Writer
	since, before int64
	knows         bool
	requests      chan json.RawMessage
}

func (w resplitManager) Write(p []byte) (int, error) {
	var call struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(p, &call) != nil || call.Method == "" {
		return len(p), nil
	}
	reply := `{"id":"` + call.ID + `","error":{"message":"unknown method"}}`
	switch {
	case !w.knows:
	case call.Method == "usage.resplit.status":
		b, _ := json.Marshal(map[string]any{"done": false, "since": w.since, "before": w.before})
		reply = `{"id":"` + call.ID + `","result":` + string(b) + `}`
	case call.Method == "usage.resplit":
		w.requests <- call.Params
		reply = `{"id":"` + call.ID + `","result":{"moved":1}}`
	}
	go func() { _, _ = w.answers.Write([]byte(reply + "\n")) }()
	return len(p), nil
}

// Once, the node re-reads the range the manager names by the ten minutes, each bucket under the directory its
// session started in, and sends it with the last ten minutes and done; with an older manager it sends nothing (#16530).
func TestResplitUsage(t *testing.T) {
	old := resplitDelay
	resplitDelay = 0
	t.Cleanup(func() { resplitDelay = old })
	for _, knows := range []bool{true, false} {
		config := filepath.Join(t.TempDir(), "claude-fixture")
		t.Setenv("CLAUDE_CONFIG_DIR", config)
		project := filepath.Join(config, "projects", "fixture")
		if err := os.MkdirAll(project, 0o700); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		at := now.Add(-30 * time.Hour)
		body := `{"type":"user","cwd":"/home/tester/code/shop","timestamp":"` + at.Format(time.RFC3339Nano) + `"}` + "\n" +
			claudeReply(at, "r1", "fixture-model", 1, 0, 0, 2)
		if err := os.WriteFile(filepath.Join(project, "sess.jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		in, answers := io.Pipe()
		since, before := now.Add(-48*time.Hour).Truncate(resplitStep), now.Add(-26*time.Hour).Truncate(resplitStep)
		w := resplitManager{answers: answers, since: since.Unix(), before: before.Unix(), knows: knows, requests: make(chan json.RawMessage, 4)}
		n := &Node{home: "/home/tester", dir: t.TempDir(), state: newState(), logger: log.New(io.Discard, "", 0)}
		n.peer = rpc.New("resplit-test", w)
		go func() { _ = n.peer.Serve(context.Background(), in) }()
		n.usageDirs.p.Store(newUsageDirs(n.home)) // as the meter does with a manager that keeps directories
		ready := make(chan struct{})
		close(ready)
		n.resplitUsage(context.Background(), ready)
		n.peer.Close(io.EOF)
		_ = answers.Close()
		if !knows {
			if len(w.requests) != 0 {
				t.Fatal("re-split sent to a manager that does not know it")
			}
			continue
		}
		select {
		case raw := <-w.requests:
			var req struct {
				Buckets []proto.UsageBucket `json:"buckets"`
				Through int64               `json:"through"`
				Done    bool                `json:"done"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			slot := at.Unix() / 600 * 600
			if !req.Done || req.Through != before.Unix()-600 || len(req.Buckets) != 1 || req.Buckets[0].Minute != slot ||
				req.Buckets[0].Dir != "~/code/shop" || req.Buckets[0].Output != 2 {
				t.Fatalf("request %s", raw)
			}
		default:
			t.Fatal("no re-split request")
		}
	}
}
