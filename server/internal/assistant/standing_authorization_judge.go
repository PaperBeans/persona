package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

type standingCardAssessment struct {
	WithinTask bool              `json:"within_task"`
	Complete   bool              `json:"complete"`
	Proceed    bool              `json:"proceed"`
	Actions    []string          `json:"actions"`
	Answers    map[string]string `json:"answers"`
	Reason     string            `json:"reason"`
}

func standingCardText(ix proto.Interaction) string {
	data := struct {
		Questions []proto.Question `json:"questions,omitempty"`
		Plan      string           `json:"plan,omitempty"`
		Summary   string           `json:"summary,omitempty"`
		Reason    string           `json:"reason,omitempty"`
	}{ix.Questions, ix.Plan, ix.Summary, ix.Reason}
	raw, _ := json.Marshal(data)
	return string(raw)
}

func assessStandingWith(ctx context.Context, m jsonModel, ix proto.Interaction, task string, directory ...string) (standingCardAssessment, error) {
	if !standingCardCandidate(ix) {
		return standingCardAssessment{}, nil
	}
	cwd := ""
	if len(directory) > 0 {
		cwd = strings.Join(directory, ", ")
	}
	var out struct {
		WithinTask bool                       `json:"within_task"`
		Complete   bool                       `json:"complete"`
		Proceed    bool                       `json:"proceed"`
		Actions    []string                   `json:"actions"`
		Answers    map[string]json.RawMessage `json:"answers"`
		Reason     string                     `json:"reason"`
	}
	if err := m.JSON(ctx, prompts.RenderStandingActions(), prompts.RenderStandingActionsInput(prompts.StandingActionsInput{Task: task, Card: standingCardText(ix), Directory: cwd}), &out); err != nil {
		return standingCardAssessment{}, err
	}
	a := standingCardAssessment{WithinTask: out.WithinTask, Complete: out.Complete, Proceed: out.Proceed, Actions: out.Actions, Reason: out.Reason}
	if !a.WithinTask || !a.Complete || !a.Proceed {
		return a, nil
	}
	if strings.TrimSpace(a.Reason) == "" || len(a.Actions) == 0 {
		return standingCardAssessment{}, errors.New("incomplete standing action classification")
	}
	for _, action := range a.Actions {
		if !slices.Contains(standingKnownActions, action) {
			return standingCardAssessment{}, errors.New("unknown standing action classification")
		}
	}
	if ix.Kind == proto.IxQuestion {
		// Use the existing exact-label validator, including multi-select and complete question coverage.
		hasOptions := true
		for _, q := range ix.Questions {
			if len(q.Options) == 0 {
				hasOptions = false
			}
		}
		if hasOptions {
			raw, _ := json.Marshal(out.Answers)
			answers, err := interactionAnswers(ix, raw)
			if err != nil {
				return standingCardAssessment{}, err
			}
			a.Answers = answers
		} else {
			a.Answers = map[string]string{}
			for _, q := range ix.Questions {
				var answer string
				if json.Unmarshal(out.Answers[q.Question], &answer) != nil || strings.TrimSpace(answer) == "" {
					return standingCardAssessment{}, errors.New("missing standing question answer")
				}
				if len(q.Options) > 0 && checkOptions(q, answer) != nil {
					return standingCardAssessment{}, errors.New("invalid standing question answer")
				}
				a.Answers[q.Question] = answer
			}
		}
		for _, q := range ix.Questions {
			answer := a.Answers[q.Question]
			for _, picked := range strings.Split(answer, ", ") {
				if standingNegativeAnswer.MatchString(strings.TrimSpace(picked)) {
					return standingCardAssessment{}, errors.New("standing answer holds off the action")
				}
				for _, option := range q.Options {
					if option.Label == picked && option.Action != "" {
						return standingCardAssessment{}, errors.New("selected answer has a runtime action")
					}
				}
			}
		}
	}
	// Code adds conservative sensitive effects even when the model overlooks an incidental step.
	for _, action := range standingSensitiveActions(standingCardText(ix)) {
		if !slices.Contains(a.Actions, action) {
			a.Actions = append(a.Actions, action)
		}
	}
	return a, nil
}

