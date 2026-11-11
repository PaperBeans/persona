package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Only complete, single-action confirmation requests have a deterministic interpretation. General plans and choices
// keep their ordinary route: finding one authorized verb somewhere in a plan does not authorize the whole plan.
var standingRequest = regexp.MustCompile(`(?i)^(?:(?:should|may|can) i |please |是否|可以|请)?(merge|push|deploy-test|deploy-production|send|delete|spend|合并|推送|部署测试|部署生产|发送|删除|花钱)(?:\s+(?:(?:the|this) )?(?:pr|branch|changes|message|files|money))?(?:\s+(https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[1-9][0-9]*))?\s*(?:now|现在)?[?.？。!！]?$`) // i18n-ignore: matched requests

var standingConfirmationLabel = regexp.MustCompile(`(?i)^(yes|no|continue|not now|cancel|approve|proceed|是|否|继续|暂不|取消|批准|はい|いいえ|続行|キャンセル)(?: \(recommended\))?$`) // i18n-ignore: matched option labels

var standingConfirmationHeader = regexp.MustCompile(`(?i)^(confirm|confirmation|approval|merge|push|确认|审批|合并|推送|確認|承認)$`) // i18n-ignore: matched question headers

func standingInteractionAction(ix proto.Interaction) (action, target, question string, ok bool) {
	if ix.Secret || len(ix.Fields) > 0 || ix.Elicitation != nil {
		return
	}
	if len(ix.ToolInput) > 0 {
		var input struct {
			Questions []struct {
				Question        string                 `json:"question"`
				Header          string                 `json:"header"`
				MultiSelect     *bool                  `json:"multiSelect"`
				MultiSelectWire *bool                  `json:"multi_select"`
				Options         []proto.QuestionOption `json:"options"`
			} `json:"questions"`
			Plan string `json:"plan"`
		}
		decoder := json.NewDecoder(bytes.NewReader(ix.ToolInput))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			return
		}
		if ix.Kind == proto.IxPlan && input.Plan != ix.Plan {
			return
		}
		if ix.Kind == proto.IxQuestion {
			questions := make([]proto.Question, 0, len(input.Questions))
			for _, q := range input.Questions {
				multiple := false
				if q.MultiSelect != nil {
					multiple = *q.MultiSelect
				}
				if q.MultiSelectWire != nil {
					if q.MultiSelect != nil && *q.MultiSelect != *q.MultiSelectWire {
						return
					}
					multiple = *q.MultiSelectWire
				}
				questions = append(questions, proto.Question{Question: q.Question, Header: q.Header, MultiSelect: multiple, Options: q.Options})
			}
			a, _ := json.Marshal(questions)
			b, _ := json.Marshal(ix.Questions)
			if !bytes.Equal(a, b) {
				return
			}
		}
	}
	switch ix.Kind {
	case proto.IxQuestion:
		if len(ix.Questions) != 1 || ix.Questions[0].MultiSelect || ix.ToolName == "ask_user" {
			return
		}
		q := ix.Questions[0]
		if q.Header != "" && !standingConfirmationHeader.MatchString(q.Header) {
			return
		}
		// Option descriptions can carry additional operations. Do not silently pick one of them.
		for _, option := range q.Options {
			if option.Description != "" || option.Action != "" || !standingConfirmationLabel.MatchString(strings.TrimSpace(option.Label)) {
				return
			}
		}
		question = q.Question
	case proto.IxPlan:
		question = ix.Plan
	default:
		return
	}
	if ix.Summary != "" && strings.TrimSpace(ix.Summary) != strings.TrimSpace(question) || ix.Reason != "" {
		return
	}
	match := standingRequest.FindStringSubmatch(strings.TrimSpace(question))
	if len(match) == 0 {
		return
	}
	action = strings.ToLower(match[1])
	target = match[2]
	switch action {
	case "合并": // i18n-ignore: matched action
		action = "merge"
	case "推送": // i18n-ignore: matched action
		action = "push"
	case "部署测试": // i18n-ignore: matched action
		action = "deploy-test"
	case "部署生产": // i18n-ignore: matched action
		action = "deploy-production"
	case "发送": // i18n-ignore: matched action
		action = "send"
	case "删除": // i18n-ignore: matched action
		action = "delete"
	case "花钱": // i18n-ignore: matched action
		action = "spend"
	}
	return action, target, question, true
}

type standingResolution struct {
	grant         StandingAuthorization
	decision      db.TodoDecisionRow
	taskUpdatedAt time.Time
}

type standingInteractionStore interface {
	FinishStandingInteraction(context.Context, string, string, proto.Interaction, proto.Resolution, db.TodoDecisionRow, time.Time) error
	GetTodo(context.Context, string) (*db.TodoRow, error)
}

