主题：{{.Name}}{{with .Aliases}}（{{join . "、"}}）{{end}}
{{with .Note}}说明：{{.}}
{{end}}{{with .Pinned}}
# 已确认的事实（只读，以此为准）
{{.}}
{{end}}
# 已有知识点（{{len .Points}} 条）
{{range .Points}}- {{.ID}} [{{join .Path " / "}}] {{.Kind}} · {{.Text}}{{with .Why}}（{{.}}）{{end}}{{with .When}} · {{.}}{{end}}
{{else}}（还没有）
{{end}}
# 候选（{{len .Candidates}} 条）
{{range .Candidates}}- {{.ID}} [{{join .Path " / "}}] {{.Kind}} · {{.Text}}{{with .Why}}（{{.}}）{{end}}{{with .When}} · {{.}}{{end}}
{{end}}
