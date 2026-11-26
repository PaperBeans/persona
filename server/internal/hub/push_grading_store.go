package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

const (
	pushGradingBaseSettingKey        = "hub.push_grading"
	pushGradingLayerSettingKey       = "hub.push_grading_layer"
	pushGradingExtensionSourcePrefix = "extension:"
)

var errPushGradingLayerTaken = errors.New("layer_taken")

// pushGradingLayer is the persisted singleton extension layer. Manager stays
// internal: extension IDs are unique only within one manager's registry. Source
// remains the public extension:<id> attribution used by GET and preview.
type pushGradingLayer struct {
	Source    string            `json:"source"`
	Manager   string            `json:"manager"`
	UpdatedAt time.Time         `json:"updated_at"`
	Revision  int64             `json:"revision"`
	Config    pushGradingConfig `json:"config"`
}

type pushGradingSnapshot struct {
	Base      *pushGradingConfig
	Layer     *pushGradingLayer
	Effective pushGradingEffective
}

// pushGradingStoreAPI is the narrow persistence seam push grading needs. It is
// deliberately a type assertion instead of new methods on the hub's central
// Store interface; db.DB supplies it, and lightweight tests can do the same.
type pushGradingStoreAPI interface {
	Settings(ctx context.Context, keys ...string) (map[string]json.RawMessage, error)
	PutPushGradingBase(ctx context.Context, baseKey, layerKey, mirrorKey string, value any) error
	SetPushGradingSwitch(ctx context.Context, baseKey, mirrorKey string, enabled bool) error
	PutPushGradingLayer(ctx context.Context, baseKey, layerKey, mirrorKey, manager, source string, value any, out any) (bool, error)
	DeletePushGradingLayer(ctx context.Context, baseKey, layerKey, mirrorKey, manager, source string, revision int64) (bool, error)
	ClearPushGradingLayer(ctx context.Context, layerKey, manager, source string) (bool, error)
	PrunePushGradingLayer(ctx context.Context, layerKey, manager string, keepSources []string) (bool, error)
}

var _ pushGradingStoreAPI = (*db.DB)(nil)

func pushGradingStore(store Store) (pushGradingStoreAPI, error) {
	if store == nil {
		return nil, errors.New("push grading storage is not available")
	}
	storeAPI, ok := store.(pushGradingStoreAPI)
	if !ok {
		return nil, errors.New("push grading storage does not support layer ownership")
	}
	return storeAPI, nil
}

func pushGradingExtensionSource(ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		return ""
	}
	return pushGradingExtensionSourcePrefix + ext
}

func validPushGradingOwner(manager, source string) bool {
	return strings.TrimSpace(manager) != "" && strings.HasPrefix(source, pushGradingExtensionSourcePrefix) && len(source) > len(pushGradingExtensionSourcePrefix)
}

func readPushGradingSnapshot(ctx context.Context, store Store) (pushGradingSnapshot, error) {
	storeAPI, err := pushGradingStore(store)
	if err != nil {
		return pushGradingSnapshot{}, err
	}
	values, err := storeAPI.Settings(ctx, pushGradingBaseSettingKey, pushGradingLayerSettingKey)
	if err != nil {
		return pushGradingSnapshot{}, err
	}
	var snapshot pushGradingSnapshot
	if raw, ok := values[pushGradingBaseSettingKey]; ok {
		base, err := parsePushGradingConfig(raw)
		if err != nil {
			return pushGradingSnapshot{}, fmt.Errorf("read push grading base: %w", err)
		}
		if err := base.validate(false); err != nil {
			return pushGradingSnapshot{}, err
		}
		snapshot.Base = &base
	}
	if raw, ok := values[pushGradingLayerSettingKey]; ok {
		var layer pushGradingLayer
		if err := json.Unmarshal(raw, &layer); err != nil {
			return pushGradingSnapshot{}, fmt.Errorf("read push grading layer: %w", err)
		}
		if !validPushGradingOwner(layer.Manager, layer.Source) || layer.Revision < 1 || layer.UpdatedAt.IsZero() {
			return pushGradingSnapshot{}, errors.New("read push grading layer: invalid ownership metadata")
		}
		if err := layer.Config.validate(true); err != nil {
			return pushGradingSnapshot{}, err
		}
		snapshot.Layer = &layer
	}
	if snapshot.Layer == nil {
		snapshot.Effective = resolvePushGrading(snapshot.Base, nil, "")
	} else {
		snapshot.Effective = resolvePushGrading(snapshot.Base, &snapshot.Layer.Config, snapshot.Layer.Source)
	}
	return snapshot, nil
}

