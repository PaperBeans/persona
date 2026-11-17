Set the tags of a Persona conversation, replacing all of its current tags (an empty list clears them). Tags are short user-defined labels shared with the apps, used to group conversations and list one group; the same tag on several conversations makes them a group. Trimmed and de-duplicated automatically; each tag is at most 32 characters and a conversation carries at most 10. Only tag conversations in your own space, and only when the user asks for a tag or to group conversations — do not invent tags on your own. This conversation itself is a valid id.

```json
{
  "type": "object",
  "required": ["id", "tags"],
  "properties": {
    "id": {
      "type": "string",
      "description": "full id or its first 8 characters"
    },
    "tags": {
      "type": "array",
      "items": { "type": "string" },
      "description": "the complete tag list to store; [] clears every tag"
    }
  }
}
```
