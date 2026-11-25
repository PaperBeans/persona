package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

type healthTellStore interface {
	InsertHealthTell(context.Context, db.HealthTell) (db.HealthTell, bool, error)
	HealthTellsPending(context.Context, int) ([]db.HealthTell, error)
	ReserveHealthTellTarget(context.Context, string, string, string) (db.HealthTell, error)
	ReserveHealthTellSession(context.Context, string, string) (string, error)
	SetHealthTellResult(context.Context, string, string, string, string, string, bool) (bool, error)
}

var _ healthTellStore = (*db.DB)(nil)

func (s *Service) healthTellRoutes() {
	s.handle("POST /api/v1/assistant/health/tell", s.healthTellCreate)
}

func (s *Service) healthTellCreate(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if !fullRole(r) && r.Header.Get(RoleHeader) != roleAnswer {
		apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-health_tell.forbidden"))
		return
	}
	st, ok := s.store().(healthTellStore)
	if !ok {
		apierr.Write(w, http.StatusServiceUnavailable, errors.New("Tell Health store unavailable"))
		return
	}
	var in assistantwire.HealthTell
	// Ignore unknown fields: no caller-supplied conversation, space or source can select a destination.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	if !utf8.ValidString(in.Text) {
		apierr.Write(w, http.StatusBadRequest, errors.New("text: invalid UTF-8"))
		return
	}
	in.Text = strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return -1
		}
		return c
	}, in.Text))
	if !captureClientID.MatchString(in.ClientID) || in.Text == "" || len(in.Text) > 8<<10 {
		apierr.Write(w, http.StatusBadRequest, errors.New("client_id: 8-128 letters, digits, dots, dashes, underscores or colons; text: 1-8192 bytes"))
		return
	}
	got, created, err := st.InsertHealthTell(r.Context(), db.HealthTell{ID: "tell_" + randomHex(8), ClientID: in.ClientID, Text: in.Text, Source: "shortcut"})
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusAccepted
		s.healthTells.kick()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(assistantwire.HealthTellReceipt{ID: got.ID, Status: got.Status, Duplicate: !created})
	s.logf("health tell %s received after %s", got.ID, time.Since(started))
}

const trainingChatKey = "health.training_chat"

type healthTrainingChat struct {
	Day       string `json:"day"`
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
}

func (s *Service) trainingChat(ctx context.Context, day string) (SessionRef, bool) {
	var v healthTrainingChat
	st := s.store()
	if st == nil {
		return SessionRef{}, false
	}
	ok, err := st.Setting(ctx, trainingChatKey, &v)
	if err != nil {
		s.logf("health tell: read training chat: %v", err)
		return SessionRef{}, false
	}
	if !ok || v.Day != day || v.MachineID != s.settings(ctx).MachineID || v.SessionID == "" {
		return SessionRef{}, false
	}
	if err := s.conversationAvailable(ctx, v.MachineID, v.SessionID); err != nil {
		return SessionRef{}, false
	}
	_, ok = s.taskSourceCwd(ctx, Task{Space: SpaceHealth, MachineID: v.MachineID, SessionID: v.SessionID})
	return SessionRef{MachineID: v.MachineID, SessionID: v.SessionID}, ok
}

func (s *Service) keepTrainingChat(ctx context.Context, day string, ref SessionRef, replace bool) {
	s.trainingChatMu.Lock()
	defer s.trainingChatMu.Unlock()
	st := s.store()
	if st == nil {
		return
	}
	var old healthTrainingChat
	_, err := st.Setting(ctx, trainingChatKey, &old)
	if err != nil {
		s.logf("health tell: read training chat: %v", err)
		return
	}
	if day < old.Day {
		return
	}
	if !replace {
		if _, ok := s.trainingChat(ctx, day); ok {
			return
		}
	}
	if err := st.PutSetting(ctx, trainingChatKey, healthTrainingChat{Day: day, MachineID: ref.MachineID, SessionID: ref.SessionID}); err != nil {
		s.logf("health tell: keep training chat: %v", err)
	}
}

const healthTellRetryInterval = 30 * time.Second

// A single worker preserves sentence order, including while the computer is offline or a send is uncertain.
func (s *Service) healthTellLoop(ctx context.Context) {
	st, ok := s.store().(healthTellStore)
	if !ok {
		return
	}
	tick := time.NewTicker(healthTellRetryInterval)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if s.healthTellRound(ctx, st) {
			s.healthTells.kick()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.healthTells.wakeup():
		case <-tick.C:
		}
	}
}

func (s *Service) healthTellRound(ctx context.Context, st healthTellStore) bool {
	newFailure := false
	defer func() {
		if newFailure {
			lang := s.lang(ctx)
			s.push(ctx, Notification{Title: i18n.T(lang, "push.health_title"), Body: i18n.T(lang, "push.health_tell_unconfirmed"), Track: SpaceHealth})
		}
	}()
	rows, err := st.HealthTellsPending(ctx, 32)
	if err != nil {
		s.logf("health tell: pending: %v", err)
		return false
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return false
		}
		advance, failed := s.deliverHealthTell(ctx, st, r)
		newFailure = newFailure || failed
		if !advance {
			return false
		}
	}
	return len(rows) == 32
}

func healthTellOffline(err error) bool {
	return errors.Is(err, apierr.ErrOffline) || errors.Is(err, ErrNoDevice)
}

