Update supplied fields immediately, including task to-dos; no confirmation. Omitted fields stay; "" clears notes/due/remind/project/part_of/audience/direction_id, [] clears after. New remind notifies again. Close/reopen via todo_complete/todo_reopen.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": [
    "id"
  ],
  "properties": {
    "id": {
      "type": "string",
      "description": "To-do id from todo_list."
    },
    "title": {
      "type": "string",
      "description": "the new title"
    },
    "notes": {
      "type": "string",
      "description": "Replacement notes; retain anything still needed. \"\" clears."
    },
    "due": {
      "type": "string",
      "description": "YYYY-MM-DD or datetime (local/RFC3339); \"\" clears."
    },
    "remind": {
      "type": "string",
      "description": "Future datetime (local/RFC3339) or 30m/2h/1d; \"\" clears."
    },
    "priority": {
      "type": "string",
      "enum": [
        "high",
        "normal",
        "low"
      ]
    },
    "project": {
      "type": "string",
      "description": "Existing kb_list project slug/name; only when clear, no near-duplicates. \"\" clears."
    },
    "audience": {
      "type": "string",
      "description": "human, autonomous, or \"\" clears."
    },
    "direction_id": {
      "type": "string",
      "description": "Direction id, or \"\" clears."
    },
    "part_of": {
      "type": "string",
      "description": "Request to-do this is a piece of; changing clears after."
    },
    "after": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "Sibling piece ids it waits for; replaces old."
    }
  }
}
```
