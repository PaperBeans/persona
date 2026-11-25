package kb

// The read path of recording_space.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

// HoldsRecording reports whether the knowledge base has recording id, kept or as a tombstone: an upload naming a
// track for it is refused, so one recording never lives in both places.
func (e *Engine) HoldsRecording(id string) bool {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if _, gone := st.tombstones()[id]; gone {
		return true
	}
	_, ok := st.Recording(id)
	return ok
}
