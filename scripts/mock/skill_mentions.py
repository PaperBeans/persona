#!/usr/bin/env python3
"""Synthetic Web-only skill-picker evidence. Never connects to a real hub or runtime."""

import os
import signal
import time
from http.server import ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

os.environ["MOCK_MANAGERS"] = "0"
import mock_hub as hub

STATE = "ready"
SKILLS = [
    {"id": "fixture-project", "name": "review", "description": "Inspect security boundaries and regressions", "source": "project · ."},
    {"id": "fixture-user", "name": "review", "description": "Review a document for clarity", "source": "user · ~/example/skills"},
    {"id": "fixture-design", "name": "interface-design", "description": "Refine accessible, responsive interfaces", "source": "project · ."},
]


class Handler(hub.H):
    def do_GET(self):
        global STATE
        parsed = urlparse(self.path)
        if parsed.path == "/mock/skills":
            STATE = parse_qs(parsed.query).get("state", ["ready"])[0]
            self.send_json({"state": STATE})
            return
        if parsed.path == "/api/v1/assistant/skills":
            state = STATE
            if state == "loading":
                time.sleep(2)
            if state == "error":
                self.send_json({"error": "Synthetic catalog unavailable", "code": "unavailable"}, 503)
            else:
                self.send_json({"catalog_version": 1, "complete": state != "partial", "skills": SKILLS})
            return
        super().do_GET()


def main():
    hub.SESSIONS[:] = []
    hub.ASSISTANT[:] = [hub.assistant_sess(hub.ASK1, "Example conversation", 1, "Synthetic skill selection example")]
    hub.ITEMS[hub.ASK1] = []
    hub.TODOS[:] = []
    hub.INTERACTIONS[:] = []
    hub.INBOX[:] = []
    for index, machine in enumerate(hub.MACHINES):
        machine["name"] = f"Example computer {index + 1}"
    server = ThreadingHTTPServer(("127.0.0.1", hub.PORT), Handler)
    server.daemon_threads = True
    hub.ENDPOINTS.append(hub.endpoint("127.0.0.1", server.server_port, "http", "lan"))
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    print(f"Synthetic skill fixture ready on loopback port {server.server_port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
