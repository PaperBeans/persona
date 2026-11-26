package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

const secretRunEnvArgs = `{"machine":"Mac mini","purpose":"Authenticate the one-off LAN CLI",` +
	`"commands":["printf 'first\\n'","printf 'second\\n'"],"delivery":"env"}`

// secretRunEnvCard accepts either a card or an early tool result. On the untouched base the early result makes the
// missing env support a prompt assertion failure instead of a timeout waiting for a card that can never appear.
func secretRunEnvCard(t *testing.T, r *secretRig, done chan proto.ToolResult) proto.Interaction {
	t.Helper()
	var ix proto.Interaction
	var early *proto.ToolResult
	waitFor(t, "the env password card or an early refusal", func() bool {
		select {
		case res := <-done:
			early = &res
			return true
		default:
		}
		questions := r.s.pendingQuestions()
		if len(questions) != 1 {
			return false
		}
		got, ok := r.s.pendingIx(questions[0].ID)
		ix = got
		return ok
	})
	if early != nil {
		t.Fatalf("env delivery returned before a fresh password card: %s", resultText(*early))
	}
	waitAnnounced(t, r.f, ix.ID)
	return ix
}

// secretRunEnvPost answers through the manager route with the vouched full-device approval by default. Passing nil
// simulates an older client that cannot prove it rendered the env card.
func secretRunEnvPost(t *testing.T, r *secretRig, id, body string, proof ...*assistantwire.HandoffApprover) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/interactions/"+id+"/resolve", strings.NewReader(body))
	if len(proof) == 0 {
		proof = append(proof, &assistantwire.HandoffApprover{DeviceID: "device-env", DeviceName: "Phone"})
	}
	if proof[0] != nil {
		req = req.WithContext(assistantwire.WithHandoffApprover(req.Context(), *proof[0]))
	}
	w := httptest.NewRecorder()
	r.s.fleetHandler(http.NotFoundHandler()).ServeHTTP(w, req)
	return w
}

func secretRunEnvSummaryWarns(summary string, phrases []string) bool {
	if !strings.Contains(summary, "PERSONA_SECRET") {
		return false
	}
	lower := strings.ToLower(summary)
	for _, phrase := range phrases {
		if strings.Contains(lower, strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}

// A complete env run carries delivery in preflight before the password is typed, shows the risk on the server
// summary, and sends the exact approved commands and one-use password to the node. Manager-side output and history
// retain the same withholding and redaction as stdin.
func TestSecretRunEnvRunsOnlyAfterFreshWarningAndApproval(t *testing.T) {
	r := newSecretRig(t, nil)
	r.f.mu.Lock()
	r.f.hello.Locale = "en"
	r.f.mu.Unlock()
	var checkDeliveries, runDeliveries []string
	r.f.answerNode(proto.MethodSecretRun, func(machine string, params json.RawMessage) (any, error) {
		var wire struct {
			Check    bool   `json:"check"`
			Delivery string `json:"delivery"`
		}
		_ = json.Unmarshal(params, &wire)
		r.mu.Lock()
		if wire.Check {
			checkDeliveries = append(checkDeliveries, wire.Delivery)
		} else {
			runDeliveries = append(runDeliveries, wire.Delivery)
		}
		r.mu.Unlock()
		return r.node(machine, params)
	})

	done := r.call(secretRunEnvArgs)
	ix := secretRunEnvCard(t, r, done)
	r.mu.Lock()
	checks := append([]string(nil), checkDeliveries...)
	r.mu.Unlock()
	if len(checks) != 1 || checks[0] != "env" {
		secretRunEnvPost(t, r, ix.ID, `{"allow":false}`)
		t.Fatalf("preflight deliveries before typing = %v, want exactly [env]", checks)
	}
	var input struct {
		Delivery string   `json:"delivery"`
		Commands []string `json:"commands"`
	}
	if err := json.Unmarshal(ix.ToolInput, &input); err != nil {
		t.Fatal(err)
	}
	if input.Delivery != "env" || len(input.Commands) != 2 {
		t.Fatalf("env tool_input = %s", ix.ToolInput)
	}
	if !secretRunEnvSummaryWarns(ix.Summary, []string{"every command", "each command", "all commands"}) {
		t.Fatalf("env summary %q does not warn that every listed command receives PERSONA_SECRET", ix.Summary)
	}
	for _, command := range []string{"printf 'first\\n'", "printf 'second\\n'"} {
		if !strings.Contains(ix.Summary, "\n$ "+command) {
			t.Fatalf("env summary %q omits approved command %q", ix.Summary, command)
		}
	}
	if w := secretRunEnvPost(t, r, ix.ID, `{"allow":true,"values":{"password":"`+testPassword+`"}}`, nil); w.Code != http.StatusForbidden ||
		!strings.Contains(w.Body.String(), "secret_run_env_elsewhere") {
		t.Fatalf("an older client's unfeatured env approval: %d %s", w.Code, w.Body)
	}
	if _, pending := r.s.pendingIx(ix.ID); !pending {
		t.Fatal("an unfeatured env approval consumed the card")
	}
	if checks, runs, _ := r.counts(); checks != 1 || runs != 0 {
		t.Fatalf("an unfeatured env approval reached execution: checks %d, runs %d", checks, runs)
	}
	if w := secretRunEnvPost(t, r, ix.ID, `{"allow":true,"values":{"password":"`+testPassword+`"}}`); w.Code != 200 {
		t.Fatalf("answer: %d %s", w.Code, w.Body)
	}
	out, text := r.outcome(done)
	if out.Status != secretRan || len(out.Results) != 2 {
		t.Fatalf("env outcome = %s", text)
	}
	for i, result := range out.Results {
		if result.Command != input.Commands[i] || result.Output != "" || !result.OutputWithheld {
			t.Errorf("env result %d = %+v, want approved command and withheld password-bearing output", i+1, result)
		}
	}
	r.mu.Lock()
	if len(r.runs) != 1 {
		r.mu.Unlock()
		t.Fatalf("node runs = %d, want one", len(r.runs))
	}
	run := r.runs[0]
	runs := append([]string(nil), runDeliveries...)
	r.mu.Unlock()
	if string(run.Password) != testPassword || len(run.Commands) != 2 || len(runs) != 1 || runs[0] != "env" {
		t.Fatalf("node run = %+v, delivery fields %v", run, runs)
	}

	if w := secretRunEnvPost(t, r, ix.ID, `{"allow":true,"values":{"password":"another-SECRET-pw"}}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"already":true`) {
		t.Fatalf("second env answer: %d %s", w.Code, w.Body)
	}
	if checks, runs, _ := r.counts(); checks != 1 || runs != 1 {
		t.Fatalf("after replay: checks %d, runs %d; want one each", checks, runs)
	}
	status, kept, _ := r.mem.InteractionOutcome(context.Background(), ix.ID)
	if status != "resolved" || kept == nil || kept.Values["password"] != redactedValue || len(kept.Values) != 1 {
		t.Fatalf("env history: %s %+v", status, kept)
	}
	waitFor(t, "the env answer told to the assistant", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.resolved) > 0
	})
	r.noLeak(text, testPassword, "another-SECRET-pw")
}

