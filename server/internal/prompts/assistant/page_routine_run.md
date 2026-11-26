{{if or (eq .Language "ja") (eq .Language "Japanese") (eq .Language "日本語")}}ダッシュボード「{{.Title}}」（ID: `{{.PageID}}`）の定期更新です。
{{if .Display}}- 確認項目：{{.Question}}
- 表示形式：{{.Display}}{{if .Unit}}（単位：{{.Unit}}）{{end}}{{if .Columns}}（列：{{join .Columns "、"}}）{{end}}
- `page` ツールの `action: "patch"`（`id: "{{.PageID}}"`）で `reading` を記録してください：{{if or (eq .Display "number") (eq .Display "trend")}}`{"value": <数値>}`{{else if eq .Display "status"}}`{"status": "ok"|"warning"|"problem", "text": "<任意の短い補足>"}`{{else if eq .Display "table"}}`{"rows": [[<セル>, ...], ...]}`（0〜20行、各行は {{len .Columns}} 列）{{end}}。{{if .HasView}}既存コンポーネントの更新が必要な場合は `items` も渡せます。{{end}}任意の `alert_text`（120文字以内）と `note` も指定できます。
- 現在の状態を見るには `page` の `action: "get"`（`id: "{{.PageID}}"`）を使います。通知は Persona がルールに従って判断するため、プッシュ通知や受信トレイへの投稿は行わないでください。{{else}}- 現在の状態を見るには `page` の `action: "get"`（`id: "{{.PageID}}"`）、既存コンポーネントのデータ更新には `action: "patch"`（`id: "{{.PageID}}"`、`items`）を使います。
- プッシュ通知や受信トレイへの投稿は行わないでください。{{end}}{{else if or (eq .Language "") (eq .Language "zh-Hans") (eq .Language "Chinese") (eq .Language "中文")}}这是仪表盘「{{.Title}}」（ID：`{{.PageID}}`）的定时刷新。
{{if .Display}}- 检查问题：{{.Question}}
- 展示形式：{{.Display}}{{if .Unit}}（单位：{{.Unit}}）{{end}}{{if .Columns}}（列：{{join .Columns "、"}}）{{end}}
- 请用 `page` 工具的 `action: "patch"`（`id: "{{.PageID}}"`）写入 `reading`：{{if or (eq .Display "number") (eq .Display "trend")}}`{"value": <数字>}`{{else if eq .Display "status"}}`{"status": "ok"|"warning"|"problem", "text": "<可选简短说明>"}`{{else if eq .Display "table"}}`{"rows": [[<单元格>, ...], ...]}`（0–20 行，每行 {{len .Columns}} 列）{{end}}。{{if .HasView}}如需更新现有组件数据，可同时传 `items`。{{end}}可选传 `alert_text`（最多 120 字，触发提醒时附带）和 `note`。
- 如需查看当前页面，调用 `page` 的 `action: "get"`（`id: "{{.PageID}}"`）。提醒由 Persona 按规则决定，不要推送通知或发进收件箱。{{else}}- 查看当前页面请用 `page` 的 `action: "get"`（`id: "{{.PageID}}"`），更新已有组件数据请用 `action: "patch"`（`id: "{{.PageID}}"`，`items`）。
- 不要推送通知或发进收件箱。{{end}}{{else}}This is a scheduled refresh for dashboard "{{.Title}}" (id: `{{.PageID}}`).
{{if .Display}}- Question: {{.Question}}
- Display: {{.Display}}{{if .Unit}} (unit: {{.Unit}}){{end}}{{if .Columns}} (columns: {{join .Columns ", "}}){{end}}
- Call `page` with `action: "patch"`, `id: "{{.PageID}}"`, and `reading`: {{if or (eq .Display "number") (eq .Display "trend")}}`{"value": <number>}`{{else if eq .Display "status"}}`{"status": "ok"|"warning"|"problem", "text": "<optional short note>"}`{{else if eq .Display "table"}}`{"rows": [[<cell>, ...], ...]}` (0-20 rows, {{len .Columns}} cells per row){{end}}.{{if .HasView}} You may also pass `items` to replace existing components by key.{{end}} Optional: `alert_text` (up to 120 chars, carried if Persona's alert rule trips) and `note`.
- Use `page` with `action: "get"` (`id: "{{.PageID}}"`) to inspect the current page. Persona evaluates alerts itself; do not push notifications or post to the Inbox.{{else}}- Use `page` with `action: "get"` (`id: "{{.PageID}}"`) to inspect the page and `action: "patch"` (`id: "{{.PageID}}"`, `items`) to update existing components by key.
- Do not push notifications or post to the Inbox.{{end}}{{end}}
