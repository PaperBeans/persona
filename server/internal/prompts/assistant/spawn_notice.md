🔔 你用 conversation_new 开的对话「{{.Title}}」（{{.ID}}）{{if .Failed}}出错了，这一轮没做成{{else}}这一轮结束了，但一直没用 conversation_send 回报{{end}}。{{if .Link}}
链接：{{.Link}}{{end}}{{if .Error}}
它的错误：{{.Error}}{{end}}{{if .Last}}
它最后说的：{{.Last}}{{end}}
（这是 Persona 的通知，不是用户发的消息，也不是那段对话自己发来的；同一段对话只通知这一次。）
你来决定接下来怎么办：要看它做到哪就用 conversation_read，要它接着做或把结果发回来就用 conversation_send，也可以自己接手。错误是登录、密钥、额度这类要用户处理的，就告诉用户怎么处理。没什么要做的就不用回应。
