{{if .Project}}项目：{{.Name}}{{with .Goal}}
目标：{{.}}{{end}}

# 时间线（第 {{.From}}–{{.To}} 条，共 {{.Total}} 条）
{{join .Lines "\n"}}
{{else}}会话：{{.Name}}

# 压缩上下文
{{.Summary}}
{{end}}
# 已有主题
{{range .Topics}}- {{.Name}}{{with .Aliases}}（{{join . "、"}}）{{end}}{{with .Note}}：{{.}}{{end}}
{{else}}（还没有）
{{end}}
