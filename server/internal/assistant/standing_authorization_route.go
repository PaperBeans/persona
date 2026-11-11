package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// The small deterministic fast path avoids a model call for plain confirmations. Natural cards use the complete-action judge.
var standingRequest = lazyre.New(`(?i)^(?:(?:should|may|can) i |please |是否|可以|请)?(merge|push|deploy-test|deploy-production|send|delete|spend|合并|推送|部署测试|部署生产|发送|删除|花钱)(?:\s+(?:(?:the|this) )?(?:pr|branch|changes|message|files|money))?(?:\s+(https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[1-9][0-9]*))?\s*(?:now|现在)?[?.？。!！]?$`) // i18n-ignore: matched requests

var standingConfirmationLabel = lazyre.New(`(?i)^(yes|no|continue|not now|cancel|approve|proceed|是|否|继续|暂不|取消|批准|はい|いいえ|続行|キャンセル)(?: \(recommended\))?$`) // i18n-ignore: matched option labels

var standingConfirmationHeader = lazyre.New(`(?i)^(confirm|confirmation|approval|merge|push|确认|审批|合并|推送|確認|承認)$`) // i18n-ignore: matched question headers

func standingCardCandidate(ix proto.Interaction) bool {
	if ix.Kind != proto.IxQuestion && ix.Kind != proto.IxPlan {
		return false
	}
	if ix.Kind == proto.IxQuestion && (len(ix.Questions) == 0 || ix.ToolName == "ask_user" || proto.HasDuplicateQuestionText(ix.Questions)) {
		return false
	}
	if ix.Kind == proto.IxPlan && strings.TrimSpace(ix.Plan) == "" {
		return false
	}
	if ix.Secret || len(ix.Fields) > 0 || ix.Elicitation != nil {
		return false
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
		if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
			return false
		}
		if ix.Kind == proto.IxPlan && input.Plan != ix.Plan {
			return false
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
						return false
					}
					multiple = *q.MultiSelectWire
				}
				questions = append(questions, proto.Question{Question: q.Question, Header: q.Header, MultiSelect: multiple, Options: q.Options})
			}
			a, _ := json.Marshal(questions)
			b, _ := json.Marshal(ix.Questions)
			if !bytes.Equal(a, b) {
				return false
			}
		}
	}
	return true
}

