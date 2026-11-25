package assistant

import (
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

func TestGoldenViewTools(t *testing.T) {
	for _, name := range []string{"view_show", "view_update", "view_get"} {
		tool := prompts.Tool(name)
		golden(t, name, tool.Description+"\n\n"+string(tool.InputSchema)+"\n")
	}
}

// The page tool (Dashboards, #15017) as each language's owner gets it: the same schema, the language guidance of
// shared/mcp_language in English and Japanese, none for the default (zh-Hans) list.
func TestGoldenPageTool(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans", "ja"} {
		tool := prompts.Tool(pageToolName, lang)
		golden(t, "page."+lang, tool.Description+"\n\n"+string(tool.InputSchema)+"\n")
	}
}
