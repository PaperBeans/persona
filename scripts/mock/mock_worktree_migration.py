#!/usr/bin/env python3
"""Run the normal synthetic hub with a stateful Orca worktree migration fixture.

PORT=18869 python3 scripts/mock/mock_worktree_migration.py
All workspaces, provider references and mutations are invented and stay in memory.
GET /mock/worktree-migration reports requests; ?mode=legacy|dirty|unknown selects faults.
"""
import base64
import copy
import json
import os
import re
import uuid
from http.server import ThreadingHTTPServer
from urllib.parse import parse_qs, unquote, urlparse

os.environ.setdefault("MOCK_MANAGERS", "1")
os.environ.setdefault("MOCK_ORCA_CHILDREN", "1")
os.environ.setdefault("MOCK_TERMINAL_PANES", "1")
os.environ.setdefault("MOCK_AGENT_WORKTREE", "1")
os.environ.setdefault("MOCK_START_SECONDS", "0.1")
import mock_hub as hub

# The normal mock Mac advertises herdr only; this fixture exercises an enabled local Orca adapter.
fixture_machine = next(machine for machine in hub.MACHINES if machine["id"] == "macbook")
fixture_machine.update(engine="orca", engine_hosts=["herdr", "orca"], engine_default="orca")
for engine in fixture_machine.get("engines", []):
    if engine["id"] == "orca":
        engine["available"] = True


ROOT = "app::/Users/me/code/app"
EMPTY = "app::/Users/me/code/app/.worktrees/empty-parent"
LEAF = "app::/Users/me/code/app/.worktrees/catalog-leaf"
OTHER = "library::/Users/me/code/library"
MODE = "normal"
REQUESTS = []
RECEIPTS = {}
INTENTS = {}
PREVIEWS = {}
EXTRA = {}
REMOVED = set()
RENAMED = {}
_catalog = hub.mock_workspaces


def catalog():
    rows = copy.deepcopy(_catalog()) + [
        {"id": "fixture-empty", "machine_id": "macbook", "source": "orca", "source_id": EMPTY,
         "parent_source_id": ROOT, "title": "Empty parent", "cwd": EMPTY.split("::", 1)[1],
         "branch": "research/empty", "index": 6, "online": True, "available": True},
        {"id": "fixture-leaf", "machine_id": "macbook", "source": "orca", "source_id": LEAF,
         "parent_source_id": EMPTY, "title": "Catalog leaf", "cwd": LEAF.split("::", 1)[1],
         "branch": "research/leaf", "index": 7, "online": True, "available": True},
        {"id": "fixture-library", "machine_id": "macbook", "source": "orca", "source_id": OTHER,
         "title": "Library main", "cwd": "/Users/me/code/library", "project_id": "example-library",
         "project_name": "Example library", "project_path": "/Users/me/code/library", "branch": "main",
         "is_main": True, "index": 8, "online": True, "available": True},
    ] + list(copy.deepcopy(EXTRA).values())
    for row in rows:
        if row["source"] == "orca":
            for key, value in {"project_id": "example-app", "project_name": "Example app", "project_path": "/Users/me/code/app",
                               "workspace_kind": "folder" if row["source_id"] == "orca:collection" else "worktree",
                               "is_main": row["source_id"] == ROOT, "head": "a" * 40}.items():
                row.setdefault(key, value)
        if row["source_id"] in RENAMED:
            row["title"] = RENAMED[row["source_id"]]
    return [row for row in rows if row["source_id"] not in REMOVED]


hub.mock_workspaces = catalog
leaf = copy.deepcopy(next(s for s in hub.SESSIONS if s.get("space_id") == ROOT))
leaf.update(session_id="e0000000-0000-4000-8000-000000000001", title="Catalog-only ancestry check",
            cwd=LEAF.split("::", 1)[1], workspace_cwd=LEAF.split("::", 1)[1], workspace_id=LEAF,
            space_id=LEAF, space_name="Catalog leaf", parent_space_id=EMPTY,
            pane_id="orca:fixture-leaf:p1", slot_id="fixture-leaf-tab", tab_id="fixture-leaf-tab",
            branch="research/leaf", repo_root="/Users/me/code/app", state="idle", status="done")
hub.SESSIONS.append(leaf)
BASE_SESSIONS = copy.deepcopy(hub.SESSIONS)


