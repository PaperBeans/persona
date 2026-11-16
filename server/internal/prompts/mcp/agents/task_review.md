Review a task you handed out after checking its work. Accepted closes it (its Agent is stopped once it stays still; its terminal stays); rework sends gaps to the same Agent (at most 2 rounds; a third is refused unless it carries a complete owner decision). A stopped Agent is read-only: use agent_start for a replacement. A task only waiting for something is not a decision; park it with source-backed evidence. A needs_user verdict is only for an answerable owner-only decision and requires why_owner, reason, recommend, 2–4 options (recommended first, each with leads_to), and cost_of_waiting. Otherwise decide within your authority, send the Agent on, rework, or close it.

For accepted with outcome done (the default) and nonempty current done_when, checks must contain each current criterion exactly once with met=true and nonblank evidence. Missing, duplicate, unknown or unmet checks, or blank evidence, are refused before any review or completion is written; user_check does not relax this. outcome cannot, cancelled and tasks without done_when keep their existing behavior. Other verdicts may omit checks; omitted progress stays unchanged. The completion report lists each criterion as met, not met or dropped (with its scope change), then also_done. Judge against the brief after logged scope changes, not a later handover; use task_review for every task a 督办 message says waits for review and do not accept on the Agent's word alone.

On supervised tasks, accepted and needs_user send one conclusion card in Lark with verdict, one-sentence reason and one link; needs_user also includes recommend, which the owner's “OK” selects. Give outcome, reason and link for accepted, and reason and recommend for needs_user. Do not duplicate the card. A deployed task's existing “Please verify” card is passed by acceptance; set user_check only when the owner must still inspect the result themselves. A needs_user decision defaults to recommend after 24 hours or its deadline unless why_owner is irreversible or visible_to_others; those never default and are reminded daily. A decision waiting 24 hours is rechecked once. Use can_wait only when nobody is blocked today, waiting costs little and no deadline is within 12 hours; the schema describes when the briefing hold is honored. Owner replies “You decide”, “This isn't what I asked for” and “Tell me the background first” return to supervision; do not ask the same question again or take an irreversible/externally visible step for them.

From a conversation other than main, on a task it does not supervise, only accepted and cancelled apply: send gaps with agent_send. A leader's needs_user also reaches main. Cancel only a duplicate or unwanted task; cancellation closes and archives its Agents and ends their waiting cards.