func (s *Service) approveStandingInteraction(ix proto.Interaction) bool {
	action, target, question, ok := standingInteractionAction(ix)
	if !ok {
		return false
	}
	// Runtime-owned local dialogs cannot atomically persist their node-side effect with the manager's decision.
	s.fleet.mu.Lock()
	p := s.fleet.interactions[ix.ID]
	eligible := p != nil && !p.claimed && !p.told && p.local == nil && p.ix.CreatedAt.Equal(ix.CreatedAt)
	s.fleet.mu.Unlock()
	if !eligible {
		return false
	}
	store, ok := s.store().(standingInteractionStore)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, guest, err := s.guestOf(ctx, ix.MachineID, ix.SessionID)
	if err != nil || guest {
		return false
	}
	task, ok := s.taskOf(ctx, ix.MachineID, ix.SessionID)
	if !ok {
		return false
	}
	taskRow, err := store.GetTodo(ctx, task.ID)
	if err != nil || taskRow.DoneAt != nil {
		return false
	}
	task.Title, task.Notes, task.PlanID = taskRow.Title, taskRow.Notes, taskRow.PlanID
	if !s.standingTaskAllows(ctx, task, action) {
		return false
	}
	session, ok := s.session(ctx, ix.MachineID, ix.SessionID)
	if !ok || session.Assistant {
		return false
	}
	cwd := session.Cwd
	if session.WorkspaceCwd != "" {
		cwd = session.WorkspaceCwd
	}
	// The interaction's cwd is supplied by the agent; the node's current session directory is authoritative.
	grants, err := s.standingForDirectory(ctx, ix.MachineID, cwd)
	if err != nil {
		s.logf("standing authorization lookup: %v", err)
		return false
	}
	taskCwd := ""
	for _, binding := range task.Agents {
		if binding.MachineID == ix.MachineID && binding.SessionID == ix.SessionID && binding.ReleasedAt == nil && binding.DismissedAt == nil {
			taskCwd = binding.Cwd
			break
		}
	}
	for _, grant := range grants {
		if taskCwd != grant.Directory && !strings.HasPrefix(taskCwd, grant.Directory+"/") {
			continue
		}
		if !slices.Contains(grant.Actions, action) {
			continue
		}
		if slices.Contains([]string{"deploy-production", "send", "delete", "spend"}, action) && !grant.Reversible {
			continue
		}
		if !s.standingConditions(ctx, ix.MachineID, cwd, action, target, grant.Conditions) {
			continue
		}
		s.standingMu.Lock()
		// A phone revocation while the condition read was in flight must win. The DB transaction checks again under a row lock.
		current, readErr := s.standingForDirectory(ctx, ix.MachineID, cwd)
		active := readErr == nil && slices.ContainsFunc(current, func(g StandingAuthorization) bool { return g.ID == grant.ID })
		if !active {
			s.standingMu.Unlock()
			continue
		}
		l := s.lang(ctx)
		reason := i18n.T(l, "assistant-standing_authorizations.basis", i18n.Args{"id": grant.ID, "quote": grant.Quote, "source": grant.Source})
		answer := i18n.T(l, "assistant-standing_authorizations.continue")
		r := proto.Resolution{AnsweredBy: proto.AnsweredByAssistant, Reason: reason}
		if ix.Kind == proto.IxQuestion {
			r.Answers = map[string]string{question: answer + "\n" + reason}
		} else {
			yes := true
			r.Approve = &yes
			r.Mode = "default"
			r.Feedback = answer + "\n" + reason
		}
		at := decisionNow()
		resolution := &standingResolution{grant: grant, taskUpdatedAt: taskRow.UpdatedAt, decision: db.TodoDecisionRow{ID: "dec_" + randomHex(8), TodoID: task.ID,
			Question: decisionText(question), Choice: decisionText(answer), Reason: decisionText(reason), Source: db.DecisionAgent,
			SourceMachine: ix.MachineID, SourceSession: ix.SessionID, SourceMessage: ix.ID, CreatedAt: at}}
		err := s.resolveWithStanding(ix.ID, r, false, resolution)
		s.standingMu.Unlock()
		if err != nil {
			s.logf("standing authorization %s, interaction %s: %v", grant.ID, ix.ID, err)
			return false
		}
		s.decisionsChanged(ctx, task.ID, at)
		s.workEvent(ix.MachineID, ix.SessionID, "standing_authorized", map[string]any{"todo": task.ID, "authorization": grant.ID, "action": action, "interaction": ix.ID})
		return true
	}
	return false
}