class MigrationHub(hub.H):
    def do_GET(self):
        global MODE
        parsed = urlparse(self.path)
        if parsed.path == "/mock/worktree-migration":
            query = parse_qs(parsed.query)
            if query.get("reset") == ["true"]:
                REQUESTS.clear()
                RECEIPTS.clear()
                INTENTS.clear()
                PREVIEWS.clear()
                EXTRA.clear()
                REMOVED.clear()
                RENAMED.clear()
                hub.SESSIONS[:] = copy.deepcopy(BASE_SESSIONS)
                MODE = "normal"
            if "mode" in query:
                mode = query["mode"][0]
                if mode not in {"normal", "legacy", "dirty", "unknown", "catalog-offline", "lost-create"}:
                    return self.send_json({"error": "Unknown synthetic mode"}, 400)
                MODE = mode
            return self.send_json({"fixture": "worktree-migration", "mode": MODE, "requests": REQUESTS,
                                   "workspaces": catalog(), "created": len(EXTRA), "removed": sorted(REMOVED)})
        op_match = re.fullmatch(r"/api/v1/ops/([0-9a-fA-F-]+)", parsed.path)
        if op_match:
            op = op_match[1]
            if op not in RECEIPTS:
                return self.send_json({"error": "Synthetic receipt not found"}, 404)
            # The hub persists the exact HTTP bytes, represented as Go []byte in JSON.
            payload = base64.b64encode(json.dumps(RECEIPTS[op]).encode()).decode()
            return self.send_json({"id": op, "kind": "worktree_create", "state": "done",
                                   "result": {"status": 200, "body": payload, "content_type": "application/json"}})
        if parsed.path == "/api/v1/workspaces" and MODE == "catalog-offline":
            return self.send_json({"error": "Synthetic manager unavailable", "code": "assistant_offline"}, 503)
        return super().do_GET()

    def do_POST(self):
        path = urlparse(self.path).path
        match = re.fullmatch(r"/api/v1/machines/([^/]+)/worktree/(prepare|create|source|preset|rename|delete)", path)
        if not match:
            return super().do_POST()
        mid, action = unquote(match[1]), match[2]
        body = json.loads(self.body() or b"{}")
        wid = body.get("workspace_id", "")
        REQUESTS.append({"machine": mid, "action": action, "body": body,
                         "op_id": self.headers.get("X-Persona-Op-Id", "")})
        row = next((w for w in catalog() if w["source_id"] == wid and w["machine_id"] == mid), None)
        if not row and not (action == "delete" and body.get("reconcile")):
            return self.send_json({"error": "Synthetic workspace no longer exists", "code": "workspace_missing"}, 409)
        if action == "prepare":
            result = {"root": row["project_path"], "repoName": row["project_name"], "branch": row.get("branch", "main"),
                      "path": row["project_path"] + "/.worktrees", "choose_parent": True, "automatic_name": True}
            if body.get("composer") and MODE != "legacy":
                result["composer"] = {"project": row["project_name"], "base_ref": "origin/main",
                                      "refs": ["origin/main", "main", "review/existing"],
                                      "presets": [{"id": "web-only", "name": "Web only", "directories": ["web"]}],
                                      "capabilities": ["create", "source.github", "sparse_presets", "branch_override", "folder_parent"]}
            return self.send_json(result)
        if action == "source":
            return self.send_json({"items": [{"type": "pr", "number": 12, "title": "Example review",
                                              "url": "https://github.com/example/app/pull/12",
                                              "branchName": "review/example", "baseRefName": "main"}]})
        if action == "preset":
            return self.send_json({"preset": {"id": "fixture-preset", "name": body["name"],
                                               "directories": body["directories"].splitlines()}})
        if action == "rename":
            RENAMED[wid] = body["name"]
            return self.send_json({"ok": True})
        if action == "create":
            op = self.headers.get("X-Persona-Op-Id", "")
            if op in RECEIPTS:
                if INTENTS[op] != {"machine": mid, "body": body}:
                    return self.send_json({"error": "Operation belongs to another intent", "code": "op_id_reused"}, 409)
                return self.send_json(RECEIPTS[op])
            name = body.get("name") or "generated-coral"
            branch = body.get("branch_override") or body.get("branch") or "persona/" + name
            checkout = row["project_path"] + "/.worktrees/" + re.sub(r"[^a-zA-Z0-9-]", "-", name)
            created_id = row["source_id"].split("::", 1)[0] + "::" + checkout
            parent = "" if body.get("no_parent") else body.get("parent_workspace_id") or wid
            EXTRA[created_id] = {"id": "fixture-created-" + name, "machine_id": mid, "source": "orca",
                                 "source_id": created_id, "title": name, "cwd": checkout, "branch": branch,
                                 "parent_source_id": parent, "project_id": row["project_id"],
                                 "project_name": row["project_name"], "project_path": row["project_path"],
                                 "index": 30, "online": True, "available": True}
            result = {"workspace_id": created_id, "parent_workspace_id": parent, "path": checkout, "branch": branch}
            if op:
                INTENTS[op] = {"machine": mid, "body": copy.deepcopy(body)}
                RECEIPTS[op] = result
            if MODE == "lost-create":
                return self.send_json({"error": "Synthetic response lost after creation"}, 502)
            return self.send_json(result)
        if body.get("reconcile"):
            removed = wid in REMOVED
            return self.send_json({"workspace_id": wid, "removed": removed, "state": "removed" if removed else "present"})
        if row.get("is_main"):
            return self.send_json({"error": "The main checkout cannot be removed", "code": "main_workspace"}, 409)
        if any(w.get("parent_source_id") == wid for w in catalog()):
            return self.send_json({"error": "This workspace still has children", "code": "worktree_children"}, 409)
        if body.get("preview"):
            token = str(uuid.uuid4())
            PREVIEWS[token] = wid
            return self.send_json({"workspace_id": wid, "kind": "orca", "host_id": "local", "instance_id": "fixture-" + wid,
                                   "path": row["cwd"], "branch": row.get("branch", ""), "dirty": int(MODE == "dirty"),
                                   "folder": row.get("workspace_kind") == "folder", "preview_token": token})
        if PREVIEWS.pop(body.get("preview_token", ""), None) != wid:
            return self.send_json({"error": "Review this workspace again", "code": "preview_changed"}, 409)
        if MODE == "dirty" and not body.get("force"):
            return self.send_json({"error": "This workspace has changed files", "code": "worktree_dirty"}, 409)
        if MODE == "unknown":
            return self.send_json({"workspace_id": wid, "removed": False, "state": "unknown"})
        REMOVED.add(wid)
        return self.send_json({"workspace_id": wid, "removed": True, "state": "removed",
                               "branch_deleted": body.get("delete_branch") is True})


if __name__ == "__main__":
    server = ThreadingHTTPServer(("127.0.0.1", hub.PORT), MigrationHub)
    server.daemon_threads = True
    hub.ENDPOINTS.append(hub.endpoint("127.0.0.1", server.server_port, "http", "lan"))
    print("Synthetic worktree migration hub on 127.0.0.1:%d" % server.server_port, flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
