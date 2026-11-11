package assistant

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

type archiveFailStore struct {
	*fleetMem
	failRead, failWrite bool
}

func (s *archiveFailStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == agentArchivesKey && s.failRead {
		return false, errors.New("archive read failed")
	}
	return s.fleetMem.Setting(ctx, key, out)
}
func (s *archiveFailStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == agentArchivesKey && s.failWrite {
		return errors.New("archive write failed")
	}
	return s.fleetMem.PutSetting(ctx, key, value)
}

func TestAgentSessionArchiveStorageFailure(t *testing.T) {
	ft, rows := archiveTestService(t)
	h := ft.s
	mid := "mac"
	store := &archiveFailStore{fleetMem: ft.st, failRead: true}
	h.store = func() Store { return store }
	if w := archiveRequest(h, mid, "codex-session", roleFull, true); w.Code != 500 {
		t.Fatalf("read failure: %d", w.Code)
	}
	store.failRead, store.failWrite = false, true
	if w := archiveRequest(h, mid, "codex-session", roleFull, true); w.Code != 500 {
		t.Fatalf("write failure: %d", w.Code)
	}
	if len(archiveRows(t, h)) != 0 {
		t.Fatal("failed write changed visibility")
	}
	store.failWrite = false
	if w := archiveRequest(h, mid, "codex-session", roleFull, true); w.Code != 200 {
		t.Fatal(w.Body)
	}
	store.failWrite = true
	if w := archiveRequest(h, mid, "codex-session", roleFull, false); w.Code != 500 {
		t.Fatalf("failed restore: %d", w.Code)
	}
	if !h.agentArchives.records[runtimeListID(mid, "codex-session")].Archived {
		t.Fatal("failed restore committed in memory")
	}
	// A failed automatic restore must be retried even after the source goes idle.
	rows[1].Status = proto.StatusWorking
	ft.d.setSessions(mid, rows)
	h.decorated(mid)
	rows[1].Status = proto.StatusOffline
	store.failWrite = false
	ft.d.setSessions(mid, rows)
	h.decorated(mid)
	if len(archiveRows(t, h)) != 0 {
		t.Fatal("busy observation lost after write failure")
	}
}

type archiveBlockingStore struct {
	*fleetMem
	read    bool
	entered chan struct{}
}

func (s *archiveBlockingStore) wait(ctx context.Context) error {
	close(s.entered)
	<-ctx.Done()
	return ctx.Err()
}
func (s *archiveBlockingStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == agentArchivesKey && s.read {
		return false, s.wait(ctx)
	}
	return s.fleetMem.Setting(ctx, key, out)
}
func (s *archiveBlockingStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == agentArchivesKey && !s.read {
		return s.wait(ctx)
	}
	return s.fleetMem.PutSetting(ctx, key, value)
}

func TestAgentSessionArchiveBoundedStorageAndLock(t *testing.T) {
	for _, read := range []bool{true, false} {
		name := "write"
		if read {
			name = "read"
		}
		t.Run(name, func(t *testing.T) {
			ft, rows := archiveTestService(t)
			h := ft.s
			mid := "mac"
			if !read {
				ft.d.setSessions(mid, rows)
				h.decorated(mid)
			}
			store := &archiveBlockingStore{fleetMem: ft.st, read: read, entered: make(chan struct{})}
			h.store = func() Store { return store }
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- h.noteAgentArchive(ctx, rows[0], true) }()
			<-store.entered
			// A contending paginated/list reader can cancel its wait for the gate.
			waiter, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer stop()
			start := time.Now()
			if _, _, err := h.archivedSessions(waiter, mid, rows, h.archiveMark()); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lock wait: %v", err)
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Fatal("lock ignored reader deadline")
			}
			// Visibility reads do not acquire the mutation gate at all.
			h.agentArchiveChanges(mid)
			if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("store wait: %v", err)
			}
			if _, ok := h.agentArchives.records[runtimeListID(mid, rows[0].SessionID)]; ok {
				t.Fatal("timed-out mutation committed")
			}
		})
	}
}

type archiveDeadlineStore struct {
	*fleetMem
	read   bool
	budget time.Duration
}

func (s *archiveDeadlineStore) check(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		s.budget = -1
	} else {
		s.budget = time.Until(deadline)
	}
	return context.DeadlineExceeded
}
func (s *archiveDeadlineStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == agentArchivesKey && s.read {
		return false, s.check(ctx)
	}
	return s.fleetMem.Setting(ctx, key, out)
}
func (s *archiveDeadlineStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == agentArchivesKey && !s.read {
		return s.check(ctx)
	}
	return s.fleetMem.PutSetting(ctx, key, value)
}
func TestAgentSessionArchiveHTTPStorageDeadline(t *testing.T) {
	for _, read := range []bool{true, false} {
		ft, _ := archiveTestService(t)
		h := ft.s
		mid := "mac"
		store := &archiveDeadlineStore{fleetMem: ft.st, read: read}
		h.store = func() Store { return store }
		var w *httptest.ResponseRecorder
		if read {
			w = httptest.NewRecorder()
			h.listArchivedSessions(w, httptest.NewRequest("GET", "/api/v1/sessions/archived", nil))
		} else {
			w = archiveRequest(h, mid, "codex-session", roleFull, true)
		}
		if w.Code != 500 || store.budget <= 0 || store.budget > agentArchiveTimeout {
			t.Fatalf("read=%v code=%d deadline=%v", read, w.Code, store.budget)
		}
	}
}

func TestAgentSessionArchiveStoreBecomesReady(t *testing.T) {
	ft, rows := archiveTestService(t)
	key := runtimeListID("mac", rows[0].SessionID)
	persisted := map[string]agentArchive{key: {Session: archiveSnapshot(rows[0]), Archived: true, ChangedAt: time.Now().UTC()}}
	if err := ft.st.PutSetting(t.Context(), agentArchivesKey, persisted); err != nil {
		t.Fatal(err)
	}
	ft.s.store = func() Store { return nil }
	if got := ft.s.decorated("mac"); len(got) != len(rows) {
		t.Fatalf("unready store broke normal inventory: %+v", got)
	}
	w := httptest.NewRecorder()
	ft.s.listArchivedSessions(w, httptest.NewRequest("GET", "/api/v1/sessions/archived", nil))
	if w.Code != 500 {
		t.Fatalf("unready store reported an empty success: %d %s", w.Code, w.Body)
	}
	if ft.s.agentArchives.loaded || ft.s.agentArchives.snapshot.Load() != nil {
		t.Fatal("unready store cached an empty archive")
	}
	ft.s.store = func() Store { return ft.st }
	if got := archiveRows(t, ft.s); len(got) != 1 || got[0].SessionID != rows[0].SessionID {
		t.Fatalf("late store lost persisted archives: %+v", got)
	}
	if w := archiveRequest(ft.s, "mac", rows[1].SessionID, roleFull, true); w.Code != 200 {
		t.Fatal(w.Body)
	}
	var saved map[string]agentArchive
	if _, err := ft.st.Setting(t.Context(), agentArchivesKey, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || !saved[key].Archived {
		t.Fatalf("later write overwrote existing archives: %+v", saved)
	}
}
