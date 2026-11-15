Hand the user a plan of one or more tasks for agents on their computers, instead of starting agents yourself. Use it for any work that takes an agent (code, research in a repo, anything longer than a few tool calls); one task per independent piece of work. Check machines_list for current machine resources and availability before choosing a machine. Fill in every field from what you know (the knowledge base, agents_list: which machine works in which directory, the user's habits); the user sees each choice as a chip they can change, with your one-line reason. Read-only tasks, tasks the user asked for themselves (user_requested), and kinds the user told you to start without asking, start at once; the rest wait as a card for the user's 开始. Either way this returns at once: don't wait, carry on. Each queued task is a todo (todo id) whose progress shows in your context snapshot; send to a task's agent with agent_send. Quick things you can do with a tool yourself (a reminder, an event) are not tasks. A task stays on its requested machine by default (machine_policy=fixed), including retries. An unavailable launcher fails there; do not reroute machine-local work. Use machine_policy=flexible only when the task may run on another computer: a failed health check can then move it, and its why says so. The response confirms queued dispatch, not a successful Agent start; inspect the task outcome. From a leader conversation (assistant.leaders) or any other side conversation you supervise what you hand out; you get a 督办 message when one needs you, and you close it with task_review, not by repeating its agent's words. What the main conversation hands out to no leader is followed up by the background supervisor by default (a built-in conversation: it gets the 督办 messages and reviews the tasks with task_review; the main conversation may still review them), and by the main conversation itself, in the same way as a leader, when assistant.main_supervises_own_tasks is on. A conversation that cannot be sent a 督办 message leaves what it hands out to that same follow-up (the background supervisor, or the main conversation), and gets a one-line notice with the task's link when each one ends. The answer says who follows the tasks up. The answer lists work already going on that looks like the same task: check it before you let both go on. A todo this conversation opened that has no agent yet (one opened from a Lark topic: the message that handed it to you names its id) is the task's own: give that id as the task's todo_id and the agent works on it, instead of a new todo being made; any other todo_id is refused with why. From the main conversation, supervisor hands the whole plan to a leader conversation (one of assistant.leaders): its tasks are that leader's to supervise and review, as if it had handed them out; leave it out for work of no direction, which is followed up as said above. Every task gets a brief, kept from the moment it starts and never rewritten: the hub records the owner's message that led to it (their words, from this conversation; work you start on your own after a 督办 message, a notice or a routine is recorded as yours, with started_why), and you supply the goal, done_when, expect_minutes and out_of_bounds. A writable task (readonly false) without done_when (at least one result that can be checked) and expect_minutes is refused, and so is a read-only task without goal and expect_minutes; the refusal says what is missing. The card shows the brief, so the owner's 开始 confirms it. A task that continues earlier work (a retry, rework, or replacement Agent) gives continues (that task's todo id) and continues_why. It always stays on the SAME original todo and keeps its original brief, criteria, and clock; write a task-specific prompt without restating or replacing the brief. Do not split continuing work into a new todo. At Start, stop and release any still-active prior Agents on that todo, then reopen the todo if it is closed, before starting the replacement. If a prior Agent may still be running but cannot be reached or stopped, refuse the takeover; never create a successor todo to work around that safeguard. Also refuse unauthorized takeovers and any legacy open successor duplicate; if an open successor already exists, do not start a second continuation. A failed start keeps the original todo and brief; never create another todo on failure. After Start, changes to scope or done_when use task_scope. A task that changes files sets readonly false and uses explicitly authorized writable permission; readonly false with writable permission (for example, Codex bypassPermissions) permits a replacement Agent on the same todo to implement fixes. A Codex plan/read-only sandbox needs a fresh writable process; do not mutate a running read-only Codex sandbox. When no permission change is wanted, a plain follow-up to the same live Agent through agent_send is fine. A task that only reads sets readonly true and its first message automatically includes the read-only constraint. Claude-family launchers use bypassPermissions for both read-only and writable tasks; Codex uses plan unless assistant.readonly_mode=prompt (normal runtime permissions, not a security boundary). pi, cursor and agy have no read-only mode, so they are refused in either setting. role "leader" makes the task's agent a task leader, which can hand direct workers to its own task through its own restricted task_plan and task_review and receives their supervision messages; use it only for a task that splits into several agents' work; any conversation may set it. Depth stops there: the leader's workers cannot hand tasks on.

