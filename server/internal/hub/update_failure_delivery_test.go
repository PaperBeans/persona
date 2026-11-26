package hub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type delayedUpdateHistory struct {
	settingsStore
	writing chan struct{}
	release chan struct{}
}

func (s *delayedUpdateHistory) PutSetting(ctx context.Context, key string, value any) error {
	if settings, ok := value.(updateSettings); ok && len(settings.History) > 0 && settings.History[len(settings.History)-1].Result == "failed" {
		close(s.writing)
		<-s.release
	}
	return s.settingsStore.PutSetting(ctx, key, value)
}

func TestUpdateFailureIsPublishedAfterHistoryWrite(t *testing.T) {
	publisher := newPublisher(t, "v2-202611021700", 202611021700)
	h, _, _, _ := updatingHub(t, publisher, "release")
	store := &delayedUpdateHistory{settingsStore: settingsStore{vals: map[string][]byte{}}, writing: make(chan struct{}), release: make(chan struct{})}
	h.store = store
	release := sync.OnceFunc(func() { close(store.release) })
	defer release()
	app := &client{out: make(chan []byte, 64), role: roleFull}
	addTestClient(h, app)
	t.Cleanup(func() { removeTestClient(h, app) })
	h.upd.handover = func(context.Context, string, int64) error { return errors.New("edge could not start") }
	ctx := t.Context()
	if _, err := h.upd.check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.upd.begin(ctx, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.writing:
	case <-time.After(5 * time.Second):
		t.Fatal("the update failure never reached history")
	}
	failedEvent := func() bool {
		for {
			select {
			case raw := <-app.out:
				var event struct {
					Type   string
					Update updateView
				}
				if err := json.Unmarshal(raw, &event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "hub_update" && event.Update.State == "failed" {
					return true
				}
			default:
				return false
			}
		}
	}
	early := failedEvent()
	release()
	if early {
		t.Fatal("the phone saw the failed update while its history was still unwritten")
	}
	waitFor(t, "the recorded failure to reach the phone", failedEvent)
	var saved updateSettings
	if ok, err := store.Setting(ctx, updateSettingsKey, &saved); err != nil || !ok || len(saved.History) != 1 || saved.History[0].Result != "failed" {
		t.Fatalf("failed update history: %+v, present=%v, err=%v", saved.History, ok, err)
	}
}
