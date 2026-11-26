package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// transferCancelWait is how long DELETE waits for a cancelled transfer to stop (its target's upload aborted).
const transferCancelWait = 15 * time.Second

func (d *Devices) transferRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/v1/files/transfer", d.postTransfer)
	api.HandleFunc("GET /api/v1/files/transfer/{id}", d.getTransferHTTP)
	api.HandleFunc("DELETE /api/v1/files/transfer/{id}", d.deleteTransfer)
	api.HandleFunc("GET /api/v1/files/transfer-audit", d.getTransferAudit)
}

// fileTransferAuditReader reads the audit of the Agents' transfers (db.DB).
type fileTransferAuditReader interface {
	FileTransferAudits(ctx context.Context, machine string, limit int) ([]db.FileTransferAudit, error)
}

// getTransferAudit answers the latest Agents' transfers, newest first: {transfers: [db.FileTransferAudit]}, those of
// ?machine= (as either end) when given, at most ?limit= (1–500, default 100). Only a device the person signed in with
// reads it.
func (d *Devices) getTransferAudit(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		writeErr(w, http.StatusForbidden, errors.New("only a signed-in device can read the transfers' audit"))
		return
	}
	st, ok := d.opts.Store.(fileTransferAuditReader)
	if !ok {
		writeJSON(w, map[string]any{"transfers": []db.FileTransferAudit{}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := st.FileTransferAudits(r.Context(), strings.TrimSpace(r.URL.Query().Get("machine")), limit)
	if err != nil {
		apierr.Call(w, err)
		return
	}
	if list == nil {
		list = []db.FileTransferAudit{}
	}
	writeJSON(w, map[string]any{"transfers": list})
}

// postTransfer starts a computer to computer transfer (docs/protocol/file-transfer.md): 202 with the transfer, which
// GET then follows. The hub relays it here, to the manager of the source computer (devicewire.TransferRoutes); the
// target must be this manager's too. Only a device the person signed in with starts one: the hub lets no answer-only
// device or extension token through, and the role it passes on is checked again here.
func (d *Devices) postTransfer(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		writeErr(w, http.StatusForbidden, errors.New("only a signed-in device can send files between computers"))
		return
	}
	var req proto.FileTransferRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "bad_request", errors.New("bad JSON: "+err.Error()))
		return
	}
	switch {
	case strings.TrimSpace(req.From.Machine) == "" || strings.TrimSpace(req.To.Machine) == "":
		writeErr(w, http.StatusBadRequest, errors.New("from.machine and to.machine are required"))
		return
	case strings.TrimSpace(req.From.Path) == "":
		writeErr(w, http.StatusBadRequest, errors.New("from.path is required"))
		return
	case req.To.Dest != "" && req.To.Dest != proto.UploadDestInbox:
		writeErr(w, http.StatusBadRequest, errors.New("unknown to.dest"))
		return
	}
	from, to := d.machineByID(req.From.Machine), d.machineByID(req.To.Machine)
	if from == nil || to == nil {
		writeErr(w, http.StatusNotFound, errUnknownMachine)
		return
	}
	// Both ends must have file transfer on (transfer_settings.go): off, never set or unreadable is 403.
	for _, m := range []*Machine{from, to} {
		if !d.fileTransferOn(m.cfg.ID) {
			writeFileTransferOff(w, r, m)
			return
		}
	}
	t, err := d.startTransfer(r.Context(), from, to, req.From)
	if err != nil {
		var te *transferError
		if errors.As(err, &te) {
			body := apierr.Body(te.status, te.code, errors.New(i18n.T(i18n.FromRequest(r), te.key, te.args)))
			for k, v := range te.extra {
				body[k] = v
			}
			apierr.WriteBody(w, te.status, body)
			return
		}
		apierr.Call(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/files/transfer/"+t.ID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(t)
}

func (d *Devices) getTransferHTTP(w http.ResponseWriter, r *http.Request) {
	t, ok := d.getTransfer(r.PathValue("id"))
	if !ok {
		writeErrCode(w, http.StatusNotFound, "unknown_transfer", errors.New("no such transfer (it ended over an hour ago, or Persona restarted)"))
		return
	}
	writeJSON(w, t)
}

// deleteTransfer cancels a running transfer and answers how it stands; one that is over is left as it ended.
func (d *Devices) deleteTransfer(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		writeErr(w, http.StatusForbidden, errors.New("only a signed-in device can cancel a transfer"))
		return
	}
	t, ok := d.cancelTransfer(r.PathValue("id"), transferCancelWait)
	if !ok {
		writeErrCode(w, http.StatusNotFound, "unknown_transfer", errors.New("no such transfer (it ended over an hour ago, or Persona restarted)"))
		return
	}
	writeJSON(w, t)
}

// fullRole: the request came from a device that may do everything (assistantwire.RoleHeader, set by the hub; none: a
// test's own request).
func fullRole(r *http.Request) bool {
	role := r.Header.Get(assistantwire.RoleHeader)
	return role == "" || role == "full"
}
