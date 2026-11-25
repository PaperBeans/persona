package recordingwire

import (
	"context"
	"time"
)

// Provider is the write side of recordings as the knowledge base reaches it (docs/plans/plan-kb-recording-port.md,
// section 3.1): the background rounds and the audio writers' two touch points with retention and delete. It is the
// recording module's; the knowledge base holds NoProvider when the module is left out of the build or switched off.
// Reading never goes through it: listing, opening and searching what is stored are the knowledge base's own, module
// or not (docs/architecture/core-and-modules.md "A module that is off keeps what the user has").
type Provider interface {
	// The background rounds, each at most max recordings a call, returning how many it wrote.
	WriteMinutes(ctx context.Context, max int) int
	TitleRecordings(ctx context.Context, max int) int
	WriteDayRecaps(ctx context.Context, max int) int
	SummarizeRecordings(ctx context.Context, max int) int
	// AfterPrune is the module's part of the retention round, after the knowledge base's own: the sweep of hub
	// transcripts (it mends what a crash left of one, makes minutes again after a final pass, drops audio).
	AfterPrune(now time.Time)
	// AudioDropped notes that recording id's audio directory is being removed, why; the caller holds the store's
	// recording files lock for writing. AudioClosed closes the audio file id has open for writing; the caller holds no
	// lock of the store.
	AudioDropped(id string, why error)
	AudioClosed(id string)
}

// NoProvider is the Provider of a knowledge base without the recording module: every round writes nothing and
// answers 0 (no model call, no file touched), and there is no audio writer to tell.
type NoProvider struct{}

func (NoProvider) WriteMinutes(context.Context, int) int        { return 0 }
func (NoProvider) TitleRecordings(context.Context, int) int     { return 0 }
func (NoProvider) WriteDayRecaps(context.Context, int) int      { return 0 }
func (NoProvider) SummarizeRecordings(context.Context, int) int { return 0 }
func (NoProvider) AfterPrune(time.Time)                         {}
func (NoProvider) AudioDropped(string, error)                   {}
func (NoProvider) AudioClosed(string)                           {}
