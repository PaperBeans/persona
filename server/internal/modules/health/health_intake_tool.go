package health

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

type healthIntakeToolArgs struct {
	Action string     `json:"action"`
	Kind   string     `json:"kind"`
	Amount *float64   `json:"amount"`
	At     *time.Time `json:"at"`
	Note   *string    `json:"note"`
	Days   *int       `json:"days"`
	ID     string     `json:"id"`
	Item   string     `json:"item"`
}

// The caller's space is checked in runTool before any intake storage is accessed.
func (s *Service) toolHealthIntake(ctx context.Context, args json.RawMessage) (string, error) {
	var a healthIntakeToolArgs
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return "", err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", errors.New("expected one health_intake argument object")
	}
	logFields := a.Kind != "" || a.Amount != nil || a.At != nil || a.Note != nil
	if a.Action != "add_item" && a.Item != "" {
		return "", errors.New("item is only valid for add_item")
	}
	switch a.Action {
	case "add_item":
		if logFields || a.ID != "" || a.Days != nil || !validHealthItems([]string{a.Item}) {
			return "", errors.New("add_item requires one catalog item")
		}
		return s.addHealthItem(ctx, a.Item)
	case "log":
		if a.Amount == nil || a.Days != nil || a.ID != "" {
			return "", i18n.Err("assistant-health_intake.invalid_entry")
		}
	case "list":
		if logFields || a.ID != "" || a.Days != nil && (*a.Days < 1 || *a.Days > 366) {
			return "", i18n.Err("assistant-health_intake.invalid_query")
		}
	case "delete":
		if logFields || a.Days != nil || !intakeUUIDPattern.MatchString(a.ID) {
			return "", i18n.Err("assistant-health_intake.invalid_entry")
		}
	default:
		return "", errors.New("action must be log, list, delete or add_item")
	}
	st := s.h.IntakeStore()
	if st == nil {
		return "", i18n.Err("assistant-health_intake.unavailable")
	}
	settings, err := s.healthIntakeSettings(ctx)
	if err != nil {
		return "", err
	}
	if a.Action == "list" {
		days := 7
		if a.Days != nil {
			days = *a.Days
		}
		return s.listHealthIntakeTool(ctx, st, settings, days)
	}
	var message string
	if a.Action == "log" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
		id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
		req := healthIntakeRequest{ID: id, Kind: a.Kind, Amount: a.Amount, At: a.At}
		if a.Note != nil {
			req.Note = *a.Note
		}
		entry, err := req.entry(settings, time.Now())
		if err != nil {
			return "", err
		}
		// Keep the gate and write in the same critical section as item saves and reminders.
		s.intakeMu.Lock()
		items, err := s.healthItemsLocked(ctx)
		if err != nil {
			s.intakeMu.Unlock()
			return "", err
		}
		if !slices.Contains(items.Items, a.Kind) {
			s.intakeMu.Unlock()
			return fmt.Sprintf("%s is not added to Health. Ask the user whether to add it before logging.", a.Kind), nil
		}
		saved, _, err := s.addHealthIntakeLocked(ctx, st, entry)
		s.intakeMu.Unlock()
		if err != nil {
			return "", fmt.Errorf("log may have committed; check list before logging again: %w", err)
		}
		unit := "ml"
		if saved.Kind == "protein" {
			unit = "g"
		}
		message = fmt.Sprintf("Recorded %s %g %s at %s (id %s).", saved.Kind, saved.Amount, unit, saved.At.Format(time.RFC3339), saved.ID)
		if saved.Note != "" {
			message += fmt.Sprintf(" Note: %s.", saved.Note)
		}
	} else {
		saved, changed, err := st.HealthIntakeUndo(ctx, strings.ToLower(a.ID))
		if err != nil {
			return "", err
		}
		if saved == nil {
			return "", i18n.Err("assistant-health_intake.not_found")
		}
		if changed {
			s.event(ctx, map[string]any{"type": "health_intake"})
		}
		message = fmt.Sprintf("Deleted %s (id %s).", saved.Kind, saved.ID)
	}
	v, err := s.healthIntakeView(ctx, st, settings, 1)
	if err != nil {
		return "", fmt.Errorf("%s Today's totals are unavailable: %w", message, err)
	}
	return fmt.Sprintf("%s Today: water %g/%d ml; protein %g/%d g (%s).", message,
		v.Water.Today, v.Water.Goal, v.Protein.Today, v.Protein.Goal, v.Zone), nil
}

func (s *Service) listHealthIntakeTool(ctx context.Context, st healthIntakeStore, settings healthIntakeSettings, days int) (string, error) {
	loc, now := s.h.Location(ctx), time.Now()
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	rows, err := st.HealthIntakeRange(ctx, today.AddDate(0, 0, 1-days), today.AddDate(0, 0, 1))
	if err != nil {
		return "", err
	}
	if rows == nil {
		rows = []assistantwire.HealthIntakeRow{}
	}
	v := sumHealthIntake(rows, settings, now, loc, days)
	b, err := json.Marshal(struct {
		Entries []assistantwire.HealthIntakeRow `json:"entries"`
		Days    []healthIntakeDay               `json:"days"`
		Today   healthIntakeTotals              `json:"today"`
		Goals   healthIntakeTotals              `json:"goals"`
		Zone    string                          `json:"zone"`
	}{rows, v.Days, healthIntakeTotals{v.Water.Today, v.Protein.Today},
		healthIntakeTotals{float64(v.Water.Goal), float64(v.Protein.Goal)}, v.Zone})
	return string(b), err
}
