package assistant

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A page's bound routine follows the page (#13020, docs/assistant/dashboards.md "The routine: schedule_routine.page_id"):
// removing the page turns its routine off, restoring the page turns it on again, and deleting the page for good
// (Delete now, the purge of Recently removed) deletes the routine with it. The owner's actions and the upkeep call these
// after the page's own change is stored; the routine side is kept here.

const (
	// pageRoutineMinMinutes is the minimum gap between two runs of a dashboard-bound routine (at most hourly).
	pageRoutineMinMinutes = 60
	// pageRoutineRunWindow is how long after a routine run starts its conversation may call page get/patch under its data grant.
	pageRoutineRunWindow = 2 * time.Hour

	// Preview card watch section and toggle field names.
	pageWatchSection       = "persona.watch"
	pageOfferFieldAlert    = "alert"
	pageOfferFieldOverview = "on_overview"
)

var (
	errPageRoutineTooOften    = i18n.Err("assistant-page-routine.too_often")
	errPageRoutineOptions     = i18n.Err("assistant-page-routine.page_options_fixed")
	errPageRoutinePageIDFixed = i18n.Err("assistant-page-routine.page_id_fixed")
)

func pageRoutineWord(l i18n.Lang, key string, args ...i18n.Args) string {
	return i18n.T(l, "assistant-page-routine."+key, args...)
}

func isCronOftenError(err error) bool {
	var msg *i18n.Message
	return errors.As(err, &msg) && msg.Key == "assistant-schedule.cron_often"
}

func writeTaskCheckError(w http.ResponseWriter, err error) {
	if errors.Is(err, errPageRoutineTooOften) {
		apierr.WriteCode(w, http.StatusBadRequest, "too_often", err)
		return
	}
	apierr.Write(w, http.StatusBadRequest, err)
}

// pageRoutineRemoved: page id went to Recently removed; its routine routineID ("" when none) stops.
func (s *Service) pageRoutineRemoved(ctx context.Context, id, routineID string) {
	if id == "" && routineID == "" {
		return
	}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		s.logf("assistant pages: stop routine %s for removed page %s: %v", routineID, id, err)
		return
	}
	now := time.Now()
	tasks := slices.Clone(sc.tasks)
	changed := false
	for i := range tasks {
		t := &tasks[i]
		if (routineID != "" && t.ID == routineID) || (id != "" && t.PageID == id) {
			if t.Enabled {
				old := *t
				t.Enabled = false
				disabledTransition(old, t, now)
				changed = true
			}
		}
	}
	if changed {
		if err := s.saveLocked(sc, tasks); err != nil {
			s.logf("assistant pages: save stopped routine %s for page %s: %v", routineID, id, err)
			changed = false
		}
	}
	sc.mu.Unlock()
	if changed {
		s.broadcastSchedule()
	}
}

// pageRoutineRestored: page id came back from Recently removed; its routine routineID ("" when none) runs again.
func (s *Service) pageRoutineRestored(ctx context.Context, id, routineID string) {
	s.enablePageRoutine(ctx, id, routineID, time.Now())
}

// armPageRoutine enables and arms a page's bound routine (used on Keep and on Restore).
func (s *Service) armPageRoutine(ctx context.Context, id, routineID string, now time.Time) {
	s.enablePageRoutine(ctx, id, routineID, now)
}

func (s *Service) enablePageRoutine(ctx context.Context, id, routineID string, now time.Time) {
	if id == "" && routineID == "" {
		return
	}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		s.logf("assistant pages: arm routine %s for page %s: %v", routineID, id, err)
		return
	}
	tasks := slices.Clone(sc.tasks)
	changed := false
	for i := range tasks {
		t := &tasks[i]
		if (routineID != "" && t.ID == routineID) || (id != "" && t.PageID == id) {
			if !t.Enabled {
				old := *t
				t.Enabled = true
				if t.Repeat == repeatInterval && !t.At.After(now) {
					t.At = now.Add(time.Duration(t.EveryMinutes) * time.Minute).UTC()
				}
				rearm(old, t, now)
				changed = true
			}
		}
	}
	if changed {
		if err := s.saveLocked(sc, tasks); err != nil {
			s.logf("assistant pages: save armed routine %s for page %s: %v", routineID, id, err)
			changed = false
		}
	}
	sc.mu.Unlock()
	if changed {
		s.broadcastSchedule()
	}
}

