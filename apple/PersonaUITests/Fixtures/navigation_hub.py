"""Synthetic 81-input, two-turn-tail fixture for the iPhone navigation regression.

Run with the repository mock module's path as the sole argument. All other routes,
including subscriptions and backward pagination, remain the original mock's.
"""
import importlib.util
import os
import sys
from urllib.parse import parse_qs, urlparse

for key in list(os.environ):
    if key.startswith("MOCK_"):
        del os.environ[key]
os.environ["MOCK_LONG_ASSISTANT"] = "1"

spec = importlib.util.spec_from_file_location("navigation_mock", sys.argv[1])
mock = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = mock
spec.loader.exec_module(mock)

for n in range(20):
    mock.ITEMS[mock.ASK1].append(mock.it("user", "text", 0.1 - n * 0.002, text=f"Navigation regression input {n + 62}"))
    mock.ITEMS[mock.ASK1].append(mock.it("assistant", "text", 0.099 - n * 0.002, text=f"Recorded input {n + 62}."))

inputs = []
seen = set()
for item in sorted(mock.ITEMS[mock.ASK1], key=lambda row: row["seq"]):
    if item.get("role") != "user" or item.get("kind") not in ("text", "image") or item.get("tool_use_id"):
        continue
    entry = item["id"].split("#", 1)[0]
    if entry not in seen:
        seen.add(entry)
        inputs.append({"id": item["id"], "seq": item["seq"]})
assert len(inputs) == 81
ordinals = {position["id"].split("#", 1)[0]: n for n, position in enumerate(inputs, 1)}
for item in mock.ITEMS[mock.ASK1]:
    ordinal = ordinals.get(item["id"].split("#", 1)[0])
    if ordinal is not None and item.get("role") == "user":
        item["text"] = f"Navigation regression input {ordinal:02d}"

original_get = mock.H.do_GET
target = f"/api/v1/sessions/server/{mock.ASK1}/items"


def get(self):
    url = urlparse(self.path)
    if url.path != target or parse_qs(url.query).get("user_inputs") != ["1"]:
        return original_get(self)
    mock.HITS["GET " + url.path] = mock.HITS.get("GET " + url.path, 0) + 1
    if self.token_refused() or self.bad_network():
        return
    return self.send_json({"total": len(inputs), "inputs": inputs, "history": mock.HISTORY_NOW["token"]})


mock.H.do_GET = get
mock.PAGING["on"] = True
server = mock.ThreadingHTTPServer(("127.0.0.1", mock.PORT), mock.H)
server.daemon_threads = True
mock.PORT = server.server_port
mock.ENDPOINTS.append(mock.endpoint("127.0.0.1", mock.PORT, "http", "lan"))
print(f"Navigation mock ready: inputs={len(inputs)}, initial transcript turns=2", flush=True)
try:
    server.serve_forever()
finally:
    server.server_close()
