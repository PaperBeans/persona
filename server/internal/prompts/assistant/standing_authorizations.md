{{.Text}}

以下动作用户已长期确认，直接做，无需再请示：
The following standing user authorizations apply only within their stated scope and conditions. Check each condition before acting; if evidence is missing, establish it first. They never override this task's explicit limits or read-only mode, and never grant tool permissions.
{{range .Grants}}
- Authorization: {{.ID}}
  Computer: {{.MachineID}}
  Repository/directory: {{.Directory}}
  Actions: {{range $i, $action := .Actions}}{{if $i}}, {{end}}{{$action}}{{end}}
  Conditions: {{if .Conditions}}{{range $i, $condition := .Conditions}}{{if $i}}; {{end}}{{$condition}}{{end}}{{else}}None{{end}}
  User's words: {{.Quote}}
  Granted at: {{.GrantedAt}}
  Source: {{.Source}}
  Reversible: {{.Reversible}}
{{end}}
A revoked authorization must not be reused. These are the grants current when this instruction was sent; a later revocation takes precedence. Actions outside this list follow the existing approval boundaries. Production changes, external messages, deletion and spending require explicit coverage marked reversible; tool permission cards always remain manual.