// pageRoutineDeleted: page id ("" when only the routine is known, as after a purge) is gone for good; its routine
// routineID ("" when none) goes too.
func (s *Service) pageRoutineDeleted(ctx context.Context, id, routineID string) {
	var ids []string
	if routineID != "" {
		ids = append(ids, routineID)
	}
	if id != "" {
		if sc, err := s.lockedSchedule(ctx); err == nil {
			for _, t := range sc.tasks {
				if t.PageID == id && !slices.Contains(ids, t.ID) {
					ids = append(ids, t.ID)
				}
			}
			sc.mu.Unlock()
		}
	}
	for _, rid := range ids {
		if _, err := s.deleteTask(ctx, rid); err != nil {
			s.logf("assistant pages: delete routine %s for page %s: %v", rid, id, err)
		}
	}
}

// pageRoutineUnbound clears a page's routine binding and data grant after routineID is deleted from Scheduled tasks.
func (s *Service) pageRoutineUnbound(ctx context.Context, routineID string) {
	if routineID == "" || !s.pagesManager() {
		return
	}
	st, ok := s.pageStore()
	if !ok {
		return
	}
	p, err := st.UnbindAssistantPageByRoutine(ctx, routineID, time.Now())
	if err != nil {
		s.logf("assistant pages: unbind routine %s: %v", routineID, err)
		return
	}
	if p == nil {
		return
	}
	if p.Status == db.AssistantPageDraft {
		if full, err := st.AssistantPage(ctx, p.ID); err == nil {
			s.redrawPageOffer(ctx, full, nil)
		}
		return
	}
	s.pageChanged(p.ID, pageContent)
}

// cleanOrphanPageRoutines removes any page-bound routine whose dashboard page no longer exists. Called at hub start-up.
func (s *Service) cleanOrphanPageRoutines(ctx context.Context) {
	if !s.pagesManager() {
		return
	}
	st, ok := s.pageStore()
	if !ok {
		return
	}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return
	}
	type bound struct{ taskID, pageID string }
	var list []bound
	for _, t := range sc.tasks {
		if t.PageID != "" {
			list = append(list, bound{taskID: t.ID, pageID: t.PageID})
		}
	}
	sc.mu.Unlock()
	for _, b := range list {
		_, err := st.AssistantPage(ctx, b.pageID)
		if errors.Is(err, db.ErrAssistantPageNotFound) {
			if _, derr := s.deleteTask(ctx, b.taskID); derr != nil {
				s.logf("assistant pages: delete orphan routine %s (missing page %s): %v", b.taskID, b.pageID, derr)
			}
		} else if err != nil {
			s.logf("assistant pages: check orphan routine %s page %s: %v", b.taskID, b.pageID, err)
		}
	}
}

