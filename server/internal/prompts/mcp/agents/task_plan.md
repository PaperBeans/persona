Use task_plan for code, repo research and multi-step work (one task per independent piece). Check machines_list, KB, agents_list and preferences for editable choices and one-line reasons. Main/lead plans start at once; elsewhere read-only, user_requested and preapproved kinds do; others and owner_step wait for 开始. Queued receipt means dispatched; check to-do outcome and use agent_send.

Explicit machine keeps fixed (default, refuses unavailable launchers) or flexible (moves portable work on failed health checks, explain why). Omit machine for automatic placement with optional placement_constraints. Planning leads own their tasks; main owns its own and receives needs_user escalations. Main may pick a lead; agent_transfer persists ownership. Incompatible leads need supervision_policy first. Check returned supervisor and similar work. Pass todo_id for caller's unassigned to-do (Lark topic, split, failed start).

Started tasks keep a hub brief (owner request or started_why). Give goal, done_when, out_of_bounds: writable needs done_when (PR/Issue defaults to merged+closed unless PR/draft-only); read-only needs goal. Omit expect_minutes for auto estimate; positive overrides. Use continues+continues_why to continue, task_retry for failed starts, task_scope+scope_revision for approved changes.

Edits need readonly=false and write permission; replace read-only Codex with writable for fixes, or use agent_send when permissions match. readonly=true adds read-only constraint. Claude-family and companion agy use bypassPermissions; Codex uses plan unless assistant.readonly_mode=prompt; pi/cursor refuse readonly. Set role=worker, or leader for direct workers via restricted task_plan/task_review.

Resolve repository via repositories_list (repository_get when useful); an explicit repository URL or path takes precedence. Saved bindings are references only; never infer machine/cwd. Non-repository tasks need no lookup (not a mandatory dispatch preflight). Closing terminal on acceptance is owner-only.

Saved workspace naming plan on existing todo: {"backend":{"operation_id":"<id>"}}. Multi-repo container: {"container":{"selected_repo_ids":[...]}} (or operation_id with todo_id). Optional host picks enabled engine; selected repos must be ready.

Workspace: "new" isolates checkout; {"new":{"branch":"task/my-change"}} names branch; {"existing":"<cwd or id>","queue":true} picks one (queue:true or owner in after queues FIFO, else busy refuses); "none" creates nothing. Omitted: readonly=none; writable Git follows assistant.task_workspace_preference, busy cwd uses new. Explicit existing never switches silently. Reusing explicit new branch needs clean checkout, no live Agent/owner. Engine owns creation; never run git worktree add. Default branch: task/<todo id>. Failed creation starts no Agent; workspaces stay after acceptance. Follow org read-only refusals.