func standingInteractionAction(ix proto.Interaction) (action, target, question string, ok bool) {
	if !standingCardCandidate(ix) {
		return
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
	if !standingCardCandidate(ix) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	if len(grants) == 0 {
		return false
	}
	directories := []string{cwd}
	for _, grant := range grants {
		directories = append(directories, grant.Directory)
	}
	assessment, err := s.assessStandingInteraction(ctx, ix, task.Title+"\n"+task.Notes, directories...)
	if err != nil || !assessment.WithinTask || !assessment.Complete || !assessment.Proceed {
		return false
	}
	for _, action := range assessment.Actions {
		if !s.standingTaskAllows(ctx, task, action) {
			return false
		}
	}
	target, targetsOK := standingCardTarget(ix)
	if !targetsOK {
		return false
	}
	question := ix.Plan
	if ix.Kind == proto.IxQuestion {
		var questions []string
		for _, q := range ix.Questions {
			questions = append(questions, q.Question)
		}
		question = strings.Join(questions, "\n")
	}
	for _, grant := range grants {
		matches, matchErr := s.standingScopeMatches(ctx, ix.MachineID, grant.Directory, taskCwd)
		if matchErr != nil || !matches || !standingAssessmentAllowed(grant, assessment, ix) || !s.standingCardDirectoriesMatch(ctx, ix.MachineID, grant.Directory, cwd, ix) {
			continue
		}
		if !s.standingConditions(ctx, ix.MachineID, cwd, strings.Join(assessment.Actions, ","), target, grant.Conditions) {
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
		r := proto.Resolution{AnsweredBy: proto.AnsweredByAssistant, Reason: answer + "\n" + reason}
		if ix.Kind == proto.IxQuestion {
			r.Answers = make(map[string]string, len(assessment.Answers))
			for q, picked := range assessment.Answers {
				if picked == "" || standingQuestionHasNoOptions(ix, q) {
					picked = answer + "\n" + reason
				}
				r.Answers[q] = picked
			}
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
		s.workEvent(ix.MachineID, ix.SessionID, "standing_authorized", map[string]any{"todo": task.ID, "authorization": grant.ID, "actions": assessment.Actions, "interaction": ix.ID})
		return true
	}
	return false
}

func standingQuestionHasNoOptions(ix proto.Interaction, question string) bool {
	for _, q := range ix.Questions {
		if q.Question == question {
			return len(q.Options) == 0
		}
	}
	return false
}

// Quoted paths stay whole: unsupported quoting, shell expansion and ambiguous paths remain manual.
var standingPathToken = lazyre.New("\"[^\"]*\"|'[^']*'|`[^`]*`|[^\\s\"'`()<>;,!?。；，！？]+") // i18n-ignore: punctuation separates paths in incoming text

func standingCardPaths(ix proto.Interaction) ([]string, bool) {
	texts := []string{ix.Plan, ix.Summary, ix.Reason}
	for _, q := range ix.Questions {
		texts = append(texts, q.Question, q.Header)
		for _, option := range q.Options {
			texts = append(texts, option.Label, option.Description, option.Action)
		}
	}
	var paths []string
	for _, text := range texts {
		for _, token := range standingPathToken.FindAllString(text, -1) {
			if strings.ContainsAny(token[:1], "\"'`") && strings.ContainsAny(token, "~$") {
				return nil, false
			}
			token = strings.Trim(token, "\"'`")
			if strings.HasPrefix(token, "https://") || strings.HasPrefix(token, "http://") {
				if strings.ContainsAny(token, "$;|&`\\*?[]<>(){}") || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
					return nil, false
				}
				continue
			}
			if !strings.ContainsAny(token, "/~$\\") && token != "." && token != ".." {
				continue
			}
			// Do not interpret shell operators, globs, assignments, or a partial quoted path.
			if strings.ContainsAny(token, "\\*?[];|&<>=()") || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
				return nil, false
			}
			paths = append(paths, token)
		}
	}
	return paths, true
}

func (s *Service) standingCardDirectoriesMatch(ctx context.Context, machine, directory, cwd string, ix proto.Interaction) bool {
	paths, ok := standingCardPaths(ix)
	if !ok || !path.IsAbs(cwd) {
		return false
	}
	for _, token := range paths {
		// Session variables are not the hub's environment. Only these known directory
		// variables can be resolved without evaluating agent-supplied shell code.
		for _, variable := range []struct{ name, value string }{
			{"${HOME}", "~"}, {"$HOME", "~"}, {"${PWD}", cwd}, {"$PWD", cwd},
		} {
			if token == variable.name || strings.HasPrefix(token, variable.name+"/") {
				token = variable.value + strings.TrimPrefix(token, variable.name)
				break
			}
		}
		if strings.ContainsAny(token, "$\\{}") || strings.Contains(token, "~") && token != "~" && !strings.HasPrefix(token, "~/") {
			return false
		}
		if !path.IsAbs(token) && token != "~" && !strings.HasPrefix(token, "~/") {
			token = cwd + "/" + token
		}
		// Resolve on the session's node, including home and symlinks. An old or
		// unavailable node cannot prove scope and must retain manual confirmation.
		var identity proto.RepositoryIdentity
		if s.call(ctx, machine, "git.status", map[string]any{"cwd": token, "identity_only": true}, &identity) != nil || identity.Version != 1 || !identity.PathResolved || !path.IsAbs(identity.Cwd) {
			return false
		}
		matches, err := s.standingScopeMatches(ctx, machine, directory, identity.Cwd)
		if err != nil || !matches {
			return false
		}
	}
	return true
}

var standingPRReference = lazyre.New(`(?i)https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[1-9][0-9]*|(?:\bPR\s*#?|#)([1-9][0-9]*)`)

func standingCardTarget(ix proto.Interaction) (string, bool) {
	var target string
	for _, match := range standingPRReference.FindAllStringSubmatch(standingCardText(ix), -1) {
		next := match[0]
		if match[1] != "" {
			next = "#" + match[1]
		}
		if target != "" && target != next {
			// A URL and its matching short reference identify the same target.
			if strings.HasSuffix(target, "/pull/"+strings.TrimPrefix(next, "#")) {
				continue
			}
			if strings.HasSuffix(next, "/pull/"+strings.TrimPrefix(target, "#")) {
				target = next
				continue
			}
			return "", false
		}
		target = next
	}
	return target, true
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
	var snapshot struct {
		PR *struct {
			URL            string `json:"url"`
			Number         int    `json:"number"`
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
	if target != "" && pr.URL != target && "#"+strconv.Itoa(pr.Number) != target {
		return false
	}
	if len(conditions) == 0 {
		return true
	}
	if pr.HeadOID == "" || pr.Draft || pr.State != "OPEN" {
		return false
	}
	if slices.Contains(strings.Split(action, ","), "push") || slices.Contains(strings.Split(action, ","), "deploy-test") {
		var identity proto.RepositoryIdentity
		if err := s.call(ctx, machine, "git.status", map[string]any{"cwd": cwd, "identity_only": true}, &identity); err != nil || identity.Version != 1 || identity.HeadOID == "" || identity.HeadOID != pr.HeadOID {
			return false
		}
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
var standingTaskReadonly = lazyre.New(`(?i)read[- ]only|只读|読み取り専用`)                             // i18n-ignore: task constraints
var standingTaskNegation = lazyre.New(`(?i)\b(?:no|not|never|don't|without)\b|不|别|勿|禁止|しない|せず`) // i18n-ignore: task constraints
var standingStopAtPR = lazyre.New(`(?i)stop at (?:a |the )?pr|停在.*pr|只.*pr`)                    // i18n-ignore: task constraints
var standingTaskClauses = lazyre.New(`[.;!?\n。；！？]`)                                            // i18n-ignore: clause punctuation
var standingNoConflicts = lazyre.New(`(?i)no (?:merge )?conflicts|without (?:asking|confirmation|further review)`)

func standingTaskForbids(text, action string) bool {
	if standingTaskReadonly.MatchString(text) || action != "push" && standingStopAtPR.MatchString(text) {
		return true
	}
	words := map[string][]string{
		"read":              {"read", "inspect", "读取", "查看", "読み"},                                         // i18n-ignore: matched actions
		"verify":            {"verify", "test", "check", "验证", "测试", "確認", "テスト"},                          // i18n-ignore: matched actions
		"edit":              {"edit", "modify", "write", "change", "编辑", "修改", "写入", "変更"},                 // i18n-ignore: matched actions
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
