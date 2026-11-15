package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func (s *Service) toolRepositoryAdd(ctx context.Context, machine, self string, raw json.RawMessage) (string, error) {
	var in repositoryInput
	if err := repositoryArguments(raw, &in); err != nil {
		return "", err
	}
	binding, err := in.binding()
	if err != nil {
		return "", s.repositoryToolError(ctx, err)
	}
	doc, err := s.repositoryDocument(ctx)
	if err != nil {
		return "", s.repositoryToolError(ctx, err)
	}
	for _, saved := range doc.Repositories {
		if saved.Identity == binding.Identity {
			return "", s.repositoryToolError(ctx, repositoryProblem("duplicate"))
		}
	}
	shown, _ := json.Marshal(in)
	summary := i18n.T(s.lang(ctx), "assistant-repository_tools.add", i18n.Args{"url": binding.CanonicalURL})
	ok, text, err := s.confirmRepositoryAdd(ctx, machine, self, "repository_add", summary, shown, time.Now().Add(settingCardWait))
	if err != nil || !ok {
		return text, err
	}
	binding, err = s.saveRepository(ctx, "", binding)
	if err != nil {
		return "", s.repositoryToolError(ctx, err)
	}
	out, err := json.Marshal(binding)
	return string(out), err
}

func (s *Service) repositoryToolError(ctx context.Context, err error) error {
	code := "store"
	var problem repositoryProblem
	var check *repository.CheckError
	if errors.As(err, &problem) {
		code = string(problem)
	} else if errors.As(err, &check) {
		code = check.Code
	}
	return fmt.Errorf("repository_%s: %s", code, i18n.T(s.lang(ctx), "assistant-repositories."+code))
}

func (s *Service) confirmRepositoryAdd(ctx context.Context, machine, self, tool, summary string, shown json.RawMessage, expires time.Time) (bool, string, error) {
	if machine == "" || self == "" {
		return false, "", errors.New("a calling conversation is required")
	}
	ctx, cancel := context.WithDeadline(ctx, expires)
	defer cancel()
	now := time.Now().UTC()
	r, err := s.ask(ctx, proto.Interaction{ID: "ix_" + randomHex(6), MachineID: machine, SessionID: self,
		Kind: proto.IxPermission, ToolName: tool, Summary: summary, ToolInput: shown, SessionTitle: "Persona",
		CreatedAt: now, ExpiresAt: expires})
	if err != nil {
		return false, "", err
	}
	if err := ctx.Err(); err != nil {
		return false, "", err
	}
	if r.UserAllowed() {
		return true, "", nil
	}
	if r.UserDenied() {
		return false, "The user declined the change: nothing changed.", nil
	}
	return false, prompts.RenderSettingUnanswered(), nil
}
