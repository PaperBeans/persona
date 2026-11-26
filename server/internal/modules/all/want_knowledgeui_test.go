//go:build !persona_without_knowledge_ui && !persona_core_only

package all

func init() { wantModules = append(wantModules, "knowledge-ui") }
