//go:build !persona_without_health && !persona_core_only

package all

func init() { wantModules = append(wantModules, "health") }
