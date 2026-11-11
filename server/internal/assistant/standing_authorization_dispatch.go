package assistant

import (
	"context"
	"fmt"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// Read grants at dispatch, not while a plan waits to start, so changes made in between apply.
func (s *Service) withStandingAuthorizations(ctx context.Context, machine, cwd, text string) (string, error) {
	grants, err := s.standingForDirectory(ctx, machine, cwd)
	if err != nil {
		return "", fmt.Errorf("read standing authorizations: %w", err)
	}
	if len(grants) == 0 {
		return text, nil
	}
	in := prompts.StandingAuthorizations{Text: text, Grants: make([]prompts.StandingAuthorization, 0, len(grants))}
	for _, grant := range grants {
		in.Grants = append(in.Grants, prompts.StandingAuthorization{
			ID: grant.ID, MachineID: grant.MachineID, Directory: grant.Directory,
			Actions: grant.Actions, Conditions: grant.Conditions, Quote: grant.Quote, Source: grant.Source,
			GrantedAt: grant.GrantedAt.Format(time.RFC3339), Reversible: grant.Reversible,
		})
	}
	return prompts.RenderStandingAuthorizations(in), nil
}