// peekSchedule returns the schedule's tasks, runs, and active sending flags without triggering loadSchedule's legacy
// migration write. Read-only discovery and grant checks use this so checking whether a caller is a page routine never
// mutates stored settings.
func (s *Service) peekSchedule(ctx context.Context) (tasks []Task, runs []ScheduleRun, sending map[string]bool, err error) {
	sc := &s.sched
	sc.mu.Lock()
	if sc.loaded {
		tasks = slices.Clone(sc.tasks)
		runs = slices.Clone(sc.runs)
		if len(sc.sending) > 0 {
			sending = make(map[string]bool, len(sc.sending))
			for k, v := range sc.sending {
				if v {
					sending[k] = true
				}
			}
		}
		sc.mu.Unlock()
		return tasks, runs, sending, nil
	}
	sc.mu.Unlock()

	st := s.store()
	if st == nil {
		return nil, nil, nil, nil
	}
	ok, err := st.Setting(ctx, tasksKey, &tasks)
	if err != nil {
		return nil, nil, nil, errScheduleUnavailable
	}
	if !ok || len(tasks) == 0 {
		return nil, nil, nil, nil
	}
	hasPageTask := false
	for _, t := range tasks {
		if t.PageID != "" {
			hasPageTask = true
			break
		}
	}
	if !hasPageTask {
		return tasks, nil, nil, nil
	}
	if _, err := st.Setting(ctx, runsKey, &runs); err != nil {
		return nil, nil, nil, errScheduleUnavailable
	}
	return tasks, runs, nil, nil
}

// pageRoutineTask returns a copy of task id from the schedule without triggering legacy migration writes.
func (s *Service) pageRoutineTask(ctx context.Context, id string) (Task, bool) {
	if id == "" {
		return Task{}, false
	}
	tasks, _, _, err := s.peekSchedule(ctx)
	if err != nil {
		return Task{}, false
	}
	for _, t := range tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// pageConversationGranter names the writing conversation that granted a routine data rights on a kept page.
func pageConversationGranter(c pageCaller) string {
	return c.machine + "/" + c.session
}

// ---- routine conversation identification and data-only grant ----

// pageRoutineCaller is the verified active run of a routine bound to a kept dashboard page.
type pageRoutineCaller struct {
	machine string
	session string
	task    Task
	page    *db.AssistantPageRow
}

// originTaskRef returns the task id recorded in origins when session was started as a scheduled task's own conversation.
func (s *Service) originTaskRef(ctx context.Context, machine, session string) (ref string, isTask bool) {
	if session == "" {
		return "", false
	}
	b := &s.origins
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.originsLocked(ctx) {
		return "", false
	}
	o := b.m[session]
	if o == nil || o.Origin != OriginTask {
		return "", false
	}
	if o.MachineID != "" && machine != "" && !s.sameMachine(o.MachineID, machine) {
		return "", false
	}
	return o.Ref, true
}

// pageRoutineTaskFor identifies the page-bound routine whose own conversation is (machine, session), if any.
func (s *Service) pageRoutineTaskFor(ctx context.Context, machine, session string) (Task, bool, error) {
	if session == "" {
		return Task{}, false, nil
	}
	tasks, runs, _, err := s.peekSchedule(ctx)
	if err != nil {
		return Task{}, false, err
	}
	byID := make(map[string]Task)
	for _, t := range tasks {
		if t.PageID == "" {
			continue
		}
		byID[t.ID] = t
		if t.RunSessionID == session && (t.RunMachineID == "" || machine == "" || s.sameMachine(t.RunMachineID, machine)) {
			return t, true, nil
		}
		if t.SessionID == "" && t.LastSessionID == session && (t.RunMachineID == "" || machine == "" || s.sameMachine(t.RunMachineID, machine)) {
			return t, true, nil
		}
	}
	if len(byID) == 0 {
		return Task{}, false, nil
	}
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.Conversation == "source" || r.SessionID != session {
			continue
		}
		if r.MachineID != "" && machine != "" && !s.sameMachine(r.MachineID, machine) {
			continue
		}
		if t, ok := byID[r.RefID]; ok {
			return t, true, nil
		}
	}
	if ref, ok := s.originTaskRef(ctx, machine, session); ok && ref != "" {
		if t, found := byID[ref]; found {
			return t, true, nil
		}
	}
	return Task{}, false, nil
}

// isPageRoutineConversation reports whether (machine, session) belongs to a page-bound routine's own conversation.
func (s *Service) isPageRoutineConversation(ctx context.Context, machine, session string) bool {
	_, ok, err := s.pageRoutineTaskFor(ctx, machine, session)
	return err == nil && ok
}

