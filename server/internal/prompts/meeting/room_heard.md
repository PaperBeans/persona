{{- /* A heard question for a meeting's room (docs/assistant/meeting-minutes.md "Asking the room"), message source
meeting: the lines of the 45 seconds before the tap in a context block, each through ctx, and a line asking for the
question just asked in the room. */ -}}
Someone in the room just asked a question. Answer the latest question in these lines that is addressed to Persona or to {{.Owner}}; if they hold none, say in one sentence that you did not catch a question.

<persona-context kind="context" title="What was just said ({{.At}})" url="">
{{range .Lines}}[{{.At}}] {{ctx .Text}}
{{end}}</persona-context>