func writePushGradingLayer(ctx context.Context, store Store, layer pushGradingLayer) (pushGradingLayer, error) {
	storeAPI, err := pushGradingStore(store)
	if err != nil {
		return pushGradingLayer{}, err
	}
	if !validPushGradingOwner(layer.Manager, layer.Source) {
		return pushGradingLayer{}, errors.New("push grading layer has no authenticated owner")
	}
	layer.Manager = strings.TrimSpace(layer.Manager)
	if err := layer.Config.validate(true); err != nil {
		return pushGradingLayer{}, err
	}
	// The database increments this on a same-owner renewal. Starting at one also
	// makes the first claim's persisted revision explicit.
	layer.Revision = 1
	if layer.UpdatedAt.IsZero() {
		layer.UpdatedAt = time.Now().UTC()
	} else {
		layer.UpdatedAt = layer.UpdatedAt.UTC()
	}
	var stored pushGradingLayer
	ok, err := storeAPI.PutPushGradingLayer(ctx, pushGradingBaseSettingKey, pushGradingLayerSettingKey, gradingSwitchSettingKey, layer.Manager, layer.Source, layer, &stored)
	if err != nil {
		return pushGradingLayer{}, err
	}
	if !ok {
		return pushGradingLayer{}, errPushGradingLayerTaken
	}
	return stored, nil
}

func deletePushGradingLayer(ctx context.Context, store Store, manager, source string, revision int64) (bool, error) {
	storeAPI, err := pushGradingStore(store)
	if err != nil {
		return false, err
	}
	if !validPushGradingOwner(manager, source) || revision < 0 {
		return false, errors.New("push grading layer delete needs an exact owner")
	}
	manager = strings.TrimSpace(manager)
	return storeAPI.DeletePushGradingLayer(ctx, pushGradingBaseSettingKey, pushGradingLayerSettingKey, gradingSwitchSettingKey, manager, source, revision)
}

func clearPushGradingLayer(ctx context.Context, store Store, manager, source string) (bool, error) {
	storeAPI, err := pushGradingStore(store)
	if err != nil {
		return false, err
	}
	manager = strings.TrimSpace(manager)
	source = strings.TrimSpace(source)
	if source != "" && (!strings.HasPrefix(source, pushGradingExtensionSourcePrefix) || len(source) == len(pushGradingExtensionSourcePrefix)) {
		return false, errors.New("push grading layer clear has an invalid extension source")
	}
	return storeAPI.ClearPushGradingLayer(ctx, pushGradingLayerSettingKey, manager, source)
}

func prunePushGradingLayer(ctx context.Context, store Store, manager string, activeExtensions []string) (bool, error) {
	storeAPI, err := pushGradingStore(store)
	if err != nil {
		return false, err
	}
	manager = strings.TrimSpace(manager)
	if manager == "" {
		return false, errors.New("push grading layer prune needs a manager")
	}
	keepSources := make([]string, 0, len(activeExtensions))
	for _, ext := range activeExtensions {
		if src := pushGradingExtensionSource(ext); src != "" {
			keepSources = append(keepSources, src)
		}
	}
	return storeAPI.PrunePushGradingLayer(ctx, pushGradingLayerSettingKey, manager, keepSources)
}

func writePushGradingBase(ctx context.Context, store Store, base pushGradingConfig) error {
	storeAPI, err := pushGradingStore(store)
	if err != nil {
		return err
	}
	if err := base.validate(false); err != nil {
		return err
	}
	return storeAPI.PutPushGradingBase(ctx, pushGradingBaseSettingKey, pushGradingLayerSettingKey, gradingSwitchSettingKey, base)
}
