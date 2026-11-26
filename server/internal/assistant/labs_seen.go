package assistant

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

const maxLabsSeen = 64

var (
	labsID           = lazyre.New(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	errLabsSeenLimit = i18n.Err("assistant-profile.too_many_labs_seen")
)

func normalizeLabsSeen(ids []string) ([]string, error) {
	if len(ids) > maxLabsSeen {
		return nil, errLabsSeenLimit
	}
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if !labsID.MatchString(id) {
			return nil, i18n.Err("assistant-profile.bad_labs_seen_id")
		}
		if !slices.Contains(clean, id) {
			clean = append(clean, id)
		}
	}
	return clean, nil
}

// applyLabsSeen runs under the same settings write lock as other changes to the assistant's row.
func applyLabsSeen(v *Settings, ids []string) (bool, error) {
	var added []string
	for _, id := range ids {
		if !slices.Contains(v.LabsSeen, id) {
			added = append(added, id)
		}
	}
	if len(added) == 0 {
		return false, nil
	}
	if len(v.LabsSeen)+len(added) > maxLabsSeen {
		return false, errLabsSeenLimit
	}
	v.LabsSeen = append(slices.Clone(v.LabsSeen), added...)
	slices.Sort(v.LabsSeen)
	return true, nil
}

// A receipt-only write does not run the hooks for behavior changes, such as rescheduling supervision.
func (s *Service) putLabsSeen(w http.ResponseWriter, ctx context.Context, ids []string) {
	if len(ids) > 0 {
		var changed bool
		saved, err := s.updateSettings(ctx, func(v *Settings) error {
			var err error
			changed, err = applyLabsSeen(v, ids)
			return err
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errLabsSeenLimit) {
				status = http.StatusBadRequest
			}
			apierr.Write(w, status, err)
			return
		}
		if changed {
			s.publishLabsSeen(ctx, saved.LabsSeen)
		}
	}
	apierr.WriteJSON(w, s.view(ctx, false))
}

func (s *Service) publishLabsSeen(ctx context.Context, ids []string) {
	s.broadcastEvent(context.WithoutCancel(ctx), assistantwire.LabsSeenEvent{Type: assistantwire.LabsSeenEventType, LabsSeen: ids})
}
