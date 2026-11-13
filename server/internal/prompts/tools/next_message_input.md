The chat's last messages, oldest first:
{{range .}}
{{if eq .Role "user"}}User{{else}}Assistant{{end}}:
{{.Text}}
{{end}}
What does the user send next?
