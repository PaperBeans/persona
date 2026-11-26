[To-do context handoff · internal context]
You are continuing existing to-do {{ctx .TodoID}} ({{ctx .TodoTitle}}) on a replacement provider/launcher.
- Handoff ID: {{ctx .HandoffID}} (operation {{ctx .OperationID}}, strategy {{ctx .Strategy}}, generator {{ctx .Generator}})
{{- if .SupersedesID}}
- Supersedes handoff: {{ctx .SupersedesID}}
{{- end}}
{{- if or .SourceLauncher .SourceSession}}
- Previous execution: {{if .SourceMachine}}{{ctx .SourceMachine}}/{{end}}{{ctx .SourceSession}}{{if .SourceLauncher}} (launcher {{ctx .SourceLauncher}}{{if .SourceAgent}}, {{ctx .SourceAgent}}{{end}}){{else if .SourceAgent}} ({{ctx .SourceAgent}}){{end}}
{{- end}}
{{- if or .TargetLauncher .TargetMachine}}
- Replacement target: {{if .TargetMachine}}{{ctx .TargetMachine}} · {{end}}launcher {{ctx .TargetLauncher}}{{if .TargetAgent}} ({{ctx .TargetAgent}}){{end}}
{{- end}}
{{- if .BriefRevision}}
- Brief revision: {{ctx .BriefRevision}}
{{- end}}
Verify the actual workspace before editing: when this deterministic summary conflicts with the workspace, the workspace wins. Do not assume unrecorded approvals were granted.
{{- if .KnownFailure}}

== Why the previous execution stopped ==
{{ctx .KnownFailure}}
{{- end}}
{{- if or .Goal .Request .Criteria .OutOfBounds}}

== Task brief ==
{{- if .Goal}}
Goal: {{ctx .Goal}}
{{- end}}
{{- if .Request}}
Owner request: {{ctx .Request}}
{{- end}}
{{- if .Criteria}}
Effective criteria:
{{- range .Criteria}}
- {{ctx .}}
{{- end}}
{{- end}}
{{- if .OutOfBounds}}
Out of bounds:
{{- range .OutOfBounds}}
- {{ctx .}}
{{- end}}
{{- end}}
{{- end}}
{{- if .Decisions}}

== Recorded decisions ==
{{- range .Decisions}}
- [{{ctx .By}}] {{ctx .Question}} -> {{ctx .Choice}}{{if .Reason}} ({{ctx .Reason}}){{end}}
{{- end}}
{{- end}}
{{- if or .SourceRanges .Files .Commands .Failed .RecentTurns}}

== Predecessor transcript evidence ==
{{- range .SourceRanges}}
- Source range: {{ctx .MachineID}}/{{ctx .SessionID}}{{if .Agent}} ({{ctx .Agent}}){{end}} seq {{.FromSeq}}..{{.ToSeq}} ({{.ItemCount}} items{{if .Truncated}}, truncated{{end}})
{{- end}}
{{- if .Files}}
Files touched in transcript:
{{- range .Files}}
- {{ctx .}}
{{- end}}
{{- end}}
{{- if .Commands}}
Commands run (most recent):
{{- range .Commands}}
- {{ctx .}}
{{- end}}
{{- end}}
{{- if .Failed}}
Failed commands/tests:
{{- range .Failed}}
- {{ctx .}}
{{- end}}
{{- end}}
{{- if .RecentTurns}}
Recent turns:
{{- range .RecentTurns}}
- [{{ctx .Speaker}}] {{ctx .Text}}
{{- end}}
{{- end}}
{{- end}}
{{- if .DiffBase}}

== Workspace snapshot at handoff time ==
Diff scope: {{ctx .DiffScope}} · base {{ctx .DiffBase}}{{if .DiffHead}} · head {{ctx .DiffHead}}{{end}}{{if .DiffBranch}} (branch {{ctx .DiffBranch}}){{end}}
{{- if .DiffFiles}}
Changed files:
{{- range .DiffFiles}}
- {{ctx .}}
{{- end}}
{{- else}}
No uncommitted file changes relative to base.
{{- end}}
{{- if .DiffTruncated}}
(workspace diff truncated)
{{- end}}
{{- end}}
{{- if .MissingSources}}

== Missing or truncated sources ==
{{- range .MissingSources}}
- {{ctx .Source}}: {{ctx .Reason}}
{{- end}}
{{- end}}

== End of to-do context handoff; the task prompt follows ==
