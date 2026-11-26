Read a live card of this conversation: id, version, View, times and actions (the user's reply and values, delivery pending/sent/failed; kind html is a persona.reply, its text what the user confirmed; version the one they saw). Read it before updating after resuming. An update never clears a committed answer.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {"type": "string", "description": "The exact live card id returned by view_show."}
  }
}
```
