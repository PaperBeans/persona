Preview/import Claude shell aliases. Preview is read-only/redacted. Commit needs names/owner approval and reuses current import rules. Names select groups/raw aliases; split/hide_originals control per-alias import/hiding. No overrides or secrets.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["preview", "commit"]},
    "machine": {"type": "string"},
    "names": {
      "type": "array",
      "items": {"type": "string", "minLength": 1},
      "maxItems": 128
    },
    "split": {"type": "boolean"},
    "hide_originals": {"type": "boolean"}
  },
  "additionalProperties": false,
  "required": ["action", "machine"]
}
```