// Watch and older clients use Summary rather than knowing the new delivery mode. Each server language must explicitly
// warn that every listed command receives PERSONA_SECRET.
func TestSecretRunEnvSummaryWarnsInEveryServerLanguage(t *testing.T) {
	for _, tc := range []struct {
		lang    string
		phrases []string
	}{
		{lang: "en", phrases: []string{"every command", "each command", "all commands"}},
		{lang: "zh-Hans", phrases: []string{"每个命令", "每条命令", "每一条命令", "所有命令"}},
		{lang: "ja", phrases: []string{"すべてのコマンド", "全てのコマンド", "各コマンド"}},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			r := newSecretRig(t, nil)
			r.f.mu.Lock()
			r.f.hello.Locale = tc.lang
			r.f.mu.Unlock()
			done := r.call(secretRunEnvArgs)
			ix := secretRunEnvCard(t, r, done)
			if !secretRunEnvSummaryWarns(ix.Summary, tc.phrases) {
				t.Errorf("%s env summary %q does not warn that every listed command receives PERSONA_SECRET", tc.lang, ix.Summary)
			}
			secretRunEnvPost(t, r, ix.ID, `{"allow":false}`)
			if out, text := r.outcome(done); out.Status != secretDeclined {
				t.Fatalf("decline after reading the summary = %s", text)
			}
			r.wantNoRun()
		})
	}
}

// The original summary has no env warning: omitted or explicit stdin never implies that commands receive the variable.
func TestSecretRunStdinSummaryHasNoEnvWarning(t *testing.T) {
	for _, args := range []string{
		secretArgs,
		`{"machine":"Mac mini","purpose":"Install the launch daemon for the backup job","commands":["sudo -S -k -p '' true"],"delivery":"stdin"}`,
	} {
		r := newSecretRig(t, nil)
		done := r.call(args)
		ix := r.card()
		if strings.Contains(ix.Summary, "PERSONA_SECRET") {
			t.Errorf("stdin summary %q unexpectedly warns about PERSONA_SECRET", ix.Summary)
		}
		secretRunEnvPost(t, r, ix.ID, `{"allow":false}`)
		if out, text := r.outcome(done); out.Status != secretDeclined {
			t.Fatalf("stdin decline = %s", text)
		}
	}
}

