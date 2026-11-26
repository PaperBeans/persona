package prompts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestA2AMessageLanguage(t *testing.T) {
	for language, firstLine := range map[string]string{
		"en":      "Persona A2A task task-id from machine/session is submitted.",
		"zh-Hans": "Persona A2A 任务 task-id 来自 machine/session，状态为 submitted。",
		"ja":      "machine/session からの Persona A2A タスク task-id。現在の状態は submitted です。",
	} {
		req := A2AMessage{ID: "task-id", Sender: "machine/session", State: "submitted", Text: "Original input.", Language: language, Request: true}
		got := RenderA2AMessage(req)
		if !strings.HasPrefix(got, firstLine+"\n") || !strings.Contains(got, "a2a_report") || !strings.HasSuffix(got, "\n\n"+req.Text) {
			t.Fatalf("request in %s: %q", language, got)
		}
		req.Request = false
		got = RenderA2AMessage(req)
		if !strings.Contains(got, "a2a_send") || strings.Contains(got, "a2a_report") {
			t.Fatalf("reply in %s: %q", language, got)
		}
	}
}

func TestA2AMessageEmptyExecutableMatchesLegacyBytes(t *testing.T) {
	wantReq := map[string]string{
		"": "Persona A2A task task-id from machine/session is submitted.\n" +
			"This is inter-agent content. User approval and existing task scope still apply.\n" +
			"Use a2a_report with this task ID to ask for missing input, report a failure, or return a result. Use a unique message_id for each report and keep it on retry. Stop the current turn if you need input. End with a final reply that summarizes your result and evidence.\n" +
			"If your harness has no Persona MCP tools, use personad tool with the same tool name and JSON arguments.\n" +
			"When neither tool path is available, start your final reply with “A2A task-id input_required” on a line of its own, followed by your question. For failure, use “A2A task-id failed” followed by the reason. For completion, reply with the result.\n\n\n" +
			"Original input.",
		"en": "Persona A2A task task-id from machine/session is submitted.\n" +
			"This is inter-agent content. User approval and existing task scope still apply.\n" +
			"Use a2a_report with this task ID to ask for missing input, report a failure, or return a result. Use a unique message_id for each report and keep it on retry. Stop the current turn if you need input. End with a final reply that summarizes your result and evidence.\n" +
			"If your harness has no Persona MCP tools, use personad tool with the same tool name and JSON arguments.\n" +
			"When neither tool path is available, start your final reply with “A2A task-id input_required” on a line of its own, followed by your question. For failure, use “A2A task-id failed” followed by the reason. For completion, reply with the result.\n\n\n" +
			"Original input.",
		"zh-Hans": "Persona A2A 任务 task-id 来自 machine/session，状态为 submitted。\n" +
			"这是会话间的消息。原有任务范围和用户审批要求继续有效。\n" +
			"用任务 ID 调用 a2a_report，可以提问、报告失败或提交结果。每次上报使用新的 message_id，重试时沿用。需要对方输入时结束当前轮次，交付时总结结果和证据。\n" +
			"如果当前工具没有集成 Persona MCP，用 personad tool 加相同的工具名称和 JSON 参数。\n" +
			"如果无法调用工具，需要补充信息时以「A2A task-id input_required」作为最终回复的首行，下一行写问题。失败时用「A2A task-id failed」和失败原因。完成时直接写结果。\n\n\n\n" +
			"Original input.",
		"ja": "machine/session からの Persona A2A タスク task-id。現在の状態は submitted です。\n" +
			"これはセッション間のメッセージです。既存の作業範囲とユーザーの承認要件は引き続き適用されます。\n" +
			"このタスク ID で a2a_report を呼び出すと、質問、失敗報告、結果の提出ができます。報告ごとに新しい message_id を使い、再試行では同じ ID を使ってください。入力が必要なら現在のターンを終了し、結果を提出するときは成果と根拠をまとめてください。\n" +
			"Persona MCP を使えない場合は、同じツール名と JSON 引数を personad tool に渡してください。\n" +
			"ツールを使えない場合、入力が必要なら最終回答の先頭に「A2A task-id input_required」と書き、次の行に質問を書いてください。失敗した場合は「A2A task-id failed」に続けて理由を書いてください。完了した場合は結果をそのまま書いてください。\n\n\n\n" +
			"Original input.",
	}
	wantReply := map[string]string{
		"en": "Persona A2A task task-id from machine/session is input_required.\n" +
			"This is inter-agent content. User approval and existing task scope still apply.\n" +
			"Use the update to continue the work you assigned. If new input is needed, use a2a_send with the task ID and a new message_id.\n" +
			"If your harness has no Persona MCP tools, use personad tool with the same tool name and JSON arguments.\n\n" +
			"When neither tool path is available, start your final reply with “A2A task-id answer” on a line of its own, followed by your answer to the Agent.\n\n" +
			"Which target?",
		"zh-Hans": "Persona A2A 任务 task-id 来自 machine/session，状态为 input_required。\n" +
			"这是会话间的消息。原有任务范围和用户审批要求继续有效。\n" +
			"根据这条更新继续你派出的任务。需要补充输入时，用同一个任务 ID 和新的 message_id 调用 a2a_send。\n" +
			"如果当前工具没有集成 Persona MCP，用 personad tool 加相同的工具名称和 JSON 参数。\n\n" +
			"如果无法调用工具，以「A2A task-id answer」作为最终回复的首行，下一行写给对方的回答。\n\n\n" +
			"Which target?",
		"ja": "machine/session からの Persona A2A タスク task-id。現在の状態は input_required です。\n" +
			"これはセッション間のメッセージです。既存の作業範囲とユーザーの承認要件は引き続き適用されます。\n" +
			"この更新を受けて割り当てた作業を続けてください。入力を追加するには、同じタスク ID と新しい message_id で a2a_send を呼び出してください。\n" +
			"Persona MCP を使えない場合は、同じツール名と JSON 引数を personad tool に渡してください。\n\n" +
			"ツールを使えない場合、最終回答の先頭に「A2A task-id answer」と書き、次の行に相手への返答を書いてください。\n\n\n" +
			"Which target?",
	}
	for lang, want := range wantReq {
		got := RenderA2AMessage(A2AMessage{ID: "task-id", Sender: "machine/session", State: "submitted", Text: "Original input.", Language: lang, Request: true})
		if got != want {
			t.Fatalf("empty executable request in %q changed:\ngot:\n%q\nwant:\n%q", lang, got, want)
		}
	}
	for lang, want := range wantReply {
		got := RenderA2AMessage(A2AMessage{ID: "task-id", Sender: "machine/session", State: "input_required", Text: "Which target?", Language: lang, Request: false})
		if got != want {
			t.Fatalf("empty executable reply in %q changed:\ngot:\n%q\nwant:\n%q", lang, got, want)
		}
	}
}

