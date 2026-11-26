---
name: persona-work
description: Use for to-dos, delegation and review, Agent status, machine availability, managing Persona conversations, and repository bindings or issue/PR links.
---

# Managing work in Persona

Choose the action from what the person wants to happen now. Read the matching reference below for the exact Persona tool name. Call the tool if available. If the runtime defers it, find it by its exact name in the runtime's tool search, then call it.

- **Record.** “Add preparing the checklist to my to-dos” asks for one saved item. Record it and stop. See [To-dos and reminders](references/record.md).
- **Delegate.** “Have someone prepare that checklist and bring it back for review” asks for execution. Find the existing item and use `task_plan` with its link. Starting an Agent directly creates independent work. Report the linked plan’s actual state or the reason Persona refused it. See [Delegating work](references/delegate.md).
- **Supervise.** For “How is the checklist coming along?” read the current task and its evidence. Contact the Agent only for new work or a change of direction. See [Progress and follow-up](references/progress.md).
- **Review.** “Is my fix ready for review?” asks for its recorded state. When asked to review the result, compare it with the agreed criteria. The current conversation’s role and the owner’s request determine who can accept it. See [Reviewing results](references/review.md).

- **Agents and machines.** “Which computer can run this?” asks for availability. “Move my Agent to another launcher” asks for a change to a specific session. See [Agents and computers](references/agents-and-machines.md).
- **Organize conversations.** For “Tag the release chat,” identify the Persona conversation and preserve its other tags. See [Conversations](references/conversations.md).
- **Manage repository associations.** For “Replace the repository on this to-do” or “Attach this PR,” edit the requested association. See [Repositories and links](references/repositories.md).

Tool contracts define inputs and permissions. A plan awaiting Start, a queued dispatch, a running Agent and a result awaiting review are different states. Use the latest recorded state in your reply.