// isPageRoutineSession reports whether session (on any machine) belongs to a page-bound routine's own conversation.
func (s *Service) isPageRoutineSession(ctx context.Context, session string) bool {
	return s.isPageRoutineConversation(ctx, "", session)
}

// latestPageRoutineRun returns the (machine, session, startedAt) of task t's latest started run.
func (s *Service) latestPageRoutineRun(runs []ScheduleRun, sending bool, t Task) (machine, session string, startedAt time.Time, ok bool) {
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.RefID != t.ID || r.Status == runSkipped || r.SessionID == "" || r.Conversation == "source" {
			continue
		}
		at := r.At
		if at.IsZero() {
			at = r.of
		}
		// While a resumed run is mid-dispatch, t.LastRun has already advanced ahead of the previous run's history entry.
		if sending && t.LastRun.After(at) && r.SessionID == t.RunSessionID &&
			(r.MachineID == "" || t.RunMachineID == "" || s.sameMachine(r.MachineID, t.RunMachineID)) {
			at = t.LastRun
		}
		return cmp.Or(r.MachineID, t.RunMachineID), r.SessionID, at, true
	}
	if !t.LastRun.IsZero() {
		sess := cmp.Or(t.LastSessionID, t.RunSessionID)
		if sess != "" {
			return t.RunMachineID, sess, t.LastRun, true
		}
	}
	return "", "", time.Time{}, false
}

// pageRoutineCallerOf re-checks a page-bound routine run's data-only grant from the hub's own records (never an
// argument): Dashboards on, built-in manager, not a refused conversation class, action in {get, patch} for its own
// page (when !listing), caller is the conversation of the routine's latest started run within 2 hours of start, the
// page's routine_id equals the routine, the page is kept and not paused, and the data grant row exists.
func (s *Service) pageRoutineCallerOf(ctx context.Context, machine, session string, task Task, action, pageID string, listing bool) (pageRoutineCaller, error) {
	if !s.pagesManager() {
		return pageRoutineCaller{}, refusePage("dashboards are the built-in assistant's; conversations of this manager do not use them.")
	}
	st, ok := s.pageStore()
	if !ok {
		return pageRoutineCaller{}, refusePage("dashboards need the assistant's database, which this process does not have.")
	}
	if !s.dashboardsOn(ctx) {
		return pageRoutineCaller{}, refusePage("Dashboards are off. Only the owner turns them on, in Settings › Assistant.")
	}
	if machine == "" || session == "" {
		return pageRoutineCaller{}, refusePage("only a routine's assistant conversation may refresh a dashboard.")
	}
	if s.isWorkMachine(machine) {
		return pageRoutineCaller{}, refusePage("a conversation on a work computer does not write dashboards.")
	}
	if _, guest, err := s.guestOf(ctx, machine, session); err != nil || guest {
		return pageRoutineCaller{}, refusePage("a meeting guest does not write dashboards.")
	}
	if s.outwardReader(ctx, machine, session) {
		return pageRoutineCaller{}, refusePage("a conversation that writes to other people does not write dashboards.")
	}
	if s.isBackground(ctx, s.supervisionHoldKey(ctx, session)) {
		return pageRoutineCaller{}, refusePage("the background supervisor does not write dashboards.")
	}
	if space := s.agentCallerSpace(ctx, machine, session); space != "" {
		return pageRoutineCaller{}, refusePage("a space's conversations do not write dashboards.")
	}
	if !listing {
		if c, ok := s.session(ctx, machine, session); !ok || !c.Assistant {
			return pageRoutineCaller{}, refusePage("only an assistant conversation may refresh a dashboard.")
		}
		if action != "get" && action != "patch" {
			return pageRoutineCaller{}, fmt.Errorf("forbidden: a dashboard routine may only call page get and page patch on its own page (%s)", task.PageID)
		}
		if !pageIDPattern.MatchString(pageID) {
			return pageRoutineCaller{}, errors.New("id: the pg_ id of this routine's page is required")
		}
		if pageID != task.PageID {
			return pageRoutineCaller{}, fmt.Errorf("forbidden: this routine may only access its own page (%s), not %s", task.PageID, pageID)
		}
	}

	tasks, runs, sending, err := s.peekSchedule(ctx)
	if err != nil {
		return pageRoutineCaller{}, err
	}
	var cur Task
	found := false
	for _, t := range tasks {
		if t.ID == task.ID {
			cur = t
			found = true
			break
		}
	}
	if !found || cur.PageID != task.PageID {
		return pageRoutineCaller{}, errors.New("no_grant: this routine is no longer bound to this page")
	}
	runMachine, runSession, startedAt, hasRun := s.latestPageRoutineRun(runs, sending[cur.ID], cur)

	now := time.Now()
	if !hasRun || runSession != session || (runMachine != "" && !s.sameMachine(runMachine, machine)) || startedAt.IsZero() || now.Sub(startedAt) > pageRoutineRunWindow {
		return pageRoutineCaller{}, errors.New("not_current_run: only the routine's latest run within 2 hours of starting may update this page")
	}

	p, err := st.AssistantPage(ctx, cur.PageID)
	if err != nil {
		return pageRoutineCaller{}, pageWriteError(err)
	}
	if p.RoutineID != cur.ID {
		return pageRoutineCaller{}, errors.New("no_grant: this routine is not bound to this page")
	}
	switch {
	case p.Status == db.AssistantPageDraft:
		return pageRoutineCaller{}, pageWriteError(db.ErrAssistantPageNotKept)
	case p.Status == db.AssistantPageRemoved:
		return pageRoutineCaller{}, pageWriteError(db.ErrAssistantPageRemoved)
	case p.Paused:
		return pageRoutineCaller{}, pageWriteError(db.ErrAssistantPagePaused)
	}
	granted, err := st.HasAssistantPageDataGrant(ctx, p.ID, db.AssistantPageGrantRoutine, cur.ID)
	if err != nil {
		return pageRoutineCaller{}, err
	}
	if !granted {
		return pageRoutineCaller{}, errors.New("no_grant: this routine does not have a data grant for this page")
	}
	return pageRoutineCaller{
		machine: s.canonicalMachine(machine),
		session: session,
		task:    cur,
		page:    p,
	}, nil
}