// deliverHealthTell reports whether later sentences may proceed and whether this row newly failed.
func (s *Service) deliverHealthTell(ctx context.Context, st healthTellStore, r db.HealthTell) (advance, newFailure bool) {
	if time.Since(r.CreatedAt) > 24*time.Hour {
		return s.healthTellResult(ctx, st, r, assistantwire.HealthTellFailed, SessionRef{}, "expired", false)
	}
	if r.Attempts >= 5 {
		return s.healthTellResult(ctx, st, r, assistantwire.HealthTellFailed, SessionRef{}, "attempts exhausted", false)
	}
	if r.Attempts > 0 && time.Since(r.UpdatedAt) < healthTellRetryInterval {
		return false, false
	}
	machine := s.settings(ctx).MachineID
	if r.NewSessionID == "" && r.SessionID != "" {
		machine = r.MachineID
	}
	m, ok := s.device(ctx, machine)
	if machine == "" || !ok || !m.Online {
		return false, false
	}
	day := r.CreatedAt.In(s.location(ctx)).Format("2006-01-02")
	// Once reserved, always retry that start, even if its pointer was saved before the process stopped.
	if r.NewSessionID == "" {
		ref := SessionRef{MachineID: r.MachineID, SessionID: r.SessionID}
		if ref.SessionID == "" {
			if candidate, ok := s.trainingChat(ctx, day); ok {
				pinned, err := st.ReserveHealthTellTarget(ctx, r.ID, candidate.MachineID, candidate.SessionID)
				if err != nil {
					s.logf("health tell %s: reserve target: %v", r.ID, err)
					return false, false
				}
				r = pinned
				ref = SessionRef{MachineID: r.MachineID, SessionID: r.SessionID}
			}
		}
		if ref.SessionID != "" {
			s.UserSpoke(ctx, ref)
			params, err := s.SendParams(ctx, SendParams{MachineID: ref.MachineID, SessionID: ref.SessionID,
				Params: map[string]any{"session_id": ref.SessionID, "text": r.Text, "client_msg_id": "health-tell:" + r.ID}})
			if err == nil {
				err = s.sendOnce(ctx, ref.MachineID, params)
			}
			if err == nil {
				return s.healthTellDelivered(ctx, st, r, ref)
			}
			if healthTellOffline(err) {
				return false, false
			}
			var refusal *rpc.Error
			deleted := errors.As(err, &refusal) && refusal.Code == "conversation_deleted"
			if !taskSourceDefinitelyUnsent(err, ref.SessionID) && !deleted {
				return s.healthTellAttempt(ctx, st, r, err, false)
			}
		}
	}
	// A pinned continuation may belong to a former assistant computer. Only a definite refusal reaches here.
	if current := s.settings(ctx).MachineID; current != machine {
		machine = current
		m, ok = s.device(ctx, machine)
		if machine == "" || !ok || !m.Online {
			return false, false
		}
	}
	sid, err := st.ReserveHealthTellSession(ctx, r.ID, newGuestSessionID())
	if err != nil {
		s.logf("health tell %s: reserve: %v", r.ID, err)
		return false, false
	}
	sess, err := s.startUserSignedIn(ctx, StartParams{Text: r.Text, Space: SpaceHealth, MachineID: machine, NewSessionID: sid, Origin: OriginUser})
	if err != nil {
		if healthTellOffline(err) {
			return false, false
		}
		binding, bindErr := s.resolveModel(ctx, "assistant")
		claude := bindErr == nil && s.launcherAgent(ctx, machine, strings.TrimPrefix(binding.Provider, "launcher.")) == "claude"
		return s.healthTellAttempt(ctx, st, r, err, sendUnknown(err) && !claude)
	}
	ref := SessionRef{MachineID: sess.MachineID, SessionID: sess.SessionID}
	s.keepTrainingChat(ctx, day, ref, true)
	return s.healthTellDelivered(ctx, st, r, ref)
}

func (s *Service) healthTellDelivered(ctx context.Context, st healthTellStore, r db.HealthTell, ref SessionRef) (advance, newFailure bool) {
	s.noteReportTurn(ref.SessionID)
	return s.healthTellResult(ctx, st, r, assistantwire.HealthTellDelivered, ref, "", false)
}

func (s *Service) healthTellAttempt(ctx context.Context, st healthTellStore, r db.HealthTell, err error, unknownStart bool) (advance, newFailure bool) {
	status := assistantwire.HealthTellReceived
	if unknownStart || r.Attempts+1 >= 5 {
		status = assistantwire.HealthTellFailed
	}
	return s.healthTellResult(ctx, st, r, status, SessionRef{MachineID: r.MachineID, SessionID: r.SessionID}, err.Error(), true)
}

func (s *Service) healthTellResult(ctx context.Context, st healthTellStore, r db.HealthTell, status string, ref SessionRef, reason string, tried bool) (advance, newFailure bool) {
	changed, err := st.SetHealthTellResult(ctx, r.ID, status, ref.MachineID, ref.SessionID, reason, tried)
	if err != nil {
		s.logf("health tell %s: record result: %v", r.ID, err)
		return false, false
	}
	if changed && status == assistantwire.HealthTellDelivered {
		s.logf("health tell %s delivered after %s", r.ID, time.Since(r.CreatedAt))
	}
	return status != assistantwire.HealthTellReceived, changed && status == assistantwire.HealthTellFailed
}

// startUserSignedIn is a background handoff of the user's words, sharing Start's protection and naming.
func (s *Service) startUserSignedIn(ctx context.Context, p StartParams) (proto.Session, error) {
	return s.startUserWith(ctx, p, s.startSignedIn)
}
