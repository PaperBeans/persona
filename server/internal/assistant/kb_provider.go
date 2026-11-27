package assistant

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Match the installation before opening the archive. An unselected provider sees
// the same error for every chat, account, date and import state.
func (s *Service) kbProviderBinding(ctx context.Context, req assistantwire.KBProviderRequest) (assistantwire.KBImportProvider, *assistantwire.KBHistory, bool) {
	set := s.kbSettings(ctx)
	if set == nil || set.Off || set.ImportProvider == nil || !set.History.On() {
		return assistantwire.KBImportProvider{}, nil, false
	}
	b := *set.ImportProvider
	stamp, err := time.Parse(time.RFC3339Nano, req.InstalledAt)
	if err != nil || stamp.IsZero() || !b.InstalledAt.Equal(stamp) || b.Extension == "" || b.Extension != req.Extension ||
		b.StorageID == "" || b.StorageID != req.StorageID || b.Connector == "" || b.SourceID == "" || b.FiledAs == "" {
		return assistantwire.KBImportProvider{}, nil, false
	}
	return b, set.History, true
}

func (s *Service) kbProviderRead(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	denied := &rpc.Error{Code: "permission_denied", Message: "knowledge import selection unavailable"}
	var env assistantwire.KBProviderRequest
	if err := kbDecodeParams(method, raw, &env); err != nil {
		return nil, err
	}
	b, history, ok := s.kbProviderBinding(ctx, env)
	if !ok || s.ui == nil {
		return nil, denied
	}
	query := env.Params
	checkpointOnly := method == assistantwire.MethodKBProviderCheckpoint
	if checkpointOnly {
		if query != (assistantwire.KBImportQuery{}) {
			return nil, kbInvalidParams(method, "expected an empty checkpoint query")
		}
	} else {
		if _, err := time.Parse(time.DateOnly, query.Day); err != nil || query.After < 0 || query.Approval == "" {
			return nil, kbInvalidParams(method, "invalid day, approval or starting line")
		}
	}
	engine, err := s.kbGet(ctx)
	if err != nil {
		return nil, denied
	}
	cp, ok := s.ui.ProviderCheckpoint(engine, history, b, query.Day, time.Now())
	if !ok {
		return nil, denied
	}
	if checkpointOnly {
		current, _, ok := s.kbProviderBinding(ctx, env)
		if !ok || current != b {
			return nil, denied
		}
		return cp, nil
	}
	if cp.Approval != query.Approval {
		return nil, denied
	}
	day, err := s.kbDaySent(ctx, engine, assistantwire.KBDayParams{ID: kb.ChatID(b.FiledAs, string(b.SourceID)), Day: query.Day})
	if err != nil || day.SentOrigin != "available" {
		return nil, &rpc.Error{Code: "unavailable", Message: "sent markers unavailable"}
	}
	// The ledger lookup can span a change to the selected account, retention or plan.
	current, history, ok := s.kbProviderBinding(ctx, env)
	if !ok || current != b {
		return nil, denied
	}
	fresh, ok := s.ui.ProviderCheckpoint(engine, history, b, query.Day, time.Now())
	if !ok || fresh.Approval != cp.Approval {
		return nil, denied
	}
	return kbMarkerPage(day.Messages, query.After), nil
}

func kbMarkerPage(messages []kb.DayMessage, after int) wire.KBSentMarkersResult {
	const pageSize = 100
	result := wire.KBSentMarkersResult{Markers: []wire.KBSentMarker{}}
	for _, line := range messages {
		if line.N <= after || line.SentBy != "assistant" {
			continue
		}
		if len(result.Markers) == pageSize {
			result.NextAfter = result.Markers[pageSize-1].N
			break
		}
		marker := wire.KBSentMarker{N: line.N, SentBy: line.SentBy}
		if line.RecalledAt != nil {
			marker.RecalledAt = *line.RecalledAt
		}
		result.Markers = append(result.Markers, marker)
	}
	return result
}
