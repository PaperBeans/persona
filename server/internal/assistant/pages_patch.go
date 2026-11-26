package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
)

// pagePatchItemsMost is the maximum number of components one page patch call may replace.
const pagePatchItemsMost = 50

// pagePatchByWriter runs page patch for a conversation that owns/writes the page.
func (s *Service) pagePatchByWriter(ctx context.Context, st pageStore, c pageCaller, in pageToolInput) (string, error) {
	p, err := s.writablePage(ctx, st, c, in.ID)
	if err != nil {
		return "", err
	}
	return s.pagePatch(ctx, st, p, db.AssistantPageWriter{Kind: db.AssistantPageByAgent, Machine: c.machine, Session: c.session}, in)
}

// pagePatch writes a watch reading and/or replaces existing components of a kept, unpaused page's view by key.
// Both writing conversations (ByAgent) and a bound routine's current run under its data-only grant (ByRoutine) pass
// through here.
func (s *Service) pagePatch(ctx context.Context, st pageStore, p *db.AssistantPageRow, writer db.AssistantPageWriter, in pageToolInput) (string, error) {
	switch {
	case p.Status == db.AssistantPageDraft:
		return "", pageWriteError(db.ErrAssistantPageNotKept)
	case p.Status == db.AssistantPageRemoved:
		return "", pageWriteError(db.ErrAssistantPageRemoved)
	case p.Paused:
		return "", pageWriteError(db.ErrAssistantPagePaused)
	}
	alertText, err := pageLine("alert_text", in.AlertText, db.AssistantPageAlertTextMost)
	if err != nil {
		return "", err
	}
	note, err := pageLine("note", in.Note, db.AssistantPageNoteMost)
	if err != nil {
		return "", err
	}
	writer.Note = note

	hasReading := hasNonNullJSON(in.Reading)
	hasItems := hasNonNullJSON(in.Items)

	var storedReading json.RawMessage
	if hasReading {
		if len(p.Watch) == 0 {
			return "", errors.New("reading: this page has no watch")
		}
		watch, err := wire.ParsePageWatch(p.Watch)
		if err != nil {
			return "", fmt.Errorf("watch: %w", err)
		}
		reading, err := wire.ParsePageReading(watch, in.Reading)
		if err != nil {
			return "", fmt.Errorf("invalid reading: %w", err)
		}
		storedReading, err = json.Marshal(reading)
		if err != nil {
			return "", err
		}
	}

	var patchedView json.RawMessage
	var patchedCount int
	if hasItems {
		patchedView, patchedCount, err = patchPageViewItems(p.View, in.Items)
		if err != nil {
			return "", err
		}
	}

	if !hasReading && patchedCount == 0 {
		return "", errors.New("patch requires at least one of reading or items")
	}

	now := time.Now()
	row := p
	if patchedView != nil {
		row, err = st.UpdateAssistantPage(ctx, db.AssistantPageUpdate{
			ID:       p.ID,
			Expected: p.Version,
			View:     patchedView,
			By:       writer,
			At:       now,
		})
		if err != nil {
			return "", pageWriteError(err)
		}
	}
	if hasReading {
		row, _, err = st.AddAssistantPageReading(ctx, db.NewAssistantPageReading{
			PageID:    p.ID,
			Reading:   storedReading,
			AlertText: alertText,
			Note:      note,
			ByKind:    writer.Kind,
			ByMachine: writer.Machine,
			BySession: writer.Session,
			At:        now,
		})
		if err != nil {
			return "", pageWriteError(err)
		}
		s.pageReadingWritten(ctx, row)
	}
	s.pageWritten(row.ID)
	out := map[string]any{"id": row.ID, "version": row.Version, "updated_at": row.ContentAt}
	if hasReading {
		out["reading"] = true
	}
	if patchedCount > 0 {
		out["patched"] = patchedCount
	}
	return viewToolJSON(out)
}

func hasNonNullJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func isExplicitNullJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && bytes.Equal(trimmed, []byte("null"))
}

