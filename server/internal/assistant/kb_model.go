package assistant

import "github.com/nwverse/persona/server/internal/kb"

// kbModel is the knowledge base's model for one purpose (kb.compress, kb.project, kb.items, kb.dedupe, kb.moments, kb.topics):
// the assistant's model configuration (models.go), resolved at every call. A paused model comes back as one
// (ai.IsPaused).
func (s *Service) kbModel(purpose string) kb.Model { return s.purposeModel(purpose) }
