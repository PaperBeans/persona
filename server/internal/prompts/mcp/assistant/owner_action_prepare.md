Prepare one exact owner-approved batch for side-effecting connector calls, managed persona_settings set/add/remove, model_profile_switch and model_profile_rollback. Use it only when the owner asked to review the batch. Put every complete call in items. The cards show full redacted arguments and each built-in before/after; they, not prose, are the approval authority. After approval, call each item once in the returned order with its canonical arguments unchanged; stop at the first error and read its partial result. Built-in calls consume one-shot grants through their ordinary tools and recheck the approved display and revision. Denial executes nothing; never retry an uncertain result or treat this as Always permission. Reads, capture, unknown tools, non-connector runtime prompts and oversized items are refused; use their ordinary path.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["items"],
  "additionalProperties": false,
  "properties": {
    "items": {
      "type": "array",
      "minItems": 1,
      "maxItems": 20,
      "items": {
        "type": "object",
        "required": ["tool", "input"],
        "additionalProperties": false,
        "properties": {
          "tool": {
            "type": "string",
            "description": "registered connector write, persona_settings, model_profile_switch or model_profile_rollback"
          },
          "input": {
            "type": "object",
            "description": "complete exact arguments, including every connector recipient/body field or built-in action, key, value and profile id"
          }
        }
      }
    }
  }
}
```
