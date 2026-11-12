<persona-context kind="engine-switch" title="Handoff" from="{{attr .From}}" to="{{attr .To}}"{{with .Model}} model="{{attr .}}"{{end}}{{if .Resumed}} resumed="1"{{end}}>
{{ctx .Text}}
</persona-context>

{{.Message}}
