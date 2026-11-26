package kb

// The read path of recording_commit.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
	"fmt"
	"os"
)

const recordingJournal = ".recording-undo.json"

var recordingFiles = [...]string{"meta.json", "segments.json", "transcript.md"}

// A durable undo snapshot precedes any replacement. Removing it commits all three
// files. One writable Store owns the directory; recFilesMu protects its readers.
// A pending journal is also a read overlay, so a failed rollback cannot expose
// rejected text. Writable open restores it before making the Store available.
type recordingUndo struct {
	Version int               `json:"version"`
	ID      string            `json:"id"`
	Files   map[string][]byte `json:"files"` // nil means the file did not exist
}

func (s *Store) recordingSnapshot(id string) (map[string][]byte, error) {
	if s.recordingReadWait != nil {
		s.recordingReadWait()
	}
	s.recFilesMu.RLock()
	defer s.recFilesMu.RUnlock()
	undo, err := s.readRecordingUndo(id)
	if err != nil {
		return nil, err
	}
	if undo != nil {
		return undo.Files, nil
	}
	return s.recordingDiskSnapshot(id)
}

// Single-file readers avoid loading an entire transcript just to list metadata.
func (s *Store) recordingRead(id, name string) ([]byte, error) {
	if s.recordingReadWait != nil {
		s.recordingReadWait()
	}
	s.recFilesMu.RLock()
	defer s.recFilesMu.RUnlock()
	undo, err := s.readRecordingUndo(id)
	if err != nil {
		return nil, err
	}
	if undo != nil {
		return undo.Files[name], nil
	}
	raw, err := os.ReadFile(s.path(KindRecording, id, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return raw, err
}

func (s *Store) readEntityJSON(kind, id string, out any) (bool, error) {
	if kind != KindRecording {
		return readJSON(s.entityPath(kind, id), out)
	}
	raw, err := s.recordingRead(id, "meta.json")
	if err != nil || raw == nil {
		return false, err
	}
	return true, json.Unmarshal(raw, out)
}

// Freeze search metadata and rendered text together. A response must not pair
// hits from one accepted revision with a name or revision from another.
func (s *Store) recordingSearchView() map[string]map[string][]byte {
	if s.recordingReadWait != nil {
		s.recordingReadWait()
	}
	s.recFilesMu.RLock()
	defer s.recFilesMu.RUnlock()
	view := map[string]map[string][]byte{}
	for _, id := range s.IDs(KindRecording) {
		undo, err := s.readRecordingUndo(id)
		if err != nil {
			continue
		}
		if undo != nil {
			view[id] = undo.Files
			continue
		}
		files := map[string][]byte{}
		for _, name := range []string{"meta.json", "transcript.md"} {
			raw, err := os.ReadFile(s.path(KindRecording, id, name))
			if err == nil {
				files[name] = raw
			}
		}
		view[id] = files
	}
	return view
}

func (s *Store) recordingDiskSnapshot(id string) (map[string][]byte, error) {
	files := make(map[string][]byte, len(recordingFiles))
	for _, name := range recordingFiles {
		raw, err := os.ReadFile(s.path(KindRecording, id, name))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		files[name] = raw
	}
	return files, nil
}

func (s *Store) readRecordingUndo(id string) (*recordingUndo, error) {
	var undo recordingUndo
	ok, err := readJSON(s.path(KindRecording, id, recordingJournal), &undo)
	if err != nil || !ok {
		return nil, err
	}
	if undo.Version != 1 || undo.ID != id || len(undo.Files) != len(recordingFiles) {
		return nil, fmt.Errorf("invalid recording recovery for %s", id)
	}
	for _, name := range recordingFiles {
		if _, ok := undo.Files[name]; !ok {
			return nil, fmt.Errorf("incomplete recording recovery for %s", id)
		}
	}
	return &undo, nil
}
