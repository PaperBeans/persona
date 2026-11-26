{{if eq .Language "English"}}Project: {{.Name}} ({{.Kind}})
Aliases:{{with .Aliases}} {{join . "、"}}{{end}}
Existing tags:{{with .Tags}} {{join . "、"}}{{end}}

# Existing record
{{if blank .Summary}}(none yet){{else}}{{.Summary}}{{end}}
{{with .Pinned}}
# Confirmed facts (read-only, authoritative, do not rewrite)
{{.}}
{{end}}
# Recent timeline
{{join .Log "\n"}}
{{with .Memory}}
# Direction memory (newest first; source records)
{{if .Outcomes}}## Outcomes
{{range .Outcomes}}- [{{.ID}}] {{.Text}}{{range .Links}} {{.}}{{end}}{{if .When}} · {{.When}}{{end}} · {{.Verification}}{{if .Unaccepted}} · not supervisor-accepted{{end}}
{{end}}{{end}}{{if .Decisions}}## Effective decisions and reasons (distinct IDs stay separate)
{{range .Decisions}}- [{{.ID}}] {{.Question}} → {{.Choice}}; reason: {{if .Reason}}{{.Reason}}{{else}}not recorded{{end}}; result: {{if .Result}}{{.Result}}{{else}}not recorded{{end}}; source time: {{if .When}}{{.When}}{{else}}not recorded{{end}} · {{.Verification}}{{if .Unaccepted}} · not supervisor-accepted{{end}}
{{end}}{{end}}{{if .Pitfalls}}## Pitfalls
{{range .Pitfalls}}- {{.Text}}{{if .When}} · {{.When}}{{end}} · {{.Verification}}{{if .Unaccepted}} · not supervisor-accepted{{end}}
{{end}}{{end}}{{end}}
# Related chats
{{range .Chats}}
## {{.ID}}
{{if .State}}State (recorded by the source): {{if eq .State "merged"}}merged{{else if eq .State "closed"}}closed{{else}}open{{end}}{{with .StateAt}} ({{.}}){{end}}
{{end}}{{.Summary}}
{{end}}{{with .Moments}}
# Related company posts
{{range .}}
- {{.At}} · {{.Author}} · Comments {{.Comments}}{{with .Note}} · {{.}}{{end}}
  {{.Text}}
{{end}}{{end}}{{else if eq .Language "日本語"}}プロジェクト：{{.Name}}（{{.Kind}}）
別名：{{join .Aliases "、"}}
既存のタグ：{{join .Tags "、"}}

# 既存の記録
{{if blank .Summary}}（まだありません）{{else}}{{.Summary}}{{end}}
{{with .Pinned}}
# 確認済みの事実（読み取り専用。最優先で、書き換え禁止）
{{.}}
{{end}}
# 最近のタイムライン
{{join .Log "\n"}}
{{with .Memory}}
# 方向メモリー（新しい順・出典レコード）
{{if .Outcomes}}## 結果
{{range .Outcomes}}- [{{.ID}}] {{.Text}}{{range .Links}} {{.}}{{end}}{{if .When}} · {{.When}}{{end}} · {{.Verification}}{{if .Unaccepted}} · スーパーバイザー未承認{{end}}
{{end}}{{end}}{{if .Decisions}}## 有効な決定と理由（異なる ID は統合しない）
{{range .Decisions}}- [{{.ID}}] {{.Question}} → {{.Choice}}；理由：{{if .Reason}}{{.Reason}}{{else}}記録なし{{end}}；結果：{{if .Result}}{{.Result}}{{else}}記録なし{{end}}；出典時刻：{{if .When}}{{.When}}{{else}}記録なし{{end}} · {{.Verification}}{{if .Unaccepted}} · スーパーバイザー未承認{{end}}
{{end}}{{end}}{{if .Pitfalls}}## 失敗からの教訓
{{range .Pitfalls}}- {{.Text}}{{if .When}} · {{.When}}{{end}} · {{.Verification}}{{if .Unaccepted}} · スーパーバイザー未承認{{end}}
{{end}}{{end}}{{end}}
# 関連する会話
{{range .Chats}}
## {{.ID}}
{{if .State}}状態（出典が記録した事実）：{{if eq .State "merged"}}マージ済み{{else if eq .State "closed"}}クローズ済み{{else}}進行中{{end}}{{with .StateAt}}（{{.}}）{{end}}
{{end}}{{.Summary}}
{{end}}{{with .Moments}}
# 関連する社内投稿
{{range .}}
- {{.At}} · {{.Author}} · コメント {{.Comments}}{{with .Note}} · {{.}}{{end}}
  {{.Text}}
{{end}}{{end}}{{else}}项目：{{.Name}}（{{.Kind}}）
别称：{{join .Aliases "、"}}
已有标签：{{join .Tags "、"}}

# 已有档案
{{if blank .Summary}}（还没有）{{else}}{{.Summary}}{{end}}
{{with .Pinned}}
# 已确认的事实（只读，以此为准，不得改写）
{{.}}
{{end}}
# 最近的时间线
{{join .Log "\n"}}
{{with .Memory}}
# 方向记忆（新的在前；来源记录）
{{if .Outcomes}}## 结果
{{range .Outcomes}}- [{{.ID}}] {{.Text}}{{range .Links}} {{.}}{{end}}{{if .When}} · {{.When}}{{end}} · {{.Verification}}{{if .Unaccepted}} · 主管未验收{{end}}
{{end}}{{end}}{{if .Decisions}}## 仍有效的决定和原因（不同 ID 分别保留）
{{range .Decisions}}- [{{.ID}}] {{.Question}} → {{.Choice}}；原因：{{if .Reason}}{{.Reason}}{{else}}无记录{{end}}；结果：{{if .Result}}{{.Result}}{{else}}无记录{{end}}；来源时间：{{if .When}}{{.When}}{{else}}无记录{{end}} · {{.Verification}}{{if .Unaccepted}} · 主管未验收{{end}}
{{end}}{{end}}{{if .Pitfalls}}## 踩过的坑
{{range .Pitfalls}}- {{.Text}}{{if .When}} · {{.When}}{{end}} · {{.Verification}}{{if .Unaccepted}} · 主管未验收{{end}}
{{end}}{{end}}{{end}}
# 相关会话
{{range .Chats}}
## {{.ID}}
{{if .State}}状态（来源记录的事实）：{{if eq .State "merged"}}已合并{{else if eq .State "closed"}}已关闭{{else}}进行中{{end}}{{with .StateAt}}（{{.}}）{{end}}
{{end}}{{.Summary}}
{{end}}{{with .Moments}}
# 相关公司圈
{{range .}}
- {{.At}} · {{.Author}} · 评论 {{.Comments}}{{with .Note}} · {{.}}{{end}}
  {{.Text}}
{{end}}{{end}}{{end}}
