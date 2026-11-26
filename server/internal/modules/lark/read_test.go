package lark

import (
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

// A /read's prompt carries where to read and the limits (the assistant's TestGoldenBotReadGroup renders these as its
// goldens); a message id that is not fit for a directory name unquoted is made so.
func TestReadPromptFillsTheLimits(t *testing.T) {
	got := readPrompt("示例用户", "", "oc_example", "om_example", "omt_example", "看下这几条告警")
	want := prompts.BotReadGroup{Owner: "示例用户", ChatID: "oc_example", MessageID: "om_example", ThreadID: "omt_example",
		Task: "看下这几条告警", Dir: "~/.persona/lark-read/om_example", Default: 40, Most: 500, FileMB: 50, TotalMB: 200, Images: 20}
	if got != want {
		t.Fatalf("readPrompt = %+v, want %+v", got, want)
	}
	if got := readPrompt("", "", "oc_example", "om_a/b c$", "", "").Dir; got != "~/.persona/lark-read/om_a_b_c_" {
		t.Fatalf("dir %q", got)
	}
}
