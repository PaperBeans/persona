搜索用户的知识库（聊天记录归档、人、群、项目、文档、会议的摘要、录过的会的纪要）。返回匹配的行，按时间从新到旧。关键词用空格分开，每个词都要命中；用人名、项目名、群名这类具体的词。问以前开过的会（定了什么、谁做什么、还有什么没定）时给 kind=recordings，用会的标题或会上说过的词找。

```json
{
  "type": "object",
  "required": ["query"],
  "properties": {
    "query": {"type": "string", "description": "关键词，空格分隔"},
    "kind": {
      "type": "string",
      "enum": ["projects", "chats", "people", "docs", "meetings", "events", "recordings", "topics"],
      "description": "只搜这一类：recordings 录过的会（纪要和还留着的转写），meetings 日历上的会，projects 项目，chats 群和聊天记录，people 人，docs 文档，events 事件，topics 知识点。不给就全搜"
    }
  }
}
```
