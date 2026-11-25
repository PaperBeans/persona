Read the recent conversation of another Persona session in A2A scope: the user's messages, its replies and one line per tool call, oldest first. Use the exact machine_id and session_id from a2a_sessions, or from a session the user quoted (its address persona://session/<machine_id>/<session_id>). turns is how many recent turns to read (default 2, at most 10). Reading does not notify the session; to ask it something or hand it work, use a2a_send.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": [
    "machine_id",
    "session_id"
  ],
  "properties": {
    "machine_id": {
      "type": "string"
    },
    "session_id": {
      "type": "string"
    },
    "turns": {
      "type": "integer",
      "minimum": 1,
      "maximum": 10
    }
  }
}
```