`wait_seconds` observes up to 50 seconds after dispatch, appending `Inline observation` JSON: `completed`(this input's `reply`), `question`(pending `interaction_id`), `timeout`/`cancelled`/`unavailable`/`interrupted`/`failed`, with known input/session ids. Observation never answers questions, accepts tasks, resends or cancels work; normal supervision/`task_review` applies. Older nodes may time out rather than return an unrelated reply. Only an immediately dispatchable single-task plan waits; approval cards and multi-task plans return immediately.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": [
    "tasks"
  ],
  "properties": {
    "wait_seconds": {
      "type": "integer",
      "minimum": 0,
      "description": "0/omitted: normal receipt"
    },
    "supervisor": {
      "type": "string",
      "description": "optional leader id (assistant.leaders), selected by main; omitted: planning lead or main"
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
          "role",
          "readonly",
          "user_requested",
          "why"
        ],
        "anyOf": [
          {"required": ["cwd"]},
          {"type": "object", "required": ["workspace", "todo_id"], "properties": {"workspace": {"type": "object", "required": ["backend"]}}},
          {"type": "object", "required": ["workspace"], "properties": {"workspace": {"type": "object", "required": ["container"]}}}
        ],
        "properties": {
          "title": {
            "type": "string",
            "description": "{{if eq .Lang "en"}}Card title in English, under 20 characters.{{else if eq .Lang "ja"}}カードに表示する日本語のタイトル（20文字未満）。{{else}}Card title in Chinese, under 20 characters.{{end}}"
          },
          "context": {"type": "string", "description": "extra facts; no project background"},
          "prompt": {
            "type": "string",
            "description": "goal, bounds and facts; manager adds context, criteria and rules"
          },
          "skip_project_memory": {
            "type": "boolean",
            "description": "default false; true skips project/direction memory in prompt"
          },
          "machine": {
            "type": "string",
            "description": "Machine id; omit for automatic placement."
          },
          "machine_policy": {
            "type": "string",
            "enum": ["fixed", "flexible"],
            "description": "fixed (default) or flexible (health-based moves for portable work)"
          },
          "placement_constraints": {
            "type": "object",
            "additionalProperties": false,
            "description": "Optional auto-placement constraints when machine is omitted.",
            "properties": {
              "os": {"type": "string", "enum": ["linux", "darwin", "windows"]},
              "capabilities": {"type": "array", "maxItems": 16, "items": {"type": "string"}},
              "require_hub": {"type": "boolean", "description": "default false (excludes hub)"},
              "workload": {"type": "string", "enum": ["light", "standard", "heavy"]},
              "candidate_machines": {"type": "array", "maxItems": 32, "items": {"type": "string"}}
            }
          },
          "cwd": {
            "type": "string",
            "description": "Abs or ~/ dir, unless workspace.backend/container supplies it."
          },
          "workspace": {
            "type": ["string", "object"],
            "description": "workspace policy; omit for default; existing: path or workspace id",
            "oneOf": [
              { "type": "string", "enum": ["new", "none"] },
              { "type": "object", "required": ["backend"], "additionalProperties": false, "properties": { "backend": { "type": "object", "required": ["operation_id"], "additionalProperties": false, "properties": { "operation_id": { "type": "string", "minLength": 1 }, "host": { "type": "string" } } } } },
              { "type": "object", "required": ["container"], "additionalProperties": false, "properties": { "container": { "type": "object", "additionalProperties": false, "properties": { "selected_repo_ids": { "type": "array", "minItems": 1, "items": { "type": "string", "minLength": 1 } }, "operation_id": { "type": "string", "minLength": 1 }, "host": { "type": "string" } } } } },
              { "type": "object", "required": ["existing"], "additionalProperties": false, "properties": { "existing": { "type": "string", "minLength": 1 }, "queue": { "type": "boolean" } } },
              { "type": "object", "required": ["new"], "additionalProperties": false, "properties": { "new": { "type": "object", "additionalProperties": false, "properties": { "branch": { "type": "string" } } } } }
            ]
          },
          "launcher": {
            "type": "string",
            "description": "listed launcher; empty: default/fallback, claude first"
          },
          "host": {
            "type": "string",
            "description": "agy (companion) or enabled engine"
          },
          "model": {
            "type": "string",
            "description": "model id/alias; empty: launcher default"
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
            "description": "mode; empty: writable default; TRAE/omp take none"
          },
          "role": {
            "type": "string",
            "enum": ["worker", "leader"],
            "description": "worker or leader (starts/reviews direct workers)"
          },
          "ultracode": {
            "type": "boolean",
            "description": "large tasks only (Claude Code 2.1.284+ multi-agent); small: false; never in prompt/title"
          },
          "readonly": {
            "type": "boolean",
            "description": "true only for read-only tasks; false for authorized edits. Adds「本任务只读：不改项目文件、不提交、不对外写入」when true"
          },
          "user_requested": {
            "type": "boolean",
            "description": "True only if user's latest message asked for it."
          },
          "owner_step": {"type": "string", "enum": ["money", "outward", "irreversible"], "description": "money, outward speech, or unagreed irreversible step"},
          "why": {
            "type": "string",
            "description": "{{if eq .Lang "en"}}One-line reason in English for the machine, directory and model.{{else if eq .Lang "ja"}}コンピュータ、ディレクトリ、モデルを選ぶ理由を日本語で一行に記述します。{{else}}Chinese one-line reason for machine, directory and model.{{end}}"
          },
          "done_when": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "{{if eq .Lang "en"}}Checkable acceptance results in English, one per entry, shared with the Agent. Nonempty for writable tasks; ignored with continues.{{else if eq .Lang "ja"}}Agent に伝える確認可能な完了条件を日本語で一項目ずつ記述します。書き込み可能なタスクは一つ以上必須です。continues 指定時は無視されます。{{else}}Chinese checkable acceptance results told to Agent, one per entry. Nonempty for writable tasks; ignored with continues.{{end}}"
          },
          "goal": {
            "type": "string",
            "description": "{{if eq .Lang "en"}}Owner's one-line outcome in English. Required for read-only tasks; writable tasks default to the title.{{else if eq .Lang "ja"}}ユーザーが得る結果を日本語で一行に記述します。読み取り専用タスクでは必須です。書き込み可能なタスクで省略した場合はタイトルを使います。{{else}}Owner's one-line Chinese outcome. Required read-only; writable defaults to title.{{end}}"
          },
          "out_of_bounds": {
            "type": "array",
            "maxItems": 6,
            "items": {
              "type": "string"
            },
            "description": "{{if eq .Lang "en"}}Optional forbidden actions or areas in English, one per entry.{{else if eq .Lang "ja"}}任意の禁止事項や対象外の範囲を日本語で一項目ずつ記述します。{{else}}Optional forbidden actions/areas, in Chinese, one per entry.{{end}}"
          },
          "continues": {
            "type": "string",
            "description": "Original todo id; reuses todo/brief, stops prior Agents, reopens if closed."
          },
          "scope_revision": {
            "type": "string",
            "description": "With continues after scope change: exact task_scope scope_revision."
          },
          "continues_why": {
            "type": "string",
            "description": "With continues: one-line replacement reason."
          },
          "started_why": {
            "type": "string",
            "description": "Non-owner-requested start reason saved in brief."
          },
          "attachments": {
            "type": "array",
            "maxItems": 8,
            "items": {
              "type": "string"
            },
            "description": "Abs paths of chat files (Claude/app); copied with updated mentions."
          },
          "expect_minutes": {
            "type": "integer",
            "minimum": 0,
            "description": "Optional minutes override (0/omit: auto or 60m)."
          },
          "project": {
            "type": "string",
            "description": "Existing kb_list projects slug/name; no near-duplicates."
          },
          "audience": {
            "type": "string",
            "enum": [
              "human",
              "autonomous"
            ],
            "description": "Optional: human or autonomous."
          },
          "direction_id": {
            "type": "string",
            "description": "Direction id."
          },
          "part_of": {
            "type": "string",
            "description": "Request todo this is a piece of."
          },
          "after": {
            "type": "array",
            "items": {
              "type": "string"
            },
            "description": "With part_of: prerequisite pieces."
          },
          "todo_id": {
            "type": "string",
            "description": "Caller's open/failed-start todo with no live Agent; equals continues if both set."
          },
          "inbox_item": {
            "type": "string",
            "description": "Handed-off Inbox ID or 'none'; links without filing/replying."
          },
          "tier": {
            "type": "string",
            "description": "Agent preset from 模型档位; explicit launcher/model/effort/speed override."
          },
          "speed": {
            "type": "string",
            "enum": ["standard", "fast", "ultrafast"],
            "description": "new-start speed; standard/omitted: default; Codex: fast/ultrafast; Claude Code: fast"
          },
          "effort": {
            "type": "string",
            "description": "Explicit effort; omit for tier/purpose default."
          }
        }
      }
    }
  }
}
```
