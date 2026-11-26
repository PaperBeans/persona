package assistantwire

// kbRecordingRoutes are the knowledge base's own routes for a stored recording (the assistant's
// kb_recording_routes.go): they answer with or without the recording module.
var kbRecordingRoutes = []string{
	"DELETE /api/v1/kb/recordings/{id}",
	// Stored photos, read only (assistant/kb_recording_media.go).
	"GET /api/v1/recordings/{id}/photos",
	"GET /api/v1/recordings/{id}/photos/{n}",
}

func init() { Routes = append(Routes, kbRecordingRoutes...) }
