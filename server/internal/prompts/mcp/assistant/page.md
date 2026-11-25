Dashboards: pages the owner keeps in the sidebar and comes back to (a streak, a reading list). Only that becomes a page; a one-off answer or this conversation's progress stays a card. create drafts one with a preview card here; it shows only once the owner taps Keep there, so end the turn after create. update replaces the whole View, as view_update (a version conflict: get first). A tap on the page comes back here as the owner's message with persona-action view -pages/pg_…: answer by updating the page. Link to to-dos and Needs you; never copy their numbers. After the owner paused the page or restored a version, ask before changing it. get gives the page and its last taps; list the pages you write (the main conversation also reads the others'); a kept title gives error exists and its id.{{template "shared/mcp_language" .}}

```json
{
  "type": "object", "required": ["action"], "additionalProperties": false,
  "properties": {
    "action": {"type": "string", "enum": ["create", "update", "get", "list", "remove"]},
    "id": {"type": "string", "description": "update/get/remove: the pg_ id"},
    "title": {"type": "string", "maxLength": 40, "description": "create: the sidebar name in the owner's words; not a built-in place"},
    "icon": {"type": "string", "description": "create: e.g. chart, list, calendar, heart"},
    "why": {"type": "string", "maxLength": 200, "description": "create: one sentence the owner sees: why this page"},
    "view": {"type": "object", "description": "create/update: the whole page as a View, as for view_show; reply, open (https), push.page, pop; no html or image"},
    "version": {"type": "integer", "description": "update: the version you read"},
    "note": {"type": "string", "maxLength": 120, "description": "update: one line of history"},
    "reason": {"type": "string", "maxLength": 200, "description": "remove: why, for the owner"}
  }
}
```
