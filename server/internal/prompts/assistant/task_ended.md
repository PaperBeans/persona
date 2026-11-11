🔔 这段对话派出去的任务「{{.Title}}」{{if eq .Verdict "accepted"}}已验收，结束了{{else}}已取消{{end}}{{if .Link}}：{{.Link}}{{else}}（{{.ID}}）{{end}}{{if .Note}}
验收说明：{{.Note}}{{end}}
（这是 Persona 的通知，不是用户发的消息。这个任务由主对话督办；这里只留一条记录，手机不会为它另外推送。）
不用回应，也不要再督办它；用户在这里问起时再说结果。
