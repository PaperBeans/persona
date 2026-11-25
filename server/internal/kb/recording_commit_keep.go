package kb

// The writes of recording_commit.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func syncRecordingDir(dir string) {
	// Directory syncing is best effort, as in managerconf.WriteFileAtomic (Windows
	// and some filesystems do not support it). File contents are synced before rename.
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}

// Keep the existing temporary-file name as well as the public file layout.
func writeRecordingFile(path string, data []byte) error {
	f, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	syncRecordingDir(filepath.Dir(path))
	return nil
}

// Requires recFilesMu. Recovery deliberately bypasses candidate fault injection
// and uses unique temporary files, so a leftover upload temp cannot prevent undo.
func (s *Store) recoverRecordingLocked(id string) error {
	undo, err := s.readRecordingUndo(id)
	if err != nil || undo == nil {
		return err
	}
	for _, name := range recordingFiles {
		path := s.path(KindRecording, id, name)
		if raw := undo.Files[name]; raw != nil {
			if err := archiveWrite(path, raw); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Remove(s.path(KindRecording, id, recordingJournal)); err != nil {
		return err
	}
	syncRecordingDir(s.path(KindRecording, id))
	return nil
}

func (s *Store) recoverRecordings() error {
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	for _, id := range s.IDs(KindRecording) {
		if err := s.recoverRecordingLocked(id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) commitRecording(r *Recording, segs []RecordingSegment) (err error) {
	s.recFilesMu.Lock()
	s.metaMu.Lock()
	defer func() { s.metaMu.Unlock(); s.recFilesMu.Unlock() }()
	if err := s.recoverRecordingLocked(r.ID); err != nil {
		return err
	}
	before, err := s.recordingDiskSnapshot(r.ID)
	if err != nil {
		return err
	}
	current := map[string]json.RawMessage{}
	if before["meta.json"] != nil {
		if err := json.Unmarshal(before["meta.json"], &current); err != nil {
			return err
		}
	}
	incoming := protectedEntity(r, &r.Tagging, current, false)
	meta, err := json.MarshalIndent(incoming, "", "  ")
	if err != nil {
		return err
	}
	segments, err := json.MarshalIndent(nonNilSegments(segs), "", "  ")
	if err != nil {
		return err
	}
	// transcript.md is the transcript of record: in hub mode the hub's sentences too (hub_segments.json is not
	// journalled: recMu, which the caller holds, keeps it as it is meanwhile).
	transcript := []byte(s.renderTranscript(r, s.recordLocked(r, segs)))
	undo, err := json.Marshal(recordingUndo{Version: 1, ID: r.ID, Files: before})
	if err != nil {
		return err
	}
	write := s.recordingWrite
	if write == nil {
		write = writeRecordingFile
	}
	// Journal writes use the same seam, with the production durable implementation.
	journal := s.path(KindRecording, r.ID, recordingJournal)
	if s.recordingWrite == nil {
		err = archiveWrite(journal, undo)
	} else {
		err = write(journal, undo)
	}
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.recoverRecordingLocked(r.ID))
		}
	}()
	for _, file := range []struct {
		name string
		raw  []byte
	}{
		{"segments.json", append(segments, '\n')}, {"transcript.md", transcript}, {"meta.json", append(meta, '\n')},
	} {
		if err = write(s.path(KindRecording, r.ID, file.name), file.raw); err != nil {
			return err
		}
	}
	remove := s.recordingRemove
	if remove == nil {
		remove = os.Remove
	}
	if err = remove(journal); err != nil {
		return err
	}
	syncRecordingDir(s.path(KindRecording, r.ID))
	conv := r.Conv // meeting.json's, never in meta.json
	*r = Recording{}
	_ = json.Unmarshal(meta, r)
	r.Persisted, r.Conv = true, conv
	return nil
}
