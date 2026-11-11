package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/db"
)

// The meeting conversation's sends keep operation receipts (docs/assistant/meeting-minutes.md "Flags and
// compatibility": "Retries"): Just me and the follow-up derive theirs from the body's id, as Lark sends derive theirs
// from client_id (reply_receipts.go). The digest covers the recording (the path) and the whole body; the result is
// the manager's response, which holds no text of the message. A follow-up's call that a restart left unknown runs
// again (rerunClientOp): its creation record and followup-<id> make nothing twice.

const (
	meetingJustMeOp   = "meeting_just_me"
	meetingFollowupOp = "meeting_followup"
)

// meetingReceiptRoutes are the relayed routes with a receipt, and the receipt's kind.
var meetingReceiptRoutes = map[string]string{
	"POST /api/v1/recordings/{id}/just-me":  meetingJustMeOp,
	"POST /api/v1/recordings/{id}/followup": meetingFollowupOp,
}

// withBodyIDReceipt gives a route whose body names its call by id (a UUID) a receipt in client_ops, under an operation
// id derived from kind and that id. A body without one goes on as it is (the manager answers it 400).
func (h *Hub) withBodyIDReceipt(kind string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.store == nil {
			next(w, r)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
		_ = r.Body.Close()
		if err != nil {
			writeBodyErr(w, err, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var in struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &in) == nil && clientOpUUID.MatchString(strings.TrimSpace(in.ID)) {
			sum := sha256.Sum256([]byte(compat.ReplyHashPrefix() + kind + ":" + strings.ToLower(strings.TrimSpace(in.ID))))
			sum[6], sum[8] = sum[6]&0x0f|0x80, sum[8]&0x3f|0x80
			r = r.Clone(context.WithoutCancel(r.Context()))
			r.Header.Set("X-Persona-Op-Id", fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16]))
			h.withClientOp(kind, next)(w, r)
			return
		}
		next(w, r)
	}
}

// clientOpRerunner takes back an operation a restart left unknown (db.DB): its call runs again.
type clientOpRerunner interface {
	ReclaimClientOp(ctx context.Context, id string) (bool, error)
}

// rerunClientOp takes back a follow-up's operation a restart left unknown, so its call runs again (its creation record
// and followup-<id> make nothing twice); false for any other, or when another call took it first.
func (h *Hub) rerunClientOp(op *db.ClientOp) bool {
	if op.Kind != meetingFollowupOp || op.State != "unknown" {
		return false
	}
	re, ok := h.store.(clientOpRerunner)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	taken, err := re.ReclaimClientOp(ctx, op.ID)
	return err == nil && taken
}