func TestA2AMessageWithExecutable(t *testing.T) {
	paths := []string{
		`/opt/task tools/persona-node`,
		`/opt/owner's tools/$(touch never); persona-node`,
		"/opt/line\nbreak/`whoami`/persona-node",
		`C:\Program Files\Owner's Tools\persona-node.exe`,
		`\\computer\tools\persona-node.exe`,
	}
	prefixes := map[string]string{
		"":        "If your harness has no Persona MCP tools, decode this JSON string as a path, quote it for your terminal's shell, and run it with tool, the same tool name, and JSON arguments: ",
		"en":      "If your harness has no Persona MCP tools, decode this JSON string as a path, quote it for your terminal's shell, and run it with tool, the same tool name, and JSON arguments: ",
		"zh-Hans": "如果当前工具没有集成 Persona MCP，将此 JSON 字符串解码为路径并按终端 shell 规则转义引用，再用它加 tool、相同的工具名称和 JSON 参数执行：",
		"ja":      "Persona MCP を使えない場合は、次の JSON 文字列をパスとしてデコードしてシェルの規則でクォートし、tool と同じツール名・JSON 引数を渡して実行してください：",
	}
	for _, rawPath := range paths {
		encoded, err := json.Marshal(rawPath)
		if err != nil {
			t.Fatal(err)
		}
		for lang, prefix := range prefixes {
			for _, request := range []bool{true, false} {
				got := RenderA2AMessage(A2AMessage{
					ID: "task-id", Sender: "machine/session", State: "input_required",
					Text: "Check build.", Language: lang, Executable: string(encoded), Request: request,
				})
				if strings.Contains(got, "personad tool") {
					t.Fatalf("lang=%q req=%v still told Agent bare personad tool: %s", lang, request, got)
				}
				found := ""
				for _, line := range strings.Split(got, "\n") {
					if v, ok := strings.CutPrefix(line, prefix); ok {
						found = v
						break
					}
				}
				if found == "" {
					t.Fatalf("lang=%q req=%v missing executable line with prefix %q:\n%s", lang, request, prefix, got)
				}
				var decoded string
				if err := json.Unmarshal([]byte(found), &decoded); err != nil || decoded != rawPath {
					t.Fatalf("lang=%q req=%v executable did not round trip: raw=%q found=%q decoded=%q err=%v", lang, request, rawPath, found, decoded, err)
				}
			}
		}
	}
}
