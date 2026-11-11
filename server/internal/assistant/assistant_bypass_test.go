package assistant

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestAssistantBypassDefaultPostgres(t *testing.T) {
	database := todoTestDB(t)
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, database)
	ctx := context.Background()
	for _, p := range []StartParams{{Main: true}, {Untitled: true}} {
		sess, err := s.Start(ctx, p)
		if err != nil || sess.PermissionMode != proto.ModeBypass || f.sends[len(f.sends)-1]["permission_mode"] != proto.ModeBypass {
			t.Fatalf("unconfigured start: %+v %v", sess, err)
		}
	}
	now := time.Now()
	task := Task{ID: "routine", Title: "Read status", Prompt: "Read local status", Repeat: repeatOnce, Enabled: true}
	setTasks(s, task)
	_, err := s.runTask(task, now, now, true, false)
	if err != nil || f.sends[len(f.sends)-1]["permission_mode"] != proto.ModeBypass {
		t.Fatalf("scheduled start mode: %v %v", f.sends, err)
	}
	w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", "assistant", "full", `{"default_permission_mode":"plan"}`)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	reloaded := newServiceOn(t, f, database)
	sess, err := reloaded.Start(ctx, StartParams{Untitled: true})
	if err != nil || sess.PermissionMode != "plan" {
		t.Fatalf("manager selection after restart: %+v %v", sess, err)
	}
	sess, err = reloaded.Start(ctx, StartParams{PermissionMode: "default", Untitled: true})
	if err != nil || sess.PermissionMode != "default" {
		t.Fatalf("explicit selection: %+v %v", sess, err)
	}
}

func TestAssistantBypassIrreversibleQuestionsPostgres(t *testing.T) {
	old := askInlineSlack
	askInlineSlack = 2 * time.Minute
	t.Cleanup(func() { askInlineSlack = old })
	for _, question := range []string{"Write production data?", "Send an external message?", "Delete stored data?", "Spend money?"} {
		t.Run(question, func(t *testing.T) {
			e := newAskTodoEnv(t, todoTestDB(t))
			setSettings(t, e.s, func(v *Settings) { v.DefaultPermissionMode = proto.ModeBypass })
			e.sessions("server", proto.Session{SessionID: "m1", Assistant: true, PermissionMode: proto.ModeBypass},
				proto.Session{SessionID: "c1x1x1", Assistant: true, PermissionMode: proto.ModeBypass})
			args := strings.Replace(fmt.Sprintf(askDeadlineArgsJSON, "1m", "false"), "Merge the release PR now?", question, 1)
			ix, text := e.expireWith(args)
			if ix.ToolName != "ask_user" || ix.Kind != proto.IxQuestion || !strings.Contains(text, "nothing was taken") {
				t.Fatalf("irreversible decision was not retained: %+v %q", ix, text)
			}
			todo := e.onlyAskTodo()
			e.s.sweepDecisionDeadlines(time.Now().Add(48 * time.Hour))
			if cur := e.get(todo.ID); cur == nil || cur.DoneAt != nil {
				t.Fatal("irreversible decision no longer waits for the user")
			}
		})
	}
}
