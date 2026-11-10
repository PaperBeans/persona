package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Behaviour checks for delegation (#2586): testdata/judge/delegate.json holds written-out agent questions with what the
// hub should end with, the assistant answering for the user or leaving it to them. TestDelegateScenariosAreWellFormed
// runs everywhere and keeps the cases consistent with the hub's own rules; TestDelegateJudgeLive asks a real model and
// runs only when PERSONA_LIVE_AI is set:
//
//	PERSONA_LIVE_AI='{"base_url":"…","api_key_file":"…","model":"…"}' go test -run DelegateJudgeLive -v ./internal/assistant
//
// docs/assistant/prompts.md ("Behaviour checks") says when to run it.

const (
	delegateAnswer = "answer" // the assistant answers for the user
	delegateAsk    = "ask"    // the user is asked
)

type delegateScenario struct {
	Name      string   `json:"name"`
	Agent     string   `json:"agent"`
	Task      string   `json:"task"`
	Decisions string   `json:"decisions"`
	Asks      []string `json:"asks"`
	Reply     string   `json:"reply"`
	Rules     string   `json:"rules"`
	Questions []struct {
		Question    string `json:"question"`
		MultiSelect bool   `json:"multi_select"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
	Want    []string            `json:"want"`
	Answers map[string][]string `json:"answers"`
	Refused map[string]string   `json:"refused"`
	Known   string              `json:"known"` // why the hub gets this case wrong today: a miss the live check reports, not fails on
}

// interaction is sc's question as the agent's card asks it.
func (sc delegateScenario) interaction() proto.Interaction {
	ix := proto.Interaction{ID: "ix_" + sc.Name, MachineID: "mac", SessionID: "s1", Kind: proto.IxQuestion, TaskTitle: sc.Agent}
	for _, q := range sc.Questions {
		pq := proto.Question{Question: q.Question, MultiSelect: q.MultiSelect}
		for _, o := range q.Options {
			pq.Options = append(pq.Options, proto.QuestionOption{Label: o.Label, Description: o.Description})
		}
		ix.Questions = append(ix.Questions, pq)
	}
	return ix
}

// input is what the model reads about sc, as delegateInput gathers it.
func (sc delegateScenario) input() prompts.AgentDelegateInput {
	in := prompts.AgentDelegateInput{Now: "2026-11-10 10:00 Tue", Agent: sc.Agent, Machine: "mac", Task: sc.Task,
		Decisions: sc.Decisions, Asks: sc.Asks, Reply: sc.Reply}
	for _, q := range sc.Questions {
		dq := prompts.DelegateQuestion{Question: q.Question, MultiSelect: q.MultiSelect}
		for _, o := range q.Options {
			dq.Options = append(dq.Options, prompts.DelegateOption{Label: o.Label, Description: o.Description})
		}
		in.Questions = append(in.Questions, dq)
	}
	return in
}

// judge runs delegation on sc as the hub does, with m as the model.
func (sc delegateScenario) judge(ctx context.Context, m jsonModel) (DelegateResult, error) {
	return judgeDelegateWith(ctx, m, prompts.AgentDelegate{Owner: "Alex", Rules: sc.Rules}, sc.input(), sc.interaction())
}

func delegateDecision(res DelegateResult) string {
	if res.Answer {
		return delegateAnswer
	}
	return delegateAsk
}

// mismatch says how res misses sc's expectation; "" when it meets it.
func (sc delegateScenario) mismatch(res DelegateResult) string {
	got := delegateDecision(res)
	if !slices.Contains(sc.Want, got) {
		return fmt.Sprintf("%s, want %s", got, strings.Join(sc.Want, " or "))
	}
	if !res.Answer {
		return ""
	}
	var miss []string
	for _, q := range sc.Questions {
		for _, label := range strings.Split(res.Answers[q.Question], ", ") {
			if !slices.Contains(sc.Answers[q.Question], label) {
				miss = append(miss, fmt.Sprintf("answered %q with %q, want one of %v", q.Question, label, sc.Answers[q.Question]))
			}
		}
	}
	return strings.Join(miss, "; ")
}

func loadDelegateScenarios(t *testing.T) []delegateScenario {
	t.Helper()
	f, err := os.Open("testdata/judge/delegate.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var file struct {
		About     string             `json:"about"`
		Scenarios []delegateScenario `json:"scenarios"`
	}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields() // a misspelt field would otherwise mean nothing, silently
	if err := dec.Decode(&file); err != nil {
		t.Fatal(err)
	}
	return file.Scenarios
}

// delegateAnswerJSON is a model's answer that picks, for each question, picked's label or else the first one sc accepts.
func delegateAnswerJSON(sc delegateScenario, picked map[string]string) string {
	answers := map[string]string{}
	for _, q := range sc.Questions {
		answers[q.Question] = picked[q.Question]
		if answers[q.Question] == "" && len(sc.Answers[q.Question]) > 0 {
			answers[q.Question] = sc.Answers[q.Question][0]
		}
		if answers[q.Question] == "" {
			answers[q.Question] = q.Options[0].Label
		}
	}
	b, _ := json.Marshal(map[string]any{"decision": delegateAnswer, "answers": answers, "reason": "as expected"})
	return string(b)
}

// TestDelegateScenariosAreWellFormed keeps the cases honest without a model: names are unique, each question is one a
// card can ask, the answers a case accepts are its options, a model answering as the case expects leaves the hub with
// that decision and those answers, and an answer the case says the hub refuses (a red line by its words) is left to
// the user even when the model gives it.
func TestDelegateScenariosAreWellFormed(t *testing.T) {
	scenarios := loadDelegateScenarios(t)
	if len(scenarios) < 15 {
		t.Fatalf("%d scenarios, want at least 15", len(scenarios))
	}
	seen := map[string]bool{}
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			if sc.Name == "" || seen[sc.Name] || strings.TrimSpace(sc.Task) == "" || strings.TrimSpace(sc.Reply) == "" ||
				len(sc.Questions) == 0 || len(sc.Want) == 0 {
				t.Fatalf("needs a unique name, a task, a reply, a question and what it wants: %+v", sc)
			}
			seen[sc.Name] = true
			for _, w := range sc.Want {
				if w != delegateAnswer && w != delegateAsk {
					t.Fatalf("want %q: answer or ask", w)
				}
			}
			options := map[string][]string{}
			for _, q := range sc.Questions {
				if strings.TrimSpace(q.Question) == "" || options[q.Question] != nil || len(q.Options) < 2 || len(q.Options) > 4 {
					t.Fatalf("question %q: unique, with 2 to 4 options", q.Question)
				}
				for _, o := range q.Options {
					if strings.TrimSpace(o.Label) == "" || slices.Contains(options[q.Question], o.Label) {
						t.Fatalf("question %q: option labels are unique and not empty", q.Question)
					}
					options[q.Question] = append(options[q.Question], o.Label)
				}
			}
			for q, labels := range sc.Answers {
				if options[q] == nil || len(labels) == 0 {
					t.Fatalf("answers name %q, not one of its questions, or accept nothing", q)
				}
				for _, l := range labels {
					if !slices.Contains(options[q], l) {
						t.Fatalf("answers accept %q for %q, not one of its options %v", l, q, options[q])
					}
				}
			}
			if sc.Known != "" && strings.TrimSpace(sc.Known) == "" {
				t.Fatal("known says why the hub misses it")
			}
			if slices.Contains(sc.Want, delegateAnswer) != (len(sc.Answers) == len(sc.Questions)) {
				t.Fatalf("answers are listed for every question exactly when an answer passes")
			}
			for q, l := range sc.Refused {
				if !slices.Contains(options[q], l) || slices.Contains(sc.Answers[q], l) {
					t.Fatalf("refused %q for %q: one of its options, and not an answer that passes", l, q)
				}
			}

			// A model that decides as the case expects.
			answer := `{"decision":"ask","what":"as expected","why_now":"as expected","recommend":"","cost":"as expected","reason":"as expected"}`
			if sc.Want[0] == delegateAnswer {
				answer = delegateAnswerJSON(sc, nil)
			}
			res, err := sc.judge(context.Background(), &cannedJSON{answer: answer})
			if err != nil {
				t.Fatal(err)
			}
			if got := delegateDecision(res); got != sc.Want[0] {
				t.Fatalf("a model answering %s leaves the hub with %s, not the case's first %s", answer, got, sc.Want[0])
			}
			if miss := sc.mismatch(res); miss != "" {
				t.Fatalf("a model answering %s: %s", answer, miss)
			}
			if len(sc.Refused) > 0 {
				answer := delegateAnswerJSON(sc, sc.Refused)
				res, err := sc.judge(context.Background(), &cannedJSON{answer: answer})
				if err != nil {
					t.Fatal(err)
				}
				if res.Answer || res.Ask == nil || res.Ask.Recommend == "" {
					t.Fatalf("a model answering %s: %+v, want it left to the user with that answer recommended", answer, res)
				}
			}
		})
	}
}

// judgeDelegateWith gives the model the user's rules and the question, and nothing it answers reaches the agent
// unless the hub's rules let it (delegateResult, TestDelegateResult has the cases).
func TestJudgeDelegateWith(t *testing.T) {
	sys := prompts.AgentDelegate{Owner: "Alex", Rules: "Add missing tests without asking"}
	in := prompts.AgentDelegateInput{Now: "2026-11-10 10:00 Tue", Agent: "Fix a crash", Machine: "mac", Task: "Fix the crash",
		Questions: []prompts.DelegateQuestion{{Question: "用哪个分支？", Options: []prompts.DelegateOption{{Label: "feat/x"}, {Label: "main"}}}}}
	m := &cannedJSON{answer: `{"decision":"answer","answers":{"用哪个分支？":"feat/x"},"reason":"the task is on feat/x"}`}
	res, err := judgeDelegateWith(context.Background(), m, sys, in, branchQuestion())
	if err != nil || !res.Answer || res.Answers["用哪个分支？"] != "feat/x" || res.Reason != "the task is on feat/x" {
		t.Fatalf("got %+v %v", res, err)
	}
	if m.calls != 1 || m.system != prompts.RenderAgentDelegate(sys) || m.user != prompts.RenderAgentDelegateInput(in) {
		t.Fatalf("the model was asked %d times with system %q user %q", m.calls, m.system, m.user)
	}
	if !strings.Contains(m.system, sys.Rules) {
		t.Fatal("the user's rules are not in the system prompt")
	}

	// A red line is the user's whatever the model says, with its pick recommended.
	m = &cannedJSON{answer: `{"decision":"answer","answers":{"CI 过了，现在合并 PR 吗？":"合并"},"reason":"CI passed"}`}
	if res, err := judgeDelegateWith(context.Background(), m, sys, in, mergeQuestion()); err != nil || res.Answer || res.Ask == nil {
		t.Fatalf("a merge: %+v %v", res, err)
	}

	// The model failing is an error and no decision.
	m = &cannedJSON{err: errors.New("no model")}
	if res, err := judgeDelegateWith(context.Background(), m, sys, in, branchQuestion()); err == nil || res.Answer || res.Ask != nil {
		t.Fatalf("a failing model: %+v %v", res, err)
	}
}

// TestDelegateJudgeLive asks a real model (PERSONA_LIVE_AI) about every case and prints what it decided; a case it
// gets wrong fails, unless the case records it as a known miss. Run it before and after changing delegate.md /
// delegate_input.md, delegateResult, or the model behind agent.delegate, and put both tables in the PR.
func TestDelegateJudgeLive(t *testing.T) {
	raw := os.Getenv("PERSONA_LIVE_AI")
	if raw == "" {
		t.Skip("PERSONA_LIVE_AI not set")
	}
	var cfg ai.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	runDelegateLive(t, ai.New(cfg), cfg.Model)
}

// runDelegateLive judges every case with m (named model in the table) and fails on each case it gets wrong that is
// not a known miss.
func runDelegateLive(t *testing.T, m jsonModel, model string) {
	t.Helper()
	var rows []string
	failed := 0
	for _, sc := range loadDelegateScenarios(t) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		start := time.Now()
		res, err := sc.judge(ctx, m)
		cancel()
		got, reason := delegateDecision(res), ""
		if res.Answer {
			var picks []string
			for _, q := range sc.Questions {
				picks = append(picks, res.Answers[q.Question])
			}
			got += ": " + strings.Join(picks, "; ")
			reason = res.Reason
		} else if res.Ask != nil {
			reason = res.Ask.Reason
		}
		miss := sc.mismatch(res)
		if err != nil {
			got, miss = "error", "error: "+err.Error()
		}
		mark := liveMark(t, sc.Name, miss, sc.Known, reason)
		if mark == "MISS" {
			failed++
		}
		want := strings.Join(sc.Want, "|")
		if slices.Contains(sc.Want, delegateAnswer) {
			var accepted []string
			for _, q := range sc.Questions {
				accepted = append(accepted, strings.Join(sc.Answers[q.Question], "|"))
			}
			want += ": " + strings.Join(accepted, "; ")
		}
		rows = append(rows, fmt.Sprintf("%s %-30s %-26s want %-34s %5.1fs %s", mark, sc.Name, got, want,
			time.Since(start).Seconds(), reason))
	}
	t.Logf("delegation on %s: %d of %d as expected or known\n%s", model, len(rows)-failed, len(rows), strings.Join(rows, "\n"))
}

// liveMark is a live row's mark: ok, MISS (the test fails) or KNOWN (a miss the case records as known: logged, not
// failed). A known miss that passes is logged too, so its mark can be dropped once the fix holds.
func liveMark(t *testing.T, name, miss, known, why string) string {
	t.Helper()
	switch {
	case miss == "" && known != "":
		t.Logf("%s: a known miss passed (%s): drop known once the fix holds", name, known)
	case miss == "":
	case known != "":
		t.Logf("%s: known miss: %s (%s)", name, miss, known)
		return "KNOWN"
	default:
		t.Errorf("%s: %s (%q)", name, miss, why)
		return "MISS"
	}
	return "ok  "
}
