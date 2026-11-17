List Persona's own conversations (the 对话 sidebar), newest activity first: id, title, tags, last activity, whether it is the main conversation, whether it is running now, and the gist of its last message. Archived ones are left out unless `include_archived`. This conversation is marked. Use it to find a conversation the user means ("the one about the trip") before reading, renaming, archiving, tagging or messaging it.

```json
{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "only conversations whose title or last message contains this (case-insensitive)"
    },
    "tag": {
      "type": "string",
      "description": "only conversations carrying this exact tag"
    },
    "include_archived": {
      "type": "boolean",
      "description": "also list archived conversations (marked as such)"
    },
    "limit": {
      "type": "integer",
      "description": "how many (default 30, max 100)"
    }
  }
}
```
