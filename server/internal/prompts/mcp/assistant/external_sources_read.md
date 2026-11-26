List saved Inbox source links, optionally for one target or exact source. Inspect a source to see its links, inherited rules and effective settings. Pass a binding_id with inspect to include that link's overrides.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": [
        "list",
        "inspect"
      ]
    },
    "external_ref": {
      "type": "object",
      "properties": {
        "provider": {
          "type": "string",
          "description": "Provider such as lark, github or mail"
        },
        "channel": {
          "type": "string",
          "description": "Account or instance. Omit for the default."
        },
        "container": {
          "type": "object",
          "properties": {
            "kind": {
              "type": "string"
            },
            "id": {
              "type": "string"
            }
          },
          "required": [
            "kind",
            "id"
          ],
          "additionalProperties": false
        },
        "item": {
          "type": "object",
          "properties": {
            "kind": {
              "type": "string"
            },
            "id": {
              "type": "string"
            }
          },
          "required": [
            "kind",
            "id"
          ],
          "additionalProperties": false
        }
      },
      "required": [
        "provider"
      ],
      "additionalProperties": false
    },
    "target_kind": {
      "type": "string",
      "enum": [
        "direction",
        "todo"
      ]
    },
    "target_id": {
      "type": "string",
      "description": "Use with target_kind to list one direction or to-do."
    },
    "binding_id": {
      "type": "string",
      "description": "Saved explicit binding ID from list or inspect."
    }
  },
  "required": [
    "action"
  ],
  "additionalProperties": false
}
```
