package assistant

import (
	"encoding/json"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestScheduledCatalogWorkoutDoesNotDispatchDefaultRoutine(t *testing.T) {
	for _, model := range []string{"default", "shell-model"} {
		t.Run(model, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := scheduledCatalogFixture(t, []string{"xhigh"})
				t.Cleanup(func() {
					time.Sleep(runWatchTimeout + time.Second) // sleep-ok: advances synctest's virtual clock to drain fake turn watchers
					synctest.Wait()
				})
				setSettings(t, s, func(v *Settings) { v.Timezone = "UTC" })
				setModel(t, s, "assistant", ModelBinding{Provider: "launcher.shared", Model: model, Effort: "xhigh"})
				// Any morning routine leaked by the fixture is due now, on every day of the virtual clock.
				// Settle both dispatches before counting; waiting for the first run can hide the second.
				sc := s.loadSchedule(t.Context())
				sc.mu.Lock()
				for i := range sc.tasks {
					if sc.tasks[i].ID == "morning" {
						sc.tasks[i].Time = time.Now().UTC().Format("15:04")
						sc.tasks[i].Weekdays = nil
					}
				}
				sc.mu.Unlock()
				v := sampleWorkout()
				raw, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				if r := request(s, http.MethodPost, "/api/v1/assistant/health/workouts", string(raw)); r.Code != http.StatusOK {
					t.Fatalf("workout admission: %d %s", r.Code, r.Body)
				}
				s.runDue(t.Context(), time.Now().Add(time.Second))
				synctest.Wait()
				runs := waitRuns(t, s, 1)
				if len(runs) != 1 || runs[0].RefID != "workout-"+v.ID || runs[0].Status != runOK || f.sent() != 1 {
					t.Fatalf("workout dispatch: runs=%+v sends=%d", runs, f.sent())
				}
				got := f.lastSend(t)
				if got["launcher"] != "shared" || got["model"] != model || got["effort"] != "xhigh" {
					t.Fatalf("requested binding changed: %v", got)
				}
			})
		})
	}
}
