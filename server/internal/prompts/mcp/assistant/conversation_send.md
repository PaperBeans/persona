Send a message to another Persona conversation. It waits for a running turn, or 2 minutes of user quiet; the result says which. Marked as from this one; replies use conversation_send. At most a few requests per user message and a few messages per pair per ten minutes; a first reply to a request adds no hop. Do not reply just to acknowledge. Policy refusals say why: follow them, do not retry. Owner-verbatim words to main stay allowed: start with them. Handing on a user request: set quote.

```json
{
  "type": "object",
  "required": ["id", "text"],
  "properties": {
    "id": {
      "type": "string",
      "description": "full id or its first 8 characters"
    },
    "text": {
      "type": "string"
    },
    "quote": {
      "type": "string",
      "description": "\"latest\" or a message id: the user's words, carried verbatim"
    }
  }
}
```