// Similar spellings are not silent aliases for the deliberate env opt-in.
func TestSecretRunRefusesUnknownDeliveryBeforeCardOrRun(t *testing.T) {
	for _, delivery := range []string{"environment", "ENV", "env "} {
		t.Run(delivery, func(t *testing.T) {
			r := newSecretRig(t, nil)
			args := `{"machine":"Mac mini","purpose":"One-off authentication","commands":["true"],"delivery":"` + delivery + `"}`
			res := <-r.call(args)
			text := resultText(res)
			if !res.IsError || !strings.Contains(text, "delivery") {
				t.Fatalf("unknown delivery result = %q, want a delivery refusal", text)
			}
			if len(r.s.pendingQuestions()) != 0 || len(r.s.pendingInteractions()) != 0 {
				t.Fatal("unknown delivery created a card")
			}
			if checks, runs, _ := r.counts(); checks != 0 || runs != 0 {
				t.Fatalf("unknown delivery reached the node: checks %d, runs %d", checks, runs)
			}
		})
	}
}

// Env delivery needs this run's newly typed password. Remembered and assistant answers are rejected even if they carry
// a value; quick approval surfaces have no fresh field value and cannot run either.
func TestSecretRunEnvRequiresFreshPerRunPasswordAnswer(t *testing.T) {
	yes := true
	cases := []struct {
		name       string
		resolution proto.Resolution
	}{
		{name: "remembered", resolution: proto.Resolution{Allow: &yes, Values: map[string]string{"password": testPassword}, Remembered: true}},
		{name: "assistant", resolution: proto.Resolution{Allow: &yes, Values: map[string]string{"password": testPassword}, AnsweredBy: proto.AnsweredByAssistant}},
	}
	for _, source := range []string{"quick", "notification", "Live Activity", "Siri"} {
		cases = append(cases, struct {
			name       string
			resolution proto.Resolution
		}{name: source, resolution: proto.Resolution{Allow: &yes}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newSecretRig(t, nil)
			done := r.call(secretRunEnvArgs)
			ix := secretRunEnvCard(t, r, done)
			if err := r.s.resolveApproved(context.Background(), ix.ID, tc.resolution, &assistantwire.HandoffApprover{DeviceID: "device-env", DeviceName: "Phone"}); err != nil {
				// A fieldless approval is already invalid at the interaction boundary. It never reaches the tool;
				// decline the still-pending card so this case also leaves no work behind.
				secretRunEnvPost(t, r, ix.ID, `{"allow":false}`)
				if out, text := r.outcome(done); out.Status != secretDeclined {
					t.Fatalf("cleanup after a fieldless env refusal = %s", text)
				}
				r.wantNoRun()
				r.noLeak(err.Error(), testPassword)
				return
			}
			out, text := r.outcome(done)
			if out.Status != secretRefused {
				t.Fatalf("non-fresh env answer = %s, want refused", text)
			}
			r.wantNoRun()
			r.noLeak(text, testPassword)
		})
	}
}

// A channel may decline env delivery but may not supply its password or consume the card's owner action with Run.
func TestSecretRunEnvChannelCanOnlyDecline(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretRunEnvArgs)
	ix := secretRunEnvCard(t, r, done)
	yes, no := true, false
	if _, err := r.s.channelResolve(context.Background(), "chat", ix.ID,
		proto.Resolution{Allow: &yes, Values: map[string]string{"password": testPassword}}, "press-env-1"); err == nil {
		t.Fatal("a channel's env Run was accepted")
	}
	if _, pending := r.s.pendingIx(ix.ID); !pending {
		t.Fatal("the refused env Run ended the card")
	}
	if _, _, owner := r.counts(); owner != 0 {
		t.Fatalf("the refused env Run consumed %d owner actions", owner)
	}
	if _, err := r.s.channelResolve(context.Background(), "chat", ix.ID, proto.Resolution{Allow: &no}, "press-env-2"); err != nil {
		t.Fatal(err)
	}
	if out, text := r.outcome(done); out.Status != secretDeclined {
		t.Fatalf("channel env decline = %s", text)
	}
	r.wantNoRun()
	r.noLeak("", testPassword)
}

func TestSecretRunEnvDeclined(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretRunEnvArgs)
	ix := secretRunEnvCard(t, r, done)
	secretRunEnvPost(t, r, ix.ID, `{"allow":false}`)
	if out, text := r.outcome(done); out.Status != secretDeclined {
		t.Fatalf("env decline = %s", text)
	}
	r.wantNoRun()
}

func TestSecretRunEnvExpiresWithoutRunning(t *testing.T) {
	old := secretRunCardWait
	secretRunCardWait = 300 * time.Millisecond
	t.Cleanup(func() { secretRunCardWait = old })
	r := newSecretRig(t, nil)
	done := r.call(secretRunEnvArgs)
	ix := secretRunEnvCard(t, r, done)
	// Once viewed in the app, the countdown starts and expires it.
	if !r.s.markInteractionSeen(context.Background(), ix.ID) {
		t.Fatal("markInteractionSeen returned false")
	}
	if out, text := r.outcome(done); out.Status != secretExpired {
		t.Fatalf("unanswered env card = %s", text)
	}
	r.wantNoRun()
	if status, _, _ := r.mem.InteractionOutcome(context.Background(), ix.ID); status != "expired" {
		t.Fatalf("env card history = %s, want expired", status)
	}
}
