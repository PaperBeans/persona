Health only: log water ml/protein g; list records/IDs, daily totals/goals (default 7d incl. today); delete UUID, repeatable; add_item after consent.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": [
    "action"
  ],
  "properties": {
    "action": {
      "type": "string",
      "enum": [
        "log",
        "list",
        "delete",
        "add_item"
      ]
    },
    "item": {"type":"string","enum":["water","protein","activity","trends","strength"]},
    "kind": {
      "type": "string",
      "enum": [
        "water",
        "protein"
      ]
    },
    "amount": {
      "type": "number",
      "description": "ml 1-3000; g 0.1-200; one decimal."
    },
    "at": {
      "type": "string",
      "description": "RFC3339; default now."
    },
    "note": {
      "type": "string",
      "description": "Food/portion; 4096 UTF-8 bytes; no NUL."
    },
    "days": {
      "type": "integer",
      "minimum": 1,
      "maximum": 366
    },
    "id": {
      "type": "string"
    }
  },
  "additionalProperties": false
}
```
