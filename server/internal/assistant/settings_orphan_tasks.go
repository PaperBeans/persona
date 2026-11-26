package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	approvalops "github.com/nwverse/persona/server/internal/approval"
	"github.com/nwverse/persona/server/internal/i18n"
)

// orphanTaskOwnSetting exposes only a saved new-namespace override whose task
// no longer exists. Its sole write is persona_settings value null: clear the
// override and inherit Main chat. It never resurrects the retired model_config
// record or permits a new binding for a nonexistent task.
func (s *Service) orphanTaskOwnSetting(ctx context.Context, key string) (ownSetting, bool, error) {
	id := strings.TrimPrefix(key, modelKeyPrefix)
	if key != modelKey(id) || !isTaskPurpose(id) || strings.TrimPrefix(id, "task.") == "" {
		return ownSetting{}, false, nil
	}
	orphan, err := s.orphanTaskID(ctx, id)
	if err != nil || !orphan || s.store() == nil {
		return ownSetting{}, false, err
	}
	var record modelRecord
	found, err := s.store().Setting(ctx, modelRecordKey(id), &record)
	if err != nil || !found || record.Binding == (ModelBinding{}) {
		return ownSetting{}, false, err
	}
	setting := ownSetting{Setting: Setting{Key: key, Title: i18n.T(s.lang(ctx), "assistant-settings_tool.title_orphan_task"),
		Actions: []string{"get", "set"}, About: "a saved model override for a task that no longer exists; get reads it, set null clears it to inherit Main chat; non-empty new bindings are refused",
		Value: "null clears the orphaned override; no other value is accepted"}}
	setting.get = func(ctx context.Context) (string, error) {
		var current modelRecord
		found, err := s.store().Setting(ctx, modelRecordKey(id), &current)
		if err != nil {
			return "", err
		}
		if !found {
			return "", errModelPurposeNotFound
		}
		raw, err := json.Marshal(map[string]any{"key": key, "revision": current.Revision, "override": current.Binding, "task_exists": false})
		return string(raw), err
	}
	setting.set = func(ctx context.Context, value json.RawMessage) (*ownChange, error) {
		if !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("the task no longer exists; only value null may clear its saved override, not set a new binding")
		}
		return s.prepareOrphanTaskClear(ctx, id)
	}
	return setting, true, nil
}

func (s *Service) orphanTaskID(ctx context.Context, id string) (bool, error) {
	tasks := s.taskViews(ctx)
	if tasks == nil {
		return false, i18n.Err("assistant-models.err_read_tasks")
	}
	for _, task := range tasks {
		if id == "task."+task.ID {
			return false, nil
		}
	}
	return true, nil
}

func (s *Service) orphanTaskRecordLocked(ctx context.Context, id string) (modelRecord, error) {
	orphan, err := s.orphanTaskID(ctx, id)
	if err != nil {
		return modelRecord{}, err
	}
	if !orphan {
		return modelRecord{}, errors.New("the task exists again; its model override was not cleared")
	}
	var record modelRecord
	found, err := s.store().Setting(ctx, modelRecordKey(id), &record)
	if err != nil {
		return modelRecord{}, err
	}
	if !found || record.Binding == (ModelBinding{}) {
		return modelRecord{}, errModelPurposeNotFound
	}
	return record, nil
}

func (s *Service) prepareOrphanTaskClear(ctx context.Context, id string) (*ownChange, error) {
	s.models.mu.Lock()
	record, err := s.orphanTaskRecordLocked(ctx, id)
	s.models.mu.Unlock()
	if err != nil {
		return nil, err
	}
	revision := record.Revision
	before := modelGroupCardBinding(s.lang(ctx), record.Binding)
	return &ownChange{applyChecksProposal: true, revision: &revision, cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_orphan_task", i18n.Args{
			"task": id, "before": modelGroupCardBinding(l, record.Binding),
			"after": i18n.T(l, "assistant-settings_tool.orphan_task_inherit")})
	}, apply: func(ctx context.Context) (string, error) {
		s.models.mu.Lock()
		current, err := s.orphanTaskRecordLocked(ctx, id)
		if err == nil && (current.Revision != revision || current.Binding != record.Binding) {
			err = fmt.Errorf("the orphaned task override changed after the card was shown: %w", approvalops.ErrStale)
		}
		if err == nil {
			err = s.saveModelRecord(ctx, id, revision, ModelBinding{})
		}
		s.models.mu.Unlock()
		if err != nil {
			return "", err
		}
		var saved modelRecord
		found, err := s.store().Setting(ctx, modelRecordKey(id), &saved)
		if err != nil || !found || saved.Binding != (ModelBinding{}) {
			if err == nil {
				err = errors.New("cleared override was not empty on read-back")
			}
			return "", ownerActionCompletedError{err: fmt.Errorf("cleared %s, but read-back failed: %w", modelKey(id), err)}
		}
		raw, err := json.Marshal(map[string]any{"key": modelKey(id), "before": before, "revision": saved.Revision, "override": saved.Binding, "inherited": true})
		return string(raw), err
	}}, nil
}
