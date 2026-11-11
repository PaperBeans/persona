# {{.Name}}
{{with .Note}}
{{.}}
{{end}}{{range .Groups}}
{{if .Heading}}{{.Heading}}
{{if .Points}}
{{end}}{{end}}{{range .Points}}- 【{{if eq .Kind "concept"}}概念{{else if eq .Kind "decision"}}决定{{else if eq .Kind "practice"}}做法{{else if eq .Kind "pitfall"}}坑{{else if eq .Kind "fact"}}事实{{else}}待定{{end}}】{{.Text}}{{with .Why}}（{{.}}）{{end}}{{with .When}} · {{.}}{{end}}
{{end}}{{end}}
