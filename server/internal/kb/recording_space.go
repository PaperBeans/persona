package kb

// CleanRecordingUpload checks upload u as PutRecording does and puts its segments in order (empty ones dropped, Persona's echo
// marked), without storing anything: for a recording the manager saves into a track instead (#13501, Space).
func CleanRecordingUpload(u *RecordingUpload) error { return cleanUpload(u) }