When repository identity needs resolving, consult repositories_list and retrieve repository_get details only when useful. The user's explicit repository URL or path takes precedence. A saved binding is a reference, not authorization or an allowlist; never infer a machine or checkout path from it. Non-repository tasks do not need a lookup, and retrieval is not a mandatory dispatch preflight.

Workspace policy: "new" makes an isolated checkout; {"new":{"branch":"task/my-change"}} names its branch; {"existing":"<cwd or workspace_id>"} selects one; "none" creates nothing. Omitted: readonly uses none; writable Git follows assistant.task_workspace_preference (prefer-existing by default, prefer-new for isolation), and an occupied cwd uses new. Explicit existing never switches silently; refusal names its task owner. For an explicit new branch already checked out, reuse requires a clean checkout, no live Agent and no unfinished-task owner; otherwise refuse with its path and known owner. The machine's engine owns creation, location and registration; never run git worktree add. The card previews the path and is editable before Start. Default branch: task/<todo id>. Creation failure starts no Agent. Workspaces remain after acceptance. Organization read-only policy refusals include their reason: follow it, never retry unchanged.

```json
{
  "type": "object",
  "required": [
    "tasks"
  ],
  "properties": {
    "supervisor": {
      "type": "string",
      "description": "optional, main conversation only: the id of the leader conversation (assistant.leaders) whose direction this work belongs to; it supervises the tasks. Leave it out for work of no direction or of your own"
    },
    "tasks": {
      "type": "array",
      "minItems": 1,
      "maxItems": 8,
      "items": {
        "type": "object",
        "required": [
          "title",
          "prompt",
          "machine",
          "cwd",
          "readonly",
          "user_requested",
          "why"
        ],
        "properties": {
          "title": {
            "type": "string",
            "description": "the task in a short line the user reads on the card (Chinese, under 20 characters)"
          },
          "prompt": {
            "type": "string",
            "description": "the agent's first message: everything it needs to do the task without asking you"
          },
          "machine": {
            "type": "string",
            "description": "machine id; fixed target by default"
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed (default): keep this machine through preview, dispatch and retries; fail there if unavailable. flexible: explicitly permit health-based placement on another computer, only for work that is portable"
          },
          "cwd": {
            "type": "string",
            "description": "the project directory: absolute path or ~/…"
          },
          "workspace": {
            "description": "Task workspace policy. Omit to use the assistant preference and readonly/busy exceptions. new uses the machine engine and default branch task/<todo id>; existing accepts an absolute/~/ path or a workspace id on that machine; none creates nothing. To choose a branch use {new:{branch:...}}. No custom worktree root.",
            "oneOf": [
              { "type": "string", "enum": ["new", "none"] },
              { "type": "object", "required": ["existing"], "additionalProperties": false, "properties": { "existing": { "type": "string", "minLength": 1 } } },
              { "type": "object", "required": ["new"], "additionalProperties": false, "properties": { "new": { "type": "object", "additionalProperties": false, "properties": { "branch": { "type": "string" } } } } }
            ]
          },
          "launcher": {
            "type": "string",
            "description": "launcher name, one that machine has (a name it lacks is refused, listing the ones it has): a Claude Code launcher, or pi, cursor or agy to start that agent in herdr; empty: the default, or one the machine has when it lacks the default (claude first; the why says so)"
          },
          "model": {
            "type": "string",
            "description": "model id the agent takes (an alias such as opus, a gateway's own id, or a provider/model such as ag-chat/cursor/grok-4.7 for pi; letters, digits and . _ - / : @ [ ] only, or a leading ~ as in ~vendor/model-latest); empty: the launcher's default"
          },
          "permission": {
            "type": "string",
            "enum": [
              "default",
              "acceptEdits",
              "plan",
              "auto",
              "bypassPermissions"
            ],
            "description": "permission mode; empty: the default for writable tasks. Set an explicit writable mode (for example bypassPermissions) with readonly=false when a replacement Agent must implement fixes on the same todo. Read-only modes: see the description. traex and omp take none"
          },
          "role": {
            "type": "string",
            "enum": ["leader"],
            "description": "optional: leader makes this task's agent a task leader that may hand direct workers to its own task (any conversation may give it); leave it out for an ordinary worker task"
          },
          "ultracode": {
            "type": "boolean",
            "description": "true for a large task (many files, checks from several angles): Claude Code runs every substantial step as a multi-agent workflow, slower and costlier; false for small or quick work. Claude Code agents only (2.1.284 or later): any other agent, an older Claude Code or a node too old for it starts without it. Never put the word in the prompt or title instead"
          },
          "readonly": {
            "type": "boolean",
            "description": "true only for a task that must remain read-only; false for authorized file changes. A replacement on a Codex plan/read-only sandbox needs a fresh writable process: use readonly=false and explicit writable permission rather than mutating the running sandbox. The first message adds「本任务只读：不改项目文件、不提交、不对外写入」 only when true"
          },
          "user_requested": {
            "type": "boolean",
            "description": "true when the user asked for this very task in their latest message (told you to do it, to have an agent do it, to go ahead and start); false when it is your own idea or inferred from something they did not ask for"
          },
          "why": {
            "type": "string",
            "description": "one line (Chinese) on why this machine, directory and model"
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "what done means: results you will check before you accept it (Chinese, one each), e.g. 「PR 合并到 master」「浅色 / 深色截图贴进 PR」「线上 hub 已部署并验证」; the agent is told them. Required for a writable task (at least one); with continues the earlier task's criteria apply and this is ignored"
          },
          "goal": {
            "type": "string",
            "description": "the work in one sentence, as the owner would say what they get from it (Chinese); required for a read-only task, the title when left out of a writable one"
          },
          "out_of_bounds": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "optional: what the agent must not do or touch (Chinese, one each), e.g. 「不合并」「不动线上」"
          },
          "continues": {
            "type": "string",
            "description": "optional: the original todo id this task continues. Continuations always reuse this same todo and its unchanged brief and criteria; never split work to a new todo. At Start, stop/release still-active prior Agents and reopen this todo if closed. Refuse unauthorized takeovers, an unreachable prior Agent that cannot be stopped, and legacy open successor duplicates. Failed starts keep this todo. Needs continues_why; if no permission change is wanted, agent_send can follow up the same live Agent"
          },
          "continues_why": {
            "type": "string",
            "description": "with continues: one line on why the earlier task stopped or needs a replacement; kept on this continuation's plan"
          },
          "started_why": {
            "type": "string",
            "description": "when the owner did not just ask for this (your own idea, something a 督办 message or a routine led to): one line on why you start it; kept on the brief"
          },
          "attachments": {
            "type": "array",
            "maxItems": 8,
            "items": {
              "type": "string"
            },
            "description": "pictures and files the user gave you for it, by their absolute paths on your computer: a picture they sent (the path Claude Code gives it), a file the app put in their message. They are copied to the agent's computer and listed at the end of its message, and the text's mentions of them name the copies. Only those the user gave you in this conversation are accepted"
          },
          "expect_minutes": {
            "type": "integer",
            "minimum": 0,
            "description": "about how long the task should take, in minutes; required (above 0) unless continues is given. A task with no progress for 1.5× that (at least 20 minutes) counts as stuck, and one with no end after 3× that (at least a day) is raised to you as overdue"
          },
          "project": {
            "type": "string",
            "description": "optional: the knowledge-base project it belongs to: the slug or name of an existing project, usually a long-term one for an area of the user's work (kb_list projects). Set it when the area is clear; never make up a near-duplicate of an existing project"
          },
          "todo_id": {
            "type": "string",
            "description": "optional: an open todo this conversation opened or supervises, with no live Agent working on it; stopped Agents are released at Start. For a handoff that stops a live Agent or reopens a closed todo, use continues and continues_why instead. If both continues and todo_id are given they must name the same todo"
          },
          "tier": {
            "type": "string",
            "description": "An Agent preset: one of the tiers the snapshot's 模型档位 line lists for the profile in use, each with what it is for; pick the one whose description fits the work. Resolved when the task actually starts, with the profile in use then. Explicit launcher/model/effort override its values."
          },
          "effort": {
            "type": "string",
            "description": "Explicit effort override; omit to use the tier or purpose default."
          }
        }
      }
    }
  }
}
```
