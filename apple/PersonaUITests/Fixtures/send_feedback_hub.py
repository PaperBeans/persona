"""Isolated Apple send-feedback fixture; acceptance and runtime output are separate.

PORT=<port> python3 -I apple/PersonaUITests/Fixtures/send_feedback_hub.py
"""
import importlib.util
import json
import os
from pathlib import Path
import sys
import subprocess
import threading
import time
from urllib.parse import urlparse, parse_qs
from http.server import ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "scripts" / "mock"))
os.environ.setdefault("MOCK_AGENTS", "1")
os.environ.setdefault("MOCK_CHAT_READ", "1")
spec = importlib.util.spec_from_file_location("mock_hub", ROOT / "scripts/mock/mock_hub.py")
hub = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hub)
# A restarted fixture is a new history, not a truncated continuation of a cached run.
hub.HISTORY = "send-feedback-" + str(time.time_ns())
hub.HISTORY_NOW["token"] = hub.HISTORY

RECEIPTS = []
LOCK = threading.Lock()
AGENT = "a1f3c2d4-0000-4000-8000-000000000001"
ASSISTANT = "b2e4d6f8-0000-4000-8000-000000000001"
for session in hub.SESSIONS + hub.ASSISTANT:
    if session["session_id"] in (AGENT, ASSISTANT):
        session.update(state="idle", status="idle", starting=False)
        session.pop("failure_reason", None)


class Handler(hub.H):
    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/mock/send-feedback-shot":
            name = parse_qs(parsed.query).get("name", [""])[0]
            allowed = [f"{chat}-{look}{size}" for chat in ("agent", "assistant")
                       for look in ("light", "dark") for size in ("", "-ax")]
            sim, directory = os.environ.get("FEEDBACK_SIM"), os.environ.get("FEEDBACK_SHOTS")
            if name not in allowed or not sim or not directory:
                return self.send_json({"error": "Configure the assigned simulator and screenshot directory"}, 400)
            folder = Path(directory).resolve()
            if not str(folder).startswith(("/tmp/", "/private/tmp/")):
                return self.send_json({"error": "Screenshots must use a task temporary directory"}, 400)
            folder.mkdir(parents=True, exist_ok=True)
            target = folder / ("simctl-" + name + ".png")
            try:
                subprocess.run(["/usr/bin/xcrun", "simctl", "io", sim, "screenshot", str(target)],
                               check=True, timeout=20, capture_output=True)
            except (OSError, subprocess.SubprocessError):
                return self.send_json({"error": "Simulator screenshot failed"}, 500)
            return self.send_json({"ok": True, "file": target.name})
        if self.path == "/mock/send-feedback":
            with LOCK:
                return self.send_json({"receipts": list(RECEIPTS)})
        return super().do_GET()

    def do_POST(self):
        if not self.path.endswith("/messages"):
            return super().do_POST()
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        received = time.time()
        delay = 2 if body.get("text", "").startswith("Slow") else 0.15
        time.sleep(delay)
        with LOCK:
            failed = body.get("text", "").startswith("Fail") and not any(r["text"] == body.get("text") for r in RECEIPTS)
        receipt = {"client_msg_id": body.get("client_msg_id"), "text": body.get("text"),
                   "received_at": received, "response_at": time.time(), "failed": failed,
                   "runtime_delay_seconds": 10}
        with LOCK:
            RECEIPTS.append(receipt)
        if failed:
            return self.send_json({"error": "The test send was refused", "code": "send_failed", "delivered": "none"}, 502)
        # No runtime status or transcript echo until ten seconds after acceptance.
        hub.echo_sent(self.path.split("/"), body, delay=10)
        return self.send_json({"ok": True})


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "18762"))
    print(f"send-feedback mock ready on 127.0.0.1:{port}", flush=True)
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