// pageRoutineTool returns the narrowed page tool definition listed to a page-bound routine run (get and patch only).
func pageRoutineTool(pageID string) proto.Tool {
	type prop struct {
		Type        string   `json:"type"`
		Enum        []string `json:"enum,omitempty"`
		MaxLength   int      `json:"maxLength,omitempty"`
		Items       *prop    `json:"items,omitempty"`
		Description string   `json:"description,omitempty"`
	}
	schema, _ := json.Marshal(struct {
		Type                 string          `json:"type"`
		Required             []string        `json:"required"`
		AdditionalProperties bool            `json:"additionalProperties"`
		Properties           map[string]prop `json:"properties"`
	}{
		Type:                 "object",
		Required:             []string{"action", "id"},
		AdditionalProperties: false,
		Properties: map[string]prop{
			"action":     {Type: "string", Enum: []string{"get", "patch"}},
			"id":         {Type: "string", Description: "the dashboard id (" + pageID + ")"},
			"reading":    {Type: "object", Description: "patch: new watch reading"},
			"items":      {Type: "array", Items: &prop{Type: "object"}, Description: "patch: replace up to 50 existing components by key and type"},
			"alert_text": {Type: "string", MaxLength: db.AssistantPageAlertTextMost, Description: "patch: optional note for alert push if rule trips"},
			"note":       {Type: "string", MaxLength: db.AssistantPageNoteMost, Description: "patch: one-line history note"},
		},
	})
	return proto.Tool{
		Name:        pageToolName,
		Description: fmt.Sprintf("Read or patch dashboard %s (get and patch only; no create, update, list or remove).", pageID),
		InputSchema: schema,
	}
}

