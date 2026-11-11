#!/usr/bin/env python3
"""Synthetic Apple acceptance hub for the task-draft contract in PR #3787.

Run with python3 -I apple/verify/draft_dismiss_mock.py --port <unused port>.
It extends the repository mock without changing server/web/Android code.
"""
import argparse
import copy
import importlib.util
import json
import os
from pathlib import Path
import threading
from http.server import ThreadingHTTPServer
from urllib.parse import urlparse

parser = argparse.ArgumentParser()
parser.add_argument("--port", type=int, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parents[2]
os.environ.update(PORT=str(args.port), MOCK_TASKS="1", MOCK_TASKS_NO_ASKS="1", MOCK_TASKS_AT="11")
spec = importlib.util.spec_from_file_location("persona_mock", root / "scripts/mock/mock_hub.py")
mock = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mock)
lock = threading.RLock()
original = copy.deepcopy(next(i for i in mock.TASK_FEED[mock.ASK1] if i["id"] == "feed_m7b"))
original["body"].update(sender="示例联系人", chat="示例项目群", text="任务进展如何？", draft="测试已通过，稍后提交变更。")
base_todos = copy.deepcopy(mock.TODOS)
base_sessions = copy.deepcopy(mock.SESSIONS)
evidence = []
serial = 0
failure = False


def push(item):
    for send, _ in list(mock.LIVE):
        try:
            send(mock.from_default({"type": "assistant_feed", "item": copy.deepcopy(item)}))
        except (OSError, ValueError):
            pass


def reset():
    global failure
    mock.TASK_FEED[mock.ASK1] = [copy.deepcopy(original)]
    evidence.clear()
    mock.SENT.clear()
    failure = False
    push(original)


reset()


class Handler(mock.H):
    def do_GET(self):
        path = urlparse(self.path).path
        if path == "/mock/draft/evidence":
            with lock:
                return self.send_json({"operations": copy.deepcopy(evidence), "sent": copy.deepcopy(mock.SENT),
                                       "task_mutations": int(mock.TODOS != base_todos),
                                       "agent_mutations": int(mock.SESSIONS != base_sessions),
                                       "pending": [i["id"] for i in mock.TASK_FEED[mock.ASK1]
                                                   if not i["body"].get("dismissed_at")]})
        if path.endswith("/feed") and path.startswith("/api/v1/assistant/conversations/"):
            with lock:
                return self.send_json({"items": copy.deepcopy(mock.TASK_FEED[mock.ASK1]), "plans": {}, "next_cursor": ""})
        return super().do_GET()

    def do_POST(self):
        global serial, failure
        path = urlparse(self.path).path
        if path.startswith("/mock/draft/"):
            self.body()
            with lock:
                if path.endswith("/reset"):
                    reset()
                elif path.endswith("/fail-next"):
                    failure = True
                elif path.endswith("/progress"):
                    serial += 1
                    item = copy.deepcopy(original)
                    item.update(id="feed_new_%d" % serial, at=mock.ago())
                    item["body"]["draft"] = "新进展已完成，请审阅。"
                    mock.TASK_FEED[mock.ASK1].append(item)
                    push(item)
                return self.send_json({"ok": True})
        if path.startswith("/api/v1/assistant/feed/"):
            body = json.loads(self.body() or b"{}")
            card_id, action = path.rsplit("/", 2)[1:]
            with lock:
                card = next((i for i in mock.TASK_FEED[mock.ASK1] if i["id"] == card_id), None)
                evidence.append({"action": action, "card": card_id, "body": body,
                                 "manager": self.headers.get("X-Persona-Manager", "")})
                if failure:
                    failure = False
                    return self.send_json({"error": "Synthetic temporary failure"}, 503)
                if card is None:
                    return self.send_json({"error": "No message card"}, 404)
                if action == "dismiss":
                    if body.get("draft") != card["body"]["draft"] or card["body"].get("dismissed_at"):
                        return self.send_json({"error": "Draft changed"}, 409)
                    serial += 1
                    receipt = "dismissal_%d" % serial
                    card["body"].update(dismissed_at=mock.ago(), dismissal_id=receipt)
                    push(card)
                    return self.send_json({"item": copy.deepcopy(card), "dismissal_id": receipt})
                if action == "undo-dismiss":
                    if not body.get("dismissal_id") or body["dismissal_id"] != card["body"].get("dismissal_id"):
                        return self.send_json({"error": "Decision changed"}, 409)
                    card["body"].pop("dismissed_at", None)
                    card["body"].pop("dismissal_id", None)
                    push(card)
                    return self.send_json({"item": copy.deepcopy(card)})
        return super().do_POST()


server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
server.daemon_threads = True
mock.ENDPOINTS.append(mock.endpoint("127.0.0.1", args.port, "http", "lan"))
print("VERDICT: READY synthetic draft hub port=%d" % args.port, flush=True)
server.serve_forever()
