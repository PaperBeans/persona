package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"context"
	"errors"
	"os"
	"testing"
)

// trimModel fails the test when called: nothing is summarized or made of a trimmed recording.
type trimModel struct{ t *testing.T }

func (m trimModel) JSONMax(context.Context, string, string, int, any) error {
	m.t.Error("a model call for a trimmed recording")
	return errors.New("no call expected")
}

func recordingFileNames(t *testing.T, e *testEngine, id string) []string {
	t.Helper()
	files, err := os.ReadDir(e.Store.Path(KindRecording, id))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	return names
}
