Withdraw a pending ask_user question card created by this conversation when it is no longer needed. Only cancels this conversation's own ask_user question; another conversation's card and user-owned approvals, plans or screen dialogs are rejected. Repeated calls on an already-ended card of this conversation are idempotent.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["interaction_id"],
  "properties": {
    "interaction_id": {
      "type": "string",
      "description": "the pending ask_user interaction ID (ix_…)"
    },
    "reason": {
      "type": "string",
      "description": "optional short reason why the question is no longer needed"
    }
  }
}
```