// Conditions are checked against the current branch's complete PR snapshot, never statements in the asking card.
// Unknown user conditions remain useful instructions, but cannot grant automatic approval.
func (s *Service) standingConditions(ctx context.Context, machine, cwd, action, target string, conditions []string) bool {
	for _, c := range conditions {
		if c != "ci_green" && c != "no_conflicts" {
			return false
		}
	}
	if len(conditions) == 0 && target == "" {
		return true
	}
	if action != "merge" && len(conditions) > 0 {
		return false
	}
	var snapshot struct {
		PR *struct {
			URL            string `json:"url"`
			HeadOID        string `json:"head_oid"`
			State          string `json:"state"`
			Draft          bool   `json:"draft"`
			Mergeable      string `json:"mergeable"`
			ChecksComplete bool   `json:"checks_complete"`
			Checks         []struct {
				State string `json:"state"`
			} `json:"checks"`
		} `json:"pr"`
	}
	if err := s.call(ctx, machine, "git.pr", map[string]any{"cwd": cwd}, &snapshot); err != nil || snapshot.PR == nil {
		return false
	}
	pr := snapshot.PR
	if target != "" && pr.URL != target {
		return false
	}
	if len(conditions) == 0 {
		return true
	}
	if pr.HeadOID == "" || pr.Draft || pr.State != "OPEN" {
		return false
	}
	for _, c := range conditions {
		switch c {
		case "no_conflicts":
			if pr.Mergeable != "MERGEABLE" {
				return false
			}
		case "ci_green":
			if !pr.ChecksComplete || len(pr.Checks) == 0 || len(pr.Checks) >= 100 {
				return false
			}
			for _, check := range pr.Checks {
				if check.State != "pass" {
					return false
				}
			}
		}
	}
	return true
}

func (s *Service) persistStandingInteraction(p *pending, r proto.Resolution, a *standingResolution) error {
	st, ok := s.store().(standingInteractionStore)
	var err error
	if !ok {
		err = fmt.Errorf("standing authorization outcome store unavailable")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
		defer cancel()
		err = st.FinishStandingInteraction(ctx, standingAuthorizationsKey, a.grant.ID, p.ix, r, a.decision, a.taskUpdatedAt)
	}
	return s.finishQuestionPersistence(p, err)
}

// Explicit task limits are narrower than a standing grant. Unclear prohibitions keep approval manual.
var standingTaskReadonly = regexp.MustCompile(`(?i)read[- ]only|只读|読み取り専用`)                             // i18n-ignore: task constraints
var standingTaskNegation = regexp.MustCompile(`(?i)\b(?:no|not|never|don't|without)\b|不|别|勿|禁止|しない|せず`) // i18n-ignore: task constraints
var standingStopAtPR = regexp.MustCompile(`(?i)stop at (?:a |the )?pr|停在.*pr|只.*pr`)                    // i18n-ignore: task constraints
var standingTaskClauses = regexp.MustCompile(`[.;!?\n。；！？]`)                                            // i18n-ignore: clause punctuation
var standingNoConflicts = regexp.MustCompile(`(?i)no (?:merge )?conflicts|without (?:asking|confirmation|further review)`)

func standingTaskForbids(text, action string) bool {
	if standingTaskReadonly.MatchString(text) || action != "push" && standingStopAtPR.MatchString(text) {
		return true
	}
	words := map[string][]string{
		"merge":             {"merge", "合并", "合入", "マージ"},                                                  // i18n-ignore: matched actions
		"push":              {"push", "推送", "推到", "プッシュ"},                                                  // i18n-ignore: matched actions
		"deploy-test":       {"deploy", "test", "部署", "发布", "测试", "テスト", "デプロイ"},                           // i18n-ignore: matched actions
		"deploy-production": {"deploy", "production", "release", "部署", "发布", "线上", "生产", "本番", "デプロイ"},     // i18n-ignore: matched actions
		"send":              {"send", "message", "reply", "comment", "发", "发送", "回复", "评论", "送信"},          // i18n-ignore: matched actions
		"delete":            {"delete", "remove", "删除", "删", "削除"},                                         // i18n-ignore: matched actions
		"spend":             {"spend", "pay", "purchase", "buy", "购买", "花钱", "付费", "续费", "充值", "支払", "購入"}, // i18n-ignore: matched actions
	}[action]
	for _, clause := range standingTaskClauses.Split(strings.ToLower(text), -1) {
		clause = standingNoConflicts.ReplaceAllString(clause, "")
		if standingTaskNegation.MatchString(clause) && containsAnyText(clause, words) {
			return true
		}
	}
	return false
}

func (s *Service) standingTaskAllows(ctx context.Context, task Todo, action string) bool {
	if standingTaskForbids(task.Title+"\n"+task.Notes, action) {
		return false
	}
	st := s.todoDB()
	if st == nil {
		return false
	}
	decisions, err := st.ListTodoDecisions(ctx, task.ID, false)
	if err != nil {
		return false
	}
	for _, decision := range decisions {
		if standingTaskForbids(decision.Question+"\n"+decision.Choice+"\n"+decision.Reason, action) {
			return false
		}
	}
	if task.PlanID == "" {
		return true
	}
	plan, err := s.loadPlan(ctx, task.PlanID)
	if err != nil || plan == nil {
		return false
	}
	for _, entry := range plan.Tasks {
		if entry.TodoID == task.ID {
			return !entry.Readonly
		}
	}
	return false
}
