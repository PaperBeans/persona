# Task workspace browser fixture (#3659)

These captures use synthetic repository/machine data, the real Go plan/settings
handlers and dispatch code, and a fake machine engine. They are not a production
deployment or proof of a real herdr/Orca checkout on this computer.

## Screenshots

The plan lives in the conversation's **Needs you** tray; **Edit** opens the
existing plan editor. The preference is in **Settings → Persona → Starting tasks →
When dispatching tasks, prefer** (the Settings Assistant group).

| Screen | Light / standard | Dark / standard | Light / large | Dark / large |
| --- | --- | --- | --- | --- |
| Plan card, 390 px | [Light](plan-light.png) | [Dark](plan-dark.png) | [Large](plan-light-large.png) | [Large](plan-dark-large.png) |
| Settings, 390 px | [Light](settings-light.png) | [Dark](settings-dark.png) | [Large](settings-light-large.png) | [Large](settings-dark-large.png) |

The web's largest supported text setting is Large. At 390 px with touch emulation,
the new controls are 44 px high and the page has no horizontal overflow.
Desktop editor captures: [new branch and engine path](edit-branch-light.png),
[existing catalog selection and resolved path](edit-existing-dark-large.png).

## Reproduce

Start the repository mock on port 18772:

```sh
MOCK_TASKS=1 PORT=18772 python3 scripts/mock/mock_hub.py
```

Start the opt-in real-handler fixture (normal Go tests skip this fixture):

```sh
TASK_WORKSPACE_QA=1 GOTOOLCHAIN=local go -C server test ./internal/assistant \
  -run '^TestTaskWorkspaceBrowserHarness$' -count=1 -timeout 35m -v
```

Start the web app against it:

```sh
PERSONA_HUB_URL=http://127.0.0.1:18773 npm --prefix web run dev -- \
  --host 127.0.0.1 --port 5184 --strictPort
```

Pair with the mock token `mock`, then open
`/persona/b2e4d6f8-0000-4000-8000-000000000001`. Open Needs you, then Edit.
Set the first task's branch to `qa/workspace`; select the review checkout from
the second task's existing-workspace catalog; keep the third at Not needed.
Start, then inspect:

```sh
curl -s http://127.0.0.1:18773/_qa/dispatch
curl -s http://127.0.0.1:18773/api/v1/assistant/conversations/b2e4d6f8-0000-4000-8000-000000000001/feed
```

Observed dispatch (order varies):

```json
{
  "new": {"cwd":"/wt/app/qa-workspace","workspace_id":"ws-new"},
  "existing": {"cwd":"/code/other","workspace_id":"ws-one"},
  "none": {"cwd":"/code/app","permission_mode":"plan"},
  "worktree_create_calls": 1,
  "created_branch": "qa/workspace"
}
```

In Settings, select New workspaces and reload. The selection remains and
`GET /api/v1/assistant` returns
`{"manager":"assistant","task_workspace_preference":"prefer-new"}`.
`TestTaskWorkspaceSettingsHTTPAndApproval` also verifies that this saved preference
actually changes omitted-workspace inference, and that a tool change waits for
approval. Stop the fixture with `POST /_qa/stop`.
