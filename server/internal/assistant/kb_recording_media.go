package assistant

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
)

// A stored recording's media, read only (docs/assistant/knowledge.md "Recordings without the recording module"): the
// photos a user attached to a meeting stay viewable when the recording module is off. The routes are the module's old
// ones, moved here so one implementation answers with the module and without it; they read kb.Engine.PhotosRead and
// PhotoImage and write nothing. Adding and deleting a photo stay the module's.
//
//	GET /api/v1/recordings/{id}/photos       the listed photos, oldest first; full credentials only;
//	                                         410 recording_deleted, 404 recording_not_found
//	GET /api/v1/recordings/{id}/photos/{n}   the stored image: its Content-Type, nosniff, private no-store; full
//	                                         credentials only; 410 recording_deleted, 404 recording_not_found,
//	                                         404 photo_not_found (also a photo deleted or a trim removed)
func (s *Service) kbRecordingMediaRoutes(engine func(http.ResponseWriter, *http.Request) *kb.Engine) {
	readEngine := func(w http.ResponseWriter, r *http.Request) *kb.Engine {
		if r.Method == http.MethodHead { // ServeMux sends HEAD here too; it is a read, like GET, on a read-only KB.
			r = r.Clone(r.Context())
			r.Method = http.MethodGet
		}
		return engine(w, r)
	}
	s.handle("GET /api/v1/recordings/{id}/photos", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-meeting.forbidden"))
			return
		}
		e := readEngine(w, r) // a read-only knowledge base answers it too (the engine refuses only changes)
		if e == nil {
			return
		}
		photos, _, err := e.PhotosRead(r.PathValue("id"))
		switch {
		case errors.Is(err, kb.ErrRecordingDeleted):
			apierr.WriteCode(w, http.StatusGone, codeRecordingDeleted, i18n.Err("assistant-meeting.deleted"))
			return
		case errors.Is(err, kb.ErrNotFound):
			apierr.WriteCode(w, http.StatusNotFound, "recording_not_found", i18n.Err("assistant-meeting.no_recording"))
			return
		case err != nil:
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"photos": photos})
	})
	s.handle("GET /api/v1/recordings/{id}/photos/{n}", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-meeting.forbidden"))
			return
		}
		e := readEngine(w, r) // a read-only knowledge base answers it too (the engine refuses only changes)
		if e == nil {
			return
		}
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n < 1 {
			n = 0
		}
		mime, data, err := e.PhotoImage(r.PathValue("id"), n)
		switch {
		case errors.Is(err, kb.ErrRecordingDeleted):
			apierr.WriteCode(w, http.StatusGone, codeRecordingDeleted, i18n.Err("assistant-meeting.deleted"))
			return
		case errors.Is(err, kb.ErrNotFound):
			apierr.WriteCode(w, http.StatusNotFound, "recording_not_found", i18n.Err("assistant-meeting.no_recording"))
			return
		case errors.Is(err, kb.ErrPhotoNotFound):
			apierr.WriteCode(w, http.StatusNotFound, "photo_not_found", i18n.Err("assistant-meeting.photo_not_found"))
			return
		case err != nil:
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		h := w.Header()
		h.Set("Content-Type", mime)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "private, no-store")
		_, _ = w.Write(data)
	})
	// Method-specific routes are more specific than these path-only fallbacks. With the module off, every method but
	// the Core GETs is absent (404). With it on, its POST/DELETE answer and other methods keep ServeMux's old 405.
	fallback := func(allow string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !s.recordingOn() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Allow", allow)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	}
	s.handleFallback("/api/v1/recordings/{id}/photos", fallback("GET, HEAD, POST"))
	s.handleFallback("/api/v1/recordings/{id}/photos/{n}", fallback("DELETE, GET, HEAD"))
}
