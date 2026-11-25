Health only: strength training. find exercises (name/alias, last/best); create/update (rename, aliases, muscles, equipment, weight rule, seat); log stated sets; undo a set id; history (days, optional exercise id; without id, up to 500 recent sets grouped by day); draft sets/a plan for confirmation; show a waiting draft here. Use history before retrying an uncertain log.{{template "shared/mcp_language" .}}

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
        "find",
        "create",
        "update",
        "log",
        "undo",
        "history",
        "draft",
        "show"
      ]
    },
    "q": {
      "type": "string"
    },
    "id": {
      "type": "string"
    },
    "name": {
      "type": "string",
      "maxLength": 120
    },
    "aliases": {
      "type": "array",
      "maxItems": 20,
      "items": {
        "type": "string"
      }
    },
    "primary": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "secondary": {
      "type": "array",
      "items": {
        "type": "string"
      }
    },
    "equipment": {
      "type": "string",
      "enum": [
        "machine",
        "dumbbell",
        "barbell",
        "cable",
        "bodyweight",
        "other"
      ]
    },
    "weight_rule": {
      "type": "string",
      "enum": [
        "stack",
        "per_hand",
        "with_bar",
        "added"
      ]
    },
    "setting": {
      "type": "string",
      "maxLength": 500
    },
    "sets": {
      "type": "array",
      "minItems": 1,
      "maxItems": 40,
      "items": {
        "type": "object",
        "required": [
          "exercise",
          "weight",
          "reps"
        ],
        "properties": {
          "exercise": {
            "type": "string"
          },
          "weight": {
            "type": "number",
            "minimum": 0,
            "maximum": 1000,
            "description": "kg by the exercise's weight_rule; convert pounds; at most two decimals."
          },
          "reps": {
            "type": "integer",
            "minimum": 1,
            "maximum": 1000
          },
          "warmup": {
            "type": "boolean"
          },
          "at": {
            "type": "string"
          }
        },
        "additionalProperties": false
      }
    },
    "plan": {
      "type": "boolean"
    },
    "days": {
      "type": "integer",
      "minimum": 1,
      "maximum": 366
    }
  },
  "additionalProperties": false
}
```
