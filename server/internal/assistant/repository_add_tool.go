package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	approvalops "github.com/nwverse/persona/server/internal/approval"
	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func (s *Service) toolRepositoryAdd(ctx context.Context, machine, self string, raw json.RawMessage) (string, error) {
	if machine == "" || self == "" {
		return "", errors.New("a calling conversation is required")
	}
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
	now := time.Now().UTC()
	outcome, err := s.waitPreparedCard(ctx, proto.Interaction{ID: "ix_" + randomHex(6), MachineID: machine, SessionID: self,
		Kind: proto.IxPermission, ToolName: "repository_add", Summary: summary, ToolInput: shown, SessionTitle: "Persona",
		CreatedAt: now, ExpiresAt: now.Add(settingCardWait)}, approvalops.Operation{
		Replacement: machine + "\x00" + self + "\x00repository\x00" + binding.Identity.Provider + "\x00" + binding.Identity.CanonicalURL,
		Denied:      "The user declined the change: nothing changed.",
		Unanswered:  prompts.RenderSettingUnanswered(),
		Apply: func(ctx context.Context, _ proto.Resolution) (string, error) {
			// A duplicate added while the owner read or discussed the card makes
			// this proposal stale; saving the old binding is not an approval of it.
			doc, err := s.repositoryDocument(ctx)
			if err != nil {
				return "", s.repositoryToolError(ctx, err)
			}
			for _, saved := range doc.Repositories {
				if saved.Identity == binding.Identity {
					return "", approvalops.ErrStale
				}
			}
			saved, err := s.saveRepository(ctx, "", binding)
			if err != nil {
				return "", s.repositoryToolError(ctx, err)
			}
			out, err := json.Marshal(saved)
			return string(out), err
		},
	})
	return outcome.Text, err
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
