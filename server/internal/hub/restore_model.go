package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// POST /api/v1/sessions/{mid}/{sid}/restore-model (#2528): "back to the configured model" for a session Claude Code
// moved to another model after an upstream refusal. A relay: the node closes the session's idle headless process
// only, so its next start uses the configured model (node/model_restore.go); it sends no message, replays nothing and
// keeps the history, todos and permission mode. No body is read: only the session id reaches the node, which reads
// the configured model itself. Closing a process is a full device's control, as rewinding is: an answer-only device
// and an extension's token are refused here and by the lists of what they may call. The node's answer passes through
// as it is — {restore: "next_start", restore_to: the configured model it read, closed: whether an idle process was
// closed} — which is no proof the configured model runs; its refusals (restore_busy, restore_unsupported,
// restore_nothing, restore_unknown, restore_gone) are 409 with their code and words.

// restoreModelTimeout: the node never waits on the session (it answers busy), only on its file reads and the hub.
const restoreModelTimeout = 30 * time.Second

func (h *Hub) restoreSessionModel(w http.ResponseWriter, r *http.Request, m *Machine) {
	if principalOf(r.Context()).Role != roleFull {
		writeErr(w, http.StatusForbidden, errors.New("going back to the configured model needs a full device"))
		return
	}
	sid := r.PathValue("sid")
	if paneAddress(sid) {
		writeErr(w, http.StatusBadRequest, errors.New("a terminal opened by pane is not a session"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), restoreModelTimeout)
	defer cancel()
	var out json.RawMessage
	if err := m.call(ctx, "session.restore_model", map[string]any{"session_id": sid}, &out); err != nil {
		var re *rpc.Error
		if errors.As(err, &re) && strings.HasPrefix(re.Message, "unknown method") {
			writeErrCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.Err("hub-restoremodel.outdated"))
			return
		}
		callErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if len(out) == 0 {
		out = json.RawMessage(`{}`)
	}
	_, _ = w.Write(out)
}
