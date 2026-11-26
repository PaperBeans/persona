Switch atomically to a profile by id; inspect first. Saves a rollback point and affects new model calls/Agent starts, not running work.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string",
      "description": "Profile id from model_profiles_list"
    }
  },
  "additionalProperties": false,
  "required": [
    "id"
  ]
}
```
