Record the owner's answer to a task's waiting decision (a needs_user decision on its to-do) when they gave it to you in this conversation, in their own words, instead of on the decision's card or row. Only from the main conversation or the task's supervisor, and only what the owner said: never your own call (a decision that is yours is no decision: send the agent on, or review the task). option is the label of one of the decision's options, the one their words choose; when their words fit none of them, don't record it: ask them, or restate the decision with task_review. With told_agent false the hub takes the option as the card does: it goes to the task's agent and is recorded (refused, and nothing recorded, while the agent cannot be reached). With told_agent true you already passed their answer to the agent with agent_send, and it is only recorded. Either way the decision ends as if they had answered it on its card: it leaves their Needs you, is not raised again, the to-do lists it among its decisions with their words, and the task is supervised as before. So when the owner answers a task's open decision in the chat, record it here; relaying it with agent_send alone leaves the decision waiting.

```json
{
  "type": "object",
  "required": ["todo_id", "option", "said"],
  "properties": {
    "todo_id": {
      "type": "string",
      "description": "the task's todo id (todo_…)"
    },
    "option": {
      "type": "string",
      "description": "the label of the option the owner chose, exactly as the decision lists it"
    },
    "said": {
      "type": "string",
      "description": "what the owner said, quoted (one or two sentences)"
    },
    "told_agent": {
      "type": "boolean",
      "description": "true when you already sent their answer to the task's agent (agent_send): it is only recorded; default false: the hub sends the option to the agent as the card does"
    }
  }
}
```