// ---- routine run preparation and skip checks ----

// preparePageRoutineRun checks whether a page-bound routine may run right now and renders its run header.
// When the page is a draft, paused, removed, missing, unbound, or lacks a grant, skipErr is non-nil and the run is
// skipped in history without pushing.
func (s *Service) preparePageRoutineRun(ctx context.Context, t Task) (header string, skipErr error) {
	if !s.pagesManager() {
		return "", i18n.Err("assistant-page-routine.skip_unavailable")
	}
	st, ok := s.pageStore()
	if !ok {
		return "", i18n.Err("assistant-page-routine.skip_unavailable")
	}
	p, err := st.AssistantPage(ctx, t.PageID)
	if errors.Is(err, db.ErrAssistantPageNotFound) {
		return "", i18n.Err("assistant-page-routine.skip_missing")
	}
	if err != nil {
		return "", i18n.Err("assistant-page-routine.skip_unavailable")
	}
	if p.RoutineID != t.ID {
		return "", i18n.Err("assistant-page-routine.skip_unbound")
	}
	switch {
	case p.Status == db.AssistantPageDraft:
		return "", i18n.Err("assistant-page-routine.skip_draft")
	case p.Status == db.AssistantPageRemoved:
		return "", i18n.Err("assistant-page-routine.skip_removed")
	case p.Paused:
		return "", i18n.Err("assistant-page-routine.skip_paused")
	}
	granted, err := st.HasAssistantPageDataGrant(ctx, p.ID, db.AssistantPageGrantRoutine, t.ID)
	if err != nil {
		return "", i18n.Err("assistant-page-routine.skip_unavailable")
	}
	if !granted {
		return "", i18n.Err("assistant-page-routine.skip_no_grant")
	}
	data := prompts.PageRoutineRun{
		Language: string(s.lang(ctx)),
		PageID:   p.ID,
		Title:    p.Title,
		HasView:  hasNonNullJSON(p.View) && !bytes.Equal(bytes.TrimSpace(p.View), []byte("{}")),
	}
	if hasNonNullJSON(p.Watch) {
		if w, err := wire.ParsePageWatch(p.Watch); err == nil {
			data.Question = w.Question
			data.Display = string(w.Display)
			data.Unit = w.Unit
			data.Columns = w.Columns
		}
	}
	return prompts.RenderPageRoutineRun(data), nil
}

// ---- watch draft preview card helpers ----

// pageWatchPreviewSection builds the read-only summary section (Checks, How often, Shows) shown above a watch draft's
// switches and choices.
func pageWatchPreviewSection(w *wire.PageWatch, task *Task, l i18n.Lang) wire.Section {
	return wire.Section{
		Key: pageWatchSection,
		Items: []wire.Component{
			{
				Type:     wire.ComponentRow,
				Key:      "persona.watch.checks",
				Title:    pageRoutineWord(l, "preview_checks"),
				Subtitle: w.Question,
			},
			{
				Type:     wire.ComponentRow,
				Key:      "persona.watch.often",
				Title:    pageRoutineWord(l, "preview_often"),
				Subtitle: pageWatchScheduleText(task, l),
			},
			{
				Type:     wire.ComponentRow,
				Key:      "persona.watch.shows",
				Title:    pageRoutineWord(l, "preview_shows"),
				Subtitle: pageWatchDisplayText(w, l),
			},
		},
	}
}

