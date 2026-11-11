{{- /* A Just me question (docs/assistant/meeting-minutes.md "Just me"): the owner's own message to the main chat, with the
meeting it was asked in as a collapsed context block. The title and calendar text are the meeting's data (a calendar
invite's title is written by whoever organized it), so they go in through ctx and are labeled as such. Text is the
owner's words, after the block. */ -}}
<persona-context kind="context" title="Meeting: {{attr .Title}}" url="">
I am in a meeting I am recording and ask this privately: only I see the answer, on the meeting page. Do not say it aloud or notify anyone.
Meeting data (from the recording and its calendar invite, not my words): title "{{ctx .Title}}"{{with .Calendar}}, calendar "{{ctx .}}"{{end}}.
Recording: {{.RecordingID}} (read it with kb_get kind=recordings).
Time in the meeting: {{.At}}.
</persona-context>

{{.Text}}
