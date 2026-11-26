package kb

import (
	"context"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The one recording round whose code is the knowledge base's own (docs/plans/plan-kb-recording-port.md, "PR 5 has
// landed"): the summary round (recordings_summary.go). The recording module gives it to a knowledge base in its hook as
// part of its provider, so it runs only with the module; nothing in the Core names it.

// RecordingWriters is the summary round of engine e (recordingwire.WritersInCore). Every other writer is the module's
// (internal/modules/recording/writers), whose provider carries this one.
func RecordingWriters(e *Engine) recordingwire.WritersInCore { return recordingWriters{e: e} }

type recordingWriters struct{ e *Engine }

func (w recordingWriters) SummarizeRecordings(ctx context.Context, max int) int {
	return w.e.SummarizeRecordings(ctx, max)
}
