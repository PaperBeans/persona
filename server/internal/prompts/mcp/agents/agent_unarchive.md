Bring an archived coding agent's session back into the Agents list (find it with agents_list and state archived). Nothing is reopened on the computer: a stopped agent stays read-only, and only the user can resume it, from its terminal page. Persona also asks the agent's own tool to restore it where that tool archived it too; the result says whether it did.

```json
{
  "type": "object",
  "required": ["machine", "session_id"],
  "properties": {
    "machine": {
      "type": "string",
      "description": "the machine's id or name, as agents_list shows it"
    },
    "session_id": {
      "type": "string",
      "description": "the agent's session id, or its first 8 characters"
    }
  }
}
```
