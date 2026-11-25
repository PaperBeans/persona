package health

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
)

const healthItemsKey = "health.items"

type healthItem struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

var healthCatalog = []healthItem{
	{"water", "persona"}, {"protein", "persona"}, {"strength", "persona"},
	{"activity", "apple_health"}, {"trends", "apple_health"},
}

type healthItemsSetting struct {
	Items    []string `json:"items"`
	Revision int64    `json:"revision"`
}

type healthItemsState struct {
	healthItemsSetting
	Catalog []healthItem `json:"catalog"`
	Saved   bool         `json:"saved"`
	// Unknown IDs belong to a newer catalog. Keep their stored order and occurrences on a save.
	unknown []string
}

func knownHealthItem(id string) bool {
	return slices.ContainsFunc(healthCatalog, func(c healthItem) bool { return c.ID == id })
}

func validHealthItems(items []string) bool {
	if items == nil || len(items) > len(healthCatalog) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range items {
		if seen[id] || !knownHealthItem(id) {
			return false
		}
		seen[id] = true
	}
	return true
}

// healthItemsLocked reads the one ordered list. Untouched owners get computed defaults, never a write on read.
// The caller holds intakeMu, shared by saves, tool logging and reminder planning.
func (s *Service) healthItemsLocked(ctx context.Context) (healthItemsState, error) {
	v := healthItemsState{Catalog: healthCatalog}
	st := s.store()
	if st == nil {
		return v, errors.New("health items storage unavailable")
	}
	found, err := st.Setting(ctx, healthItemsKey, &v.healthItemsSetting)
	if err != nil {
		return v, err
	}
	if found {
		if v.Items == nil || v.Revision < 1 {
			return v, errors.New("invalid stored health items")
		}
		stored := v.Items
		v.Items = []string{}
		seen := map[string]bool{}
		for _, id := range stored {
			if !knownHealthItem(id) {
				v.unknown = append(v.unknown, id)
			} else if !seen[id] {
				v.Items = append(v.Items, id)
				seen[id] = true
			}
		}
		v.Saved = true
		return v, nil
	}
	var intake json.RawMessage
	legacy, err := st.Setting(ctx, healthIntakeKey, &intake)
	if err != nil {
		var syntax *json.SyntaxError
		if !legacy || !errors.As(err, &syntax) {
			return v, err
		}
		// A malformed existing intake setting still marks an existing owner; its page can repair it.
	}
	if !legacy {
		if entries := s.h.IntakeStore(); entries != nil {
			rows, err := entries.HealthIntakeChanges(ctx, 0, 1)
			if err != nil {
				return v, err
			}
			legacy = len(rows) != 0 // Includes undo markers: removing a record never makes an existing owner new.
		}
	}
	if !legacy {
		pending, err := s.pendingWaterExtension(ctx)
		if err != nil {
			return v, err
		}
		legacy = pending == "pending"
	}
	v.Items = []string{}
	if legacy {
		v.Items = append(v.Items, "water", "protein")
	}
	v.Items = append(v.Items, "activity", "trends")
	return v, nil
}

// Items returns the owner's added items in order, through the core's Health seam.
func (s *Service) Items(ctx context.Context) ([]string, error) {
	s.intakeMu.Lock()
	defer s.intakeMu.Unlock()
	v, err := s.healthItemsLocked(ctx)
	return v.Items, err
}

func (s *Service) saveHealthItemsLocked(ctx context.Context, v healthItemsState, items []string) (healthItemsState, error) {
	if v.Revision == math.MaxInt64 {
		return v, errors.New("health items revision exhausted")
	}
	next := healthItemsSetting{Items: append(slices.Clone(items), v.unknown...), Revision: v.Revision + 1}
	if err := s.store().PutSetting(ctx, healthItemsKey, next); err != nil {
		return v, err
	}
	v.Items, v.Revision, v.Saved = slices.Clone(items), next.Revision, true
	s.event(ctx, map[string]any{"type": "health_items", "revision": v.Revision})
	return v, nil
}

func (s *Service) getHealthItems(w http.ResponseWriter, r *http.Request) {
	if !intakeAccess(w, r, false) {
		return
	}
	s.intakeMu.Lock()
	defer s.intakeMu.Unlock()
	v, err := s.healthItemsLocked(r.Context())
	if err != nil {
		intakeUnavailable(w)
		return
	}
	intakeJSON(w, v)
}

func (s *Service) putHealthItems(w http.ResponseWriter, r *http.Request) {
	if !intakeAccess(w, r, false) {
		return
	}
	var req struct {
		Items    []string `json:"items"`
		Revision *int64   `json:"revision"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF ||
		!validHealthItems(req.Items) || req.Revision == nil || *req.Revision < 0 {
		apierr.WriteCode(w, http.StatusBadRequest, "invalid_health_items", i18n.Err("assistant-health_intake.invalid_items"))
		return
	}
	s.intakeMu.Lock()
	defer s.intakeMu.Unlock()
	v, err := s.healthItemsLocked(r.Context())
	if err != nil {
		intakeUnavailable(w)
		return
	}
	if *req.Revision != v.Revision {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		intakeJSON(w, v)
		return
	}
	v, err = s.saveHealthItemsLocked(r.Context(), v, req.Items)
	if err != nil {
		intakeUnavailable(w)
		return
	}
	intakeJSON(w, v)
}

func (s *Service) addHealthItem(ctx context.Context, id string) (string, error) {
	s.intakeMu.Lock()
	defer s.intakeMu.Unlock()
	v, err := s.healthItemsLocked(ctx)
	if err != nil {
		return "", err
	}
	if slices.Contains(v.Items, id) {
		return "Already added to Health: " + id + ".", nil
	}
	v, err = s.saveHealthItemsLocked(ctx, v, append(v.Items, id))
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	return string(b), err
}
