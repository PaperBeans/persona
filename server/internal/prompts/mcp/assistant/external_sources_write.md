Change Inbox source routing after owner approval. create_binding takes external_ref, target_kind, target_id and optional policy. update_binding takes the same full replacement plus binding_id. delete_binding takes binding_id alone. set_policy takes external_ref and a replacement policy. Inspect first. Omitted overrides inherit, including after set_policy with {}. Use ask_owner unless the user explicitly wants automatic to-dos.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": [
        "create_binding",
        "update_binding",
        "delete_binding",
        "set_policy"
      ]
    },
    "binding_id": {
      "type": "string",
      "description": "Saved explicit binding ID from list or inspect."
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
    "policy": {
      "type": "object",
      "properties": {
        "mode": {
          "type": "string",
          "enum": [
            "realtime",
            "digest",
            "on_demand",
            "off"
          ]
        },
        "digest_interval_minutes": {
          "type": "integer",
          "minimum": 1,
          "maximum": 525600
        },
        "action_role": {
          "type": "string",
          "enum": [
            "owner",
            "fyi"
          ]
        },
        "new_item_policy": {
          "type": "string",
          "enum": [
            "ask_owner",
            "auto_todo"
          ]
        },
        "pre_filter": {
          "type": "object",
          "properties": {
            "keywords": {
              "type": "array",
              "items": {
                "type": "string"
              }
            },
            "mentions": {
              "type": "array",
              "items": {
                "type": "string"
              }
            },
            "senders": {
              "type": "array",
              "items": {
                "type": "string"
              }
            }
          },
          "additionalProperties": false
        }
      },
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
      "type": "string"
    }
  },
  "required": [
    "action"
  ],
  "additionalProperties": false
}
```
