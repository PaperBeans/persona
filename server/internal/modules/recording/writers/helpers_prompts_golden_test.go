package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"context"
	"encoding/json"
	"github.com/nwverse/persona/server/internal/kb"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "prompts", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (UPDATE_GOLDEN=1 writes it)", name, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

type captureModel struct {
	mu     sync.Mutex // the knowledge points steps call it topicWorkers at a time
	reply  string
	suffix string
	calls  [][2]string // system, user
}

func (m *captureModel) JSONMax(ctx context.Context, system, user string, maxTokens int, out any) error {
	m.mu.Lock()
	m.calls = append(m.calls, [2]string{system, user})
	m.mu.Unlock()
	return json.Unmarshal([]byte(m.reply), out)
}

func (m *captureModel) golden(t *testing.T, name string) {
	t.Helper()
	if len(m.calls) != 1 {
		t.Fatalf("%s: %d model calls, want 1", name, len(m.calls))
	}
	golden(t, name+m.suffix+".system", m.calls[0][0])
	golden(t, name+m.suffix+".user", m.calls[0][1])
	m.calls = nil
}

// goldenZone is the user's time zone in the golden tests: the archive is written in it, and the times below are built in
// it, so the goldens read the same on a hub in UTC and on a laptop in Shanghai.
var goldenZone = func() *time.Location {
	l, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return l
}()

func goldenEngine(t *testing.T, m kb.Model) *testEngine {
	t.Helper()
	st, err := kb.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Rezone(goldenZone); err != nil { // the user's zone, whatever the machine's
		t.Fatal(err)
	}
	return newTestEngine(&kb.Engine{Store: st, Model: m, Owner: "示例用户", Logf: func(string, ...any) {}})
}
