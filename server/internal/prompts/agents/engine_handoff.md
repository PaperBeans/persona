[Session handoff · internal context]
{{if .Delta -}}
You ({{ctx .To}}) worked on this conversation before; it was handed over to the {{ctx .From}} engine for a while, and now it is switching back to you. Your memory of the conversation from before you left should still be valid; below is a record of what happened while you were away. Merge it into your understanding and continue naturally in the first person. Do not mention this handoff note to the user, and do not mention the engine switch. Before making any change, check the actual workspace state (e.g. git status / git diff / reading the relevant files): when the record, your own memory, and the workspace disagree, the workspace always wins; when you are unsure about something from while you were away, read the real files and code to confirm rather than guessing from the summary.
{{- else -}}
This conversation was previously driven by the {{ctx .From}} engine; from here on you ({{ctx .To}}) continue it. Below is the prior record of this conversation — it is the same conversation between you and the user, so continue naturally in the first person. Do not mention this handoff note to the user, do not mention the engine switch, and do not say things like "according to the handoff summary/record". Before making any change, check the actual workspace state (e.g. git status / git diff / reading the relevant files): when the record conflicts with the workspace, the workspace always wins; when you are unsure about an earlier detail, read the real files and code to confirm rather than guessing from the summary.
{{- end}}
{{- with .Ledger}}{{if or .Files .Commands .Failed}}

== Work ledger (auto-extracted) ==
{{- if .Files}}
Files changed:
{{- range .Files}}
- {{ctx .}}
{{- end}}
{{- end}}
{{- if .Commands}}
Commands (most recent):
{{- range .Commands}}
- {{template "agents/engine_handoff_command" .}}
{{- end}}
{{- end}}
{{- if .Failed}}
Failed attempts:
{{- range .Failed}}
- {{template "agents/engine_handoff_command" .}}
{{- end}}
{{- end}}
{{- end}}{{end}}
{{- if or .Index .Omitted}}

{{if .Delta}}== Earlier progress while you were away =={{else}}== Earlier conversation index (hints only, not a verified summary) =={{end}}
{{- if .Omitted}}
(earlier content omitted)
{{- end}}
{{- range .Index}}
{{if .Reply}}  Reply: {{else}}- {{template "agents/engine_handoff_speaker" .}}: {{end}}{{ctx .Text}}
{{- end}}
{{- end}}
{{- if .Recent}}

{{if .Delta}}== Conversation while you were away =={{else}}== Recent conversation =={{end}}
{{- range $i, $turn := .Recent}}
{{- if $i}}
---
{{- end}}
{{- if or .Text .Images}}
[{{template "agents/engine_handoff_speaker" .}}]
{{- if .Text}}
{{ctx .Text}}
{{- end}}
{{- if .Images}}
({{.Images}} image{{if gt .Images 1}}s{{end}} attached, not carried over)
{{- end}}
{{- end}}
{{- range .Lines}}
{{if eq .Kind "assistant"}}[Assistant]
{{ctx .Text}}
{{- else if eq .Kind "tool"}}[Tool] {{ctx .Tool}}{{if .ID}} (id={{ctx .ID}}){{end}}{{if .Text}}: {{ctx .Text}}{{end}}
{{- else if eq .Kind "result"}}[Tool result{{if .ID}} id={{ctx .ID}}{{end}}; {{if .Failed}}failed{{else}}recorded; verify outcome from result{{end}}]
{{if .Text}}{{ctx .Text}}{{else}}(no text result retained){{end}}
{{- else}}({{.Omitted}} tool calls omitted)
{{- end}}
{{- end}}
{{- end}}
{{- else if .Delta}}

== No new messages while you were away ==
{{- end}}
{{- if .Transcripts}}

== Retrieving earlier verbatim history (use when needed) ==
{{if .Delta}}What happened while you were away{{else}}This conversation's earlier record{{end}} is kept word for word in {{if gt (len .Transcripts) 1}}these transcript files{{else}}this transcript file{{end}} on this computer:
{{- range .Transcripts}}
- {{ctx .Engine}} session {{ctx .Session}}: {{ctx .Path}}
{{- end}}
The record above is only a hint. Retrieve earlier verbatim text only when an earlier detail is actually needed: search the file for keywords and read only around a hit. Each line is one JSON record and some are very long, so print only part of a matching line (e.g. `grep -o '.\{0,200\}<keyword>.\{0,200\}' <file>`). Never read or print a whole file, and never dump the history into the conversation. Retrieved original text outranks this note; when it conflicts with the current workspace, the workspace wins.
{{- end}}

== End of handoff note; the user's new message follows ==