type pageItemLoc struct {
	page string // "" for root view, else detail page key in v.Pages
	sec  int
	item int
}

// patchPageViewItems replaces existing components in storedView (root view or detail pages) by key, requiring each
// replacement to match an existing component's key and type, with at most pagePatchItemsMost replacements, and
// holding the resulting view to the page floor (wire.ParseAgentPage).
func patchPageViewItems(storedView, rawItems json.RawMessage) (json.RawMessage, int, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(rawItems, &list); err != nil {
		return nil, 0, fmt.Errorf("items: JSON array of components required: %w", err)
	}
	if len(list) == 0 {
		return nil, 0, nil
	}
	if len(list) > pagePatchItemsMost {
		return nil, 0, fmt.Errorf("items: at most %d components", pagePatchItemsMost)
	}
	trimmedView := bytes.TrimSpace(storedView)
	if len(trimmedView) == 0 || bytes.Equal(trimmedView, []byte("{}")) || bytes.Equal(trimmedView, []byte("null")) {
		return nil, 0, errors.New("items: this page has no view components to patch")
	}
	v, err := wire.ParseAgentPage(trimmedView)
	if err != nil {
		return nil, 0, fmt.Errorf("stored view: %w", err)
	}
	locs := map[string][]pageItemLoc{}
	indexView := func(pageName string, view *wire.View) {
		for si, sec := range view.Sections {
			for ii, item := range sec.Items {
				if item.Key != "" {
					locs[item.Key] = append(locs[item.Key], pageItemLoc{page: pageName, sec: si, item: ii})
				}
			}
		}
	}
	indexView("", v)
	pageNames := make([]string, 0, len(v.Pages))
	for name := range v.Pages {
		pageNames = append(pageNames, name)
	}
	slices.Sort(pageNames)
	for _, name := range pageNames {
		dp := v.Pages[name]
		indexView(name, &dp)
	}

	seen := make(map[string]bool, len(list))
	for i, rawItem := range list {
		if err := wire.ValidateJSON("Component", rawItem); err != nil {
			return nil, 0, fmt.Errorf("items[%d]: %w", i, err)
		}
		var comp wire.Component
		if err := json.Unmarshal(rawItem, &comp); err != nil {
			return nil, 0, fmt.Errorf("items[%d]: %w", i, err)
		}
		if strings.TrimSpace(comp.Key) == "" {
			return nil, 0, fmt.Errorf("items[%d]: key is required", i)
		}
		if seen[comp.Key] {
			return nil, 0, fmt.Errorf("items[%d]: duplicate key %q", i, comp.Key)
		}
		seen[comp.Key] = true
		matches := locs[comp.Key]
		if len(matches) == 0 {
			return nil, 0, fmt.Errorf("items[%d]: unknown key %q; patch only replaces existing components", i, comp.Key)
		}
		for _, loc := range matches {
			var existing wire.Component
			if loc.page == "" {
				existing = v.Sections[loc.sec].Items[loc.item]
			} else {
				existing = v.Pages[loc.page].Sections[loc.sec].Items[loc.item]
			}
			if comp.Type != existing.Type {
				return nil, 0, fmt.Errorf("items[%d] (%s): type %q does not match stored type %q", i, comp.Key, comp.Type, existing.Type)
			}
			if loc.page == "" {
				v.Sections[loc.sec].Items[loc.item] = comp
			} else {
				dp := v.Pages[loc.page]
				dp.Sections[loc.sec].Items[loc.item] = comp
				v.Pages[loc.page] = dp
			}
		}
	}

	patched, err := json.Marshal(v)
	if err != nil {
		return nil, 0, err
	}
	checked, err := wire.ParseAgentPage(patched)
	if err != nil {
		return nil, 0, fmt.Errorf("items: patched view failed page floor: %w", err)
	}
	canonical, err := json.Marshal(checked)
	if err != nil {
		return nil, 0, err
	}
	return canonical, len(list), nil
}
