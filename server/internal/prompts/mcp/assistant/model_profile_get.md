Inspect a profile. Edit bindings with persona_settings model_profiles.bindings, tiers with model_profiles.tiers, or capture with model_profile_capture. Saving does not apply purposes/launchers.{{template "shared/mcp_language" .}}

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
