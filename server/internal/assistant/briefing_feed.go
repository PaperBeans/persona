package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/modapi"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The `brief` item's body and writer live in server/internal/modules/briefing/feed.go. These HTTP routes stay in
// package assistant until modapi serves the hub_routes slot (#13845 item 4).

const feedBrief = modapi.FeedKindBrief

type (
	briefBody           = modapi.BriefBody
	briefBodyNorthStar  = modapi.BriefBodyNorthStar
	briefBodyRow        = modapi.BriefBodyRow
	briefBodyPRFollowup = modapi.BriefPRFollowup
)

func (s *Service) briefRoutes() {
	// POST /api/v1/later/{id}/handled: the Handled button of a question in a `brief` item's later list. It leaves the queue
	// (an ask_user nobody answered ends with it: its to-do is closed, laterHandled); a question already handled, dropped (its
	// to-do ended, or 7 days) or unknown is 409. A queue, or such a to-do, that can't be read or written is 503, and the
	// question stays.
	s.handle("POST /api/v1/later/{id}/handled", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change the queue"))
			return
		}
		ok, err := s.laterHandled(r.Context(), r.PathValue("id"), time.Now())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, i18n.Err("http-status.unavailable"))
			return
		}
		if !ok {
			apierr.Write(w, http.StatusConflict, i18n.Err("assistant-feishu_cards.already_handled"))
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})

	// POST /api/v1/todos/{id}/accept {"asked_at"}: the Accept button of a `brief` item. It verifies the task when asked_at is
	// the round that waits now; a button of an earlier round, or a task already verified, is 409. A decision whose
	// recommendation could not be sent to the task's agent is 502 accept_not_taken (423 with the node's code when the node
	// refused it, such as dialog_open, #7343), and asks that cannot be read or an acceptance that
	// cannot be saved is 503; either way nothing was taken (conclusion.go, errAcceptStore). A send whose answer may have
	// reached the agent, or did before what followed failed, is 502 send_in_doubt (todo_write_outcome.go). A decision also takes
	// {"option"}: the label of the option chosen (the recommended one is what the plain press takes; no such option is
	// 400), or {"response"}: decide_yourself, not_what_i_asked or explain_first, which takes nothing and goes to the
	// task's supervisor (decision.go; any other value is 400).
	s.handle("POST /api/v1/todos/{id}/accept", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change todos"))
			return
		}
		var in struct {
			AskedAt  string            `json:"asked_at"`
			Option   string            `json:"option"`
			Response string            `json:"response"`
			Answers  map[string]string `json:"answers"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || in.AskedAt == "" {
			apierr.Write(w, http.StatusBadRequest, errors.New("asked_at is required"))
			return
		}
		var result acceptResult
		var err error
		switch {
		case isAskTodo(r.PathValue("id")):
			if in.Response != "" {
				apierr.Write(w, http.StatusBadRequest, errDecisionOption)
				return
			}
			result, err = s.chooseExpiredQuestion(r.Context(), r.PathValue("id"), in.AskedAt, in.Option, in.Answers)
		case in.Answers != nil:
			apierr.Write(w, http.StatusBadRequest, errDecisionOption)
			return
		case in.Response != "" && in.Option != "":
			apierr.Write(w, http.StatusBadRequest, errors.New("give option or response, not both"))
			return
		case in.Response != "":
			if !slices.Contains(ownerResponses, in.Response) {
				apierr.Write(w, http.StatusBadRequest, errors.New("response must be decide_yourself, not_what_i_asked or explain_first"))
				return
			}
			result.taken, err = s.ownerCannotAnswer(r.Context(), r.PathValue("id"), in.AskedAt, in.Response)
		case in.Option != "":
			result, err = s.chooseOption(r.Context(), r.PathValue("id"), in.AskedAt, in.Option)
		default:
			result, err = s.acceptTryFromBrief(r.Context(), r.PathValue("id"), in.AskedAt)
		}
		if errors.Is(err, errDecisionOption) {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if errors.Is(err, errAcceptStore) { // nothing accepted: the button works again once the store answers
			apierr.Write(w, http.StatusServiceUnavailable, i18n.Err("http-status.unavailable"))
			return
		}
		if isInvestigationGuardError(err) {
			// Unconfirmed or open investigation findings block closing the task, not Agent delivery.
			// Use 422 so older apps keep the button retryable (unlike 409) and do not show a 502 delivery failure.
			writeTypedNothing(w, http.StatusUnprocessableEntity, "investigation_findings_pending", err)
			return
		}
		if delivered := writeDelivered(err); err != nil && delivered != "none" {
			// The words may be with the Agent already, or are (todo_write_outcome.go): a failure every app shows, never
			// "nothing was changed", so the owner looks before taking it again. The button works again (the round still
			// waits), as for any 502.
			writeInDoubt(w, http.StatusBadGateway, err, delivered, answerInDoubtKeys)
			return
		}
		if nodeRefusal(err) {
			// The node refused with a code it words itself (a dialog open in the terminal, a draft in its input box):
			// that code and the node's details, so the app can say what to do first instead of "try again later".
			// Nothing was taken; the button works again (#7343). Not the 409 every other node call answers
			// (apierr.Call): on this route the apps already installed (iOS, Android, older web pages) read any 409 as
			// "this round is over" and lock the button, though nothing reached the agent. 423 Locked (the terminal is
			// held by the dialog) is a failure to every one of them, which shows the hub's sentence and keeps the button.
			writeNodeRefusal(w, err)
			return
		}
		if err != nil { // another delivery failure: nothing typed or taken, the button works again
			// In the request's language, the reason too when the hub words it (an offline computer, no agent).
			writeTypedNothing(w, http.StatusBadGateway, "accept_not_taken", i18n.Err("assistant-reports.accept_not_taken",
				i18n.Args{"error": clip(oneLine(i18n.Localize(err, s.requestCtxLang(r.Context()))), 120)}))
			return
		}
		if !result.taken {
			apierr.Write(w, http.StatusConflict, i18n.Err("assistant-feishu_cards.already_handled"))
			return
		}
		body := map[string]any{"ok": true}
		if result.undelivered != nil {
			body["delivered"] = "none"
		}
		apierr.WriteJSON(w, body)
	})
}

// nodeRefusal is err a node's coded refusal (*rpc.Error with a code other than a transport failure's): nothing was
// typed, and its code says why.
func nodeRefusal(err error) bool {
	var re *rpc.Error
	if !errors.As(err, &re) {
		return false
	}
	switch re.Code {
	case "", "internal", "node_unreachable", "timeout":
		return false
	}
	return true
}
