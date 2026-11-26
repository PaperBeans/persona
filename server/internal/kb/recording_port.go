package kb

import (
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The recordings' write side as the knowledge base reaches it (docs/plans/plan-kb-recording-port.md, section 3;
// docs/architecture/core-and-modules.md "A module that is off keeps what the user has"). The knowledge base reads,
// lists, searches, deletes and prunes recordings itself, with or without the recording module. What makes new data
// (the minutes, title, day recap and summary rounds, the sweep of hub transcripts after a prune, the audio writers)
// is the module's and is reached only through recordingwire.Provider: without the module the provider is
// recordingwire.NoProvider, so those calls do nothing and no stored file is touched.

// RecordingProvider is the recording module's write side for this knowledge base; never nil (recordingwire.NoProvider
// without the module).
func (e *Engine) RecordingProvider() recordingwire.Provider {
	if e.recordings == nil {
		return recordingwire.NoProvider{}
	}
	return e.recordings
}

// SetRecordingProvider gives the knowledge base the recording module's write side, before e is used (the module's
// hook, when e opens): the engine's rounds and the store's audio touch points go to p from then on.
func (e *Engine) SetRecordingProvider(p recordingwire.Provider) {
	e.recordings = p
	if e.Store != nil {
		e.Store.recordings.Store(&p)
	}
}

// recordingAudio is the audio writers' side of delete, retention and the trim (Provider.AudioDropped, AudioClosed):
// the module's once it hooked a knowledge base on this store, a no-op before.
func (s *Store) recordingAudio() recordingwire.Provider {
	if p := s.recordings.Load(); p != nil && *p != nil {
		return *p
	}
	return recordingwire.NoProvider{}
}

// RetentionWithoutRecordingModule is the one place that decides whether the retention the user configured
// (recording_days, minutes_days) keeps running when the recording module is off or left out of the build
// (docs/plans/plan-kb-recording-port.md, section 7, question 1; section 3.2 (ii)). The owner decided on 2026-11-25
// that it keeps running as configured: true, PruneRecordings runs in Core as it always did, so a module switch neither
// triggers nor stops a deletion the user asked for. false would pause retention with the module: recordings would then
// outlive recording_days while it is off.
const RetentionWithoutRecordingModule = true

// retentionRuns says whether this retention round runs: always with the module, and without it as
// RetentionWithoutRecordingModule decides.
func (e *Engine) retentionRuns() bool {
	return e.recordings != nil || RetentionWithoutRecordingModule
}

// PausedModuleOff is Detail.Paused and Entry.Paused of a recording whose stored state is in flight while nothing can
// settle it: the recording module is off.
const PausedModuleOff = "module_off"

// recordingPaused is what a response says about recording r's in-flight state (docs/plans/plan-kb-recording-port.md,
// section 5): PausedModuleOff when the recording module is absent and r's stored state waits for it (an upload not
// complete, minutes or a day recap pending, a hub transcription live or finishing), "" otherwise. Derived when read
// and never stored: meta.json and meeting.json stay as they are, and the module takes the recording up from the
// same stored fields when it is back. A read-only knowledge base says nothing: nothing settles there either way.
func (e *Engine) recordingPaused(r *Recording) string {
	if e.recordings != nil || r == nil || r.Trimmed() || e.Store == nil || e.Store.ReadOnly() {
		return ""
	}
	if t := hubTranscription(r); t != nil && !t.Done() {
		return PausedModuleOff
	}
	if !r.Complete || r.MinutesState == MinutesPending || r.RecapState == MinutesPending {
		return PausedModuleOff
	}
	return ""
}

// recordingEntryOf is recordingEntry with what only this knowledge base knows: the derived paused mark.
func (e *Engine) recordingEntryOf(r *Recording) Entry {
	en := recordingEntry(r)
	en.Paused = e.recordingPaused(r)
	return en
}

// afterPrune is the module's part of a retention round (Provider.AfterPrune).
func (e *Engine) afterPrune(now time.Time) { e.RecordingProvider().AfterPrune(now) }