// pageWatchScheduleText formats a bound routine's rule in the owner's language, or "Not scheduled yet" when none is bound.
func pageWatchScheduleText(t *Task, l i18n.Lang) string {
	if t == nil {
		return pageRoutineWord(l, "preview_not_scheduled")
	}
	switch t.Repeat {
	case repeatCron:
		if text := cronText(t.Cron, l); text != "" {
			return text
		}
		return t.Cron
	case repeatInterval:
		if t.EveryMinutes%60 == 0 {
			return pageRoutineWord(l, "freq_every_hours", i18n.Args{"count": t.EveryMinutes / 60})
		}
		return pageRoutineWord(l, "freq_every_minutes", i18n.Args{"count": t.EveryMinutes})
	case repeatDaily:
		switch {
		case len(t.Weekdays) == 0 || len(t.Weekdays) == 7:
			return pageRoutineWord(l, "freq_daily", i18n.Args{"time": t.Time})
		case len(t.Weekdays) == 5 && !slices.Contains(t.Weekdays, 6) && !slices.Contains(t.Weekdays, 7):
			return pageRoutineWord(l, "freq_weekdays", i18n.Args{"time": t.Time})
		default:
			names := make([]string, 0, len(t.Weekdays))
			for _, d := range t.Weekdays {
				if d >= 1 && d <= 7 {
					names = append(names, i18n.T(l, fmt.Sprintf("assistant-page-routine.freq_day_%d", d)))
				}
			}
			days := strings.Join(names, pageRoutineWord(l, "freq_day_sep"))
			return pageRoutineWord(l, "freq_days_at", i18n.Args{"days": days, "time": t.Time})
		}
	}
	return pageRoutineWord(l, "preview_not_scheduled")
}

// pageWatchDisplayText names the watch display in plain words for the preview card.
func pageWatchDisplayText(w *wire.PageWatch, l i18n.Lang) string {
	switch w.Display {
	case wire.PageWatchNumber:
		if w.Unit != "" {
			return pageRoutineWord(l, "preview_display_number_unit", i18n.Args{"unit": w.Unit})
		}
		return pageRoutineWord(l, "preview_display_number")
	case wire.PageWatchStatus:
		return pageRoutineWord(l, "preview_display_status")
	case wire.PageWatchTable:
		if len(w.Columns) > 0 {
			cols := strings.Join(w.Columns, pageRoutineWord(l, "freq_day_sep"))
			return pageRoutineWord(l, "preview_display_table_cols", i18n.Args{"columns": cols})
		}
		return pageRoutineWord(l, "preview_display_table")
	case wire.PageWatchTrend:
		if w.Unit != "" {
			return pageRoutineWord(l, "preview_display_trend_unit", i18n.Args{"unit": w.Unit})
		}
		return pageRoutineWord(l, "preview_display_trend")
	}
	return string(w.Display)
}

// pageWatchAlertLabel formats the watch's alert rule in plain words for the preview card's toggle label.
func pageWatchAlertLabel(w *wire.PageWatch, l i18n.Lang) string {
	if w == nil || w.Alert == nil {
		return ""
	}
	switch w.Alert.When {
	case wire.PageWatchAlertAbove:
		return pageRoutineWord(l, "preview_alert_above", i18n.Args{"value": formatWatchNumberUnit(w.Alert.Number, w.Unit)})
	case wire.PageWatchAlertBelow:
		return pageRoutineWord(l, "preview_alert_below", i18n.Args{"value": formatWatchNumberUnit(w.Alert.Number, w.Unit)})
	case wire.PageWatchAlertStatus:
		if w.Alert.Status == wire.PageWatchStatusProblem {
			return pageRoutineWord(l, "preview_alert_status_problem")
		}
		return pageRoutineWord(l, "preview_alert_status_warning")
	case wire.PageWatchAlertRows:
		return pageRoutineWord(l, "preview_alert_rows", i18n.Args{"count": w.Alert.Rows})
	}
	return ""
}

func formatWatchNumberUnit(v float64, unit string) string {
	if v == 0 {
		v = 0
	}
	var num string
	if math.Trunc(v) == v && math.Abs(v) < 1e15 {
		num = strconv.FormatInt(int64(v), 10)
	} else {
		num = strconv.FormatFloat(v, 'f', -1, 64)
	}
	if unit == "" {
		return num
	}
	if unit == "%" {
		return num + unit
	}
	return num + " " + unit
}