var standingKnownActions = []string{"merge", "push", "deploy-test", "deploy-production", "send", "delete", "spend", "read", "verify", "edit"}
var standingSensitiveWords = []struct {
	action  string
	pattern *lazyre.Regexp
}{
	{"deploy-production", lazyre.New(`(?i)\bproduction\b|\bprod\b|\brelease\b|testflight|app store|生产|线上|上线|发版`)},                                                                                                // i18n-ignore: card action data
	{"send", lazyre.New(`(?i)\bsend\b|\breply\b|\bcomment\b|发送|发消息|发邮件|对外|回复|评论`)},                                                                                                                               // i18n-ignore: card action data
	{"delete", lazyre.New(`(?i)\bdelete\b|\bremove\b|\bdrop\b|force[-\s]+push(?:ing)?|rewrit(?:e|ing)[-\s]+(?:remote[-\s]+)?history|reset\s+--hard|--force(?:-with-lease)?|强推|强制推送|改写历史|重写历史|覆盖远端|删除|删掉|清空|削除`)}, // i18n-ignore: card action data
	{"spend", lazyre.New(`(?i)\bspend\b|\bpurchase\b|\bbuy\b|\bpay\b|购买|花钱|付费|续费|充值|購入|支払`)},                                                                                                                     // i18n-ignore: card action data
}
var standingDeployWords = lazyre.New(`(?i)\bdeploy(?:ment)?\b|部署|发布|デプロイ`)                                                                                                                                                                                                                                           // i18n-ignore: card action data
var standingTestDeployment = lazyre.New(`(?i)\bdeploy-test\b|\bdeploy(?:ment)?\s+[^.;,\n]{0,80}?\bto\s+(?:test(?:ing)?|staging|ppe|boe)\b|\bdeploy(?:ment)?(?:[- ]to)?\s+(?:to\s+)?(?:the\s+)?(?:test(?:ing)?(?:[- ]environment)?|staging|ppe|boe)\b|部署(?:到|至)?(?:测试|预发)|发布(?:到|至)(?:测试|预发)|テスト(?:環境)?(?:に|へ)?デプロイ`) // i18n-ignore: environment data
var standingNegativeAnswer = lazyre.New(`(?i)^(?:no\b|not\b|never\b|don't\b|do not\b|wait\b|hold\b|cancel\b|defer\b|skip\b|stop\b|先不|暂不|不要|不用|别|先别|不|否|取消|等待|いいえ|キャンセル)`)                                                                                                                                            // i18n-ignore: negative options

func standingSensitiveActions(text string) []string {
	var actions []string
	for _, rule := range standingSensitiveWords {
		if rule.pattern.MatchString(text) {
			actions = append(actions, rule.action)
		}
	}
	if standingDeployWords.MatchString(standingTestDeployment.ReplaceAllString(text, "")) && !slices.Contains(actions, "deploy-production") {
		actions = append(actions, "deploy-production")
	}
	return actions
}

func standingAssessmentAllowed(grant StandingAuthorization, a standingCardAssessment, ix proto.Interaction) bool {
	if !standingCardCandidate(ix) || !a.WithinTask || !a.Complete || !a.Proceed || len(a.Actions) == 0 {
		return false
	}
	actions := append(slices.Clone(a.Actions), standingSensitiveActions(standingCardText(ix))...)
	covered := false
	for _, action := range actions {
		if !slices.Contains(standingKnownActions, action) {
			return false
		}
		if slices.Contains([]string{"read", "verify", "edit"}, action) {
			continue
		}
		covered = true
		if !slices.Contains(grant.Actions, action) {
			return false
		}
		if slices.Contains([]string{"deploy-production", "send", "delete", "spend"}, action) && !grant.Reversible {
			return false
		}
	}
	return covered
}

func (s *Service) assessStandingInteraction(ctx context.Context, ix proto.Interaction, task string, directory ...string) (standingCardAssessment, error) {
	if action, _, question, ok := standingInteractionAction(ix); ok {
		answers := map[string]string{question: ""}
		if ix.Kind == proto.IxQuestion && len(ix.Questions[0].Options) > 0 {
			picked := ""
			for _, option := range ix.Questions[0].Options {
				if !standingNegativeAnswer.MatchString(option.Label) {
					if picked != "" {
						return standingCardAssessment{}, errors.New("ambiguous confirmation options")
					}
					picked = option.Label
				}
			}
			if picked == "" {
				return standingCardAssessment{}, errors.New("no affirmative confirmation option")
			}
			answers[question] = picked
		}
		return standingCardAssessment{WithinTask: true, Complete: true, Proceed: true, Actions: []string{action}, Answers: answers, Reason: "Plain action confirmation"}, nil
	}
	m := s.standingJudgeModel
	if m == nil {
		m = s.purposeModel("agent.delegate")
	}
	return assessStandingWith(ctx, m, ix, task, directory...)
}
