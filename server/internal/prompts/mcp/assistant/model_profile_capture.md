Capture current model fields into a profile with phone approval; does not apply them.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "id": {
      "type": "string",
      "description": "Existing profile id from model_profiles_list"
    }
  },
  "additionalProperties": false,
  "required": ["id"]
}
```