```json
{
  "type": "object",
  "required": ["todo_id", "verdict"],
  "properties": {
    "todo_id": {
      "type": "string",
      "description": "the task's todo id (todo_…), from the 督办 message or the context snapshot"
    },
    "verdict": {
      "type": "string",
      "enum": ["accepted", "rework", "needs_user", "parked", "cancelled"]
    },
    "checks": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["criterion", "met"],
        "properties": {
          "criterion": {"type": "string", "description": "one done_when criterion (or what the task was for, when it had none)"},
          "met": {"type": "boolean"},
          "evidence": {"type": "string", "description": "what shows it is met (a link, a command's output), or what is missing"}
        }
      },
      "description": "each criterion, met or not, with its evidence or gap; updates that done_when entry's persisted state, evidence, time and supervisor identity. Omitted entries keep their progress"
    },
    "note": {
      "type": "string",
      "description": "rework: what the agent is to do (Chinese, concrete); needs_user: what the user is to decide; parked: anything more than reason, for your own record; cancelled: why; accepted: the result in a line"
    },
    "outcome": {
      "type": "string",
      "enum": ["done", "cannot"],
      "description": "accepted: done (the work is done or the answer is yes), or cannot (the task found it cannot be done); default done"
    },
    "reason": {
      "type": "string",
      "description": "accepted: the conclusion card's one sentence of why, in the user's language; default the note's first line. needs_user (required): one sentence saying what is to be decided, in the user's language. parked (required): one sentence saying what the task waits for, in the user's language; waiting on a PR: include its link"
    },
    "waiting_on": {
      "type": "string",
      "enum": ["someone_else", "external_event", "date"],
      "description": "parked (required): the external wait. someone_else: another person must act or answer; external_event: an outside event must happen; date: wait only until a date. New owner decisions use needs_user; legacy owner_priority parks remain readable and retain their digest behavior"
    },
    "until": {
      "type": "string",
      "description": "parked, optional: duration (\"3d\", \"36h\", \"90m\") or RFC 3339 time, 1 minute to 30 days ahead; default 24h. For waiting_on date, give an explicit RFC 3339 time (no duration or default). At that time the task returns to supervision"
    },
    "link": {
      "type": "string",
      "description": "accepted and needs_user: the one link to the evidence (the PR, the page, the document); default the task's PR, a link in the note or checks, or the agent's conversation"
    },
    "recommend": {
      "type": "string",
      "description": "needs_user (required): the option you recommend, the label of the first of options; the user's \"OK\" takes it and it goes to the task's agent"
    },
    "why_owner": {
      "type": "string",
      "enum": ["irreversible", "visible_to_others", "priority", "acceptance", "only_owner_knows"],
      "description": "needs_user (required): why only the user can decide. irreversible: a step that cannot be undone (deleting, a production release or write, spending money); visible_to_others: something other people will see (a message, a comment, a review request); priority: whether to do it or what comes first; acceptance: a result they have to judge by eye; only_owner_knows: a fact only they have. Anything else is yours to decide"
    },
    "options": {
      "type": "array",
      "minItems": 2,
      "maxItems": 4,
      "items": {
        "type": "object",
        "required": ["label", "leads_to"],
        "properties": {
          "label": {"type": "string", "description": "the option in a few words, as a button says it"},
          "leads_to": {"type": "string", "description": "one sentence: what choosing it leads to"}
        }
      },
      "description": "needs_user (required): 2 to 4 options the user chooses from, the recommended one first"
    },
    "cost_of_waiting": {
      "type": "string",
      "description": "needs_user (required): one sentence saying what it costs if the user does not answer soon"
    },
    "can_wait": {
      "type": "boolean",
      "description": "needs_user, optional: true when the decision can wait for the next daily briefing (no deadline within 12 hours, a low cost_of_waiting): it is not pushed and has no card, and is in the user's Needs you at once. Ignored (the answer says why) for why_owner irreversible and visible_to_others, a deadline within 12 hours or with reversible false, and when no daily briefing with facts is due within 24 hours and at least 12 hours before the deadline (24 hours by default: give a longer deadline for what can wait longer); default false (pushed as usual)"
    },
    "deadline": {
      "type": "string",
      "description": "needs_user, optional: until when the decision waits, a duration (\"4h\", \"90m\") or an RFC 3339 time, 1 minute to 30 days ahead; needs reversible. Default: 24h and reversible, except for why_owner irreversible and visible_to_others, which have none"
    },
    "also_done": {
      "type": "array",
      "maxItems": 6,
      "items": {"type": "string"},
      "description": "accepted, optional: what was done beyond the task's brief (one line each); the completion report lists it after the criteria"
    },
    "user_check": {
      "type": "boolean",
      "description": "accepted only: the user is still to look at the result themselves (a UI change someone has to see): a \"Please verify\" card waits for their \"Pass\" instead of your acceptance passing it; default false"
    },
    "reversible": {
      "type": "boolean",
      "description": "with deadline: whether taking recommend can be undone later. false for deleting, sending anything to other people, spending money or changing production: those are never taken by default (why_owner irreversible and visible_to_others must say false)"
    },
    "evidence": {
      "type": "object",
      "required": ["kind", "target", "wake"],
      "properties": {
        "kind": {
          "type": "string",
          "enum": ["person_reply", "merge_review", "external_event", "date"],
          "description": "parked: person_reply (a sent request to a person), merge_review (a sent request for review of a ready PR/MR), external_event (an observable outside event), or date"
        },
        "target": {"type": "string", "maxLength": 200, "description": "the named person/reviewer or external object"},
        "wake": {"type": "string", "maxLength": 600, "description": "the observable change that ends this wait"},
        "url": {"type": "string", "maxLength": 2000, "description": "required for merge_review (ready PR/MR link) and external_event (observable object URL); omit for person_reply and date"},
        "contact": {
          "type": "object",
          "required": ["channel", "url", "sender", "sent_at", "request", "status"],
          "properties": {
            "channel": {"type": "string", "maxLength": 100},
            "url": {"type": "string", "maxLength": 2000, "description": "sent-message/receipt link, not a channel homepage"},
            "sender": {"type": "string", "maxLength": 200},
            "sent_at": {"type": "string", "format": "date-time", "description": "actual sent time; not in the future"},
            "request": {"type": "string", "maxLength": 600},
            "status": {"type": "string", "enum": ["sent"]}
          },
          "description": "required for person_reply and merge_review: the request has already been sent"
        },
        "ready": {"type": "boolean", "enum": [true], "description": "merge_review only: the named reviewer can review the PR/MR"}
      },
      "description": "required for parked; do not supply recorded_by. Inspect the source receipt; the tool records your attestation, not an independent provider check. recorded_by is server-authored for the current task_review conversation and is output only. Missing evidence on a legacy park means unrecorded, not verified"
    }
  }
}
```
