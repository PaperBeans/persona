package assistant

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/kb"
)

// A stored recording's delete, the knowledge base's own (docs/assistant/knowledge.md "Recordings without the
// recording module"): a recording the user already has stays listed, readable and searchable when the recording
// module is off, and stays theirs to remove. The module's DELETE /api/v1/recordings/{id} (the phone's, which also
// answers for a recording saved into a track) is gone with the module; this route is always there.
//
//	DELETE /api/v1/kb/recordings/{id}   {"deleted": bool}; 400 for an id that is no UUID, 403 kb_read_only
func (s *Service) kbRecordingRoutes(engine func(http.ResponseWriter, *http.Request) *kb.Engine) {
	s.handle("DELETE /api/v1/kb/recordings/{id}", func(w http.ResponseWriter, r *http.Request) {
		e := engine(w, r)
		if e == nil {
			return
		}
		deleted, err := e.DeleteRecording(r.PathValue("id"), time.Now())
		switch {
		case errors.Is(err, kb.ErrBadRecording):
			apierr.Write(w, http.StatusBadRequest, err)
			return
		case err != nil:
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		if deleted {
			// With the module: its room, audio socket and job go with it, as after the module's own delete (both are
			// no-ops without the module).
			go s.meetingRoomsSweep(context.WithoutCancel(r.Context()), e)
			s.hubSweep(e)
		}
		apierr.WriteJSON(w, map[string]any{"deleted": deleted})
	})
}
