"""MOCK_ENGINE_SWITCH (#5054): a pick of the other engine's launcher waits on the row with its engine, the next message
moves the conversation to a session of that engine (the row replaced, engine_history, the handoff block in front of
the message), switching back resumes the earlier session, a refusal drops the switch, =done lists a conversation that
went Claude Code -> Codex -> Claude Code, and MOCK_LAUNCHER_SWITCH alone answers as before."""
import json
import socket
import unittest
from urllib.error import HTTPError
from urllib.request import Request, urlopen
from test_mock_hub_mesh import Mock

REVIEW = "a1f3c2d4-0000-4000-8000-000000000006"  # an idle Claude Code Agent on the MacBook
ORDERS = "a1f3c2d4-0000-4000-8000-000000000004"  # a Codex Agent in a terminal on the server
DONE = "e5054000-0000-4000-8000-000000000001"
DONE_CODEX = "019a5054-0000-7000-8000-000000000002"


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class EngineSwitchMockTests(unittest.TestCase):
    def hub(self, **env):
        hub = Mock(free_port(), **env)
        self.addCleanup(hub.stop)
        return hub

    def post(self, hub, path, body):
        req = Request(hub.base + path, data=json.dumps(body).encode(), method="POST",
                      headers={"Content-Type": "application/json", "Authorization": "Bearer mock"})
        try:
            with urlopen(req, timeout=5) as response:
                return response.status, json.load(response)
        except HTTPError as e:
            with e:
                return e.code, json.loads(e.read() or b"null")

    def rows(self, hub):
        return {s["session_id"]: s for s in hub.get("/api/v1/sessions")["sessions"]}

    def items(self, hub, mid, sid):
        return hub.get(f"/api/v1/sessions/{mid}/{sid}/items")["items"]

    def test_pick_waits_then_the_message_moves_the_conversation_and_back(self):
        hub = self.hub(MOCK_ENGINE_SWITCH="1")
        self.assertIn("engine_switch", hub.get("/api/v1/health")["features"])
        self.assertEqual(self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/modes", {"launcher": "codex", "from": "claude", "model": "gpt-5.5-codex"}), (200, {}))
        self.assertEqual({k: v for k, v in self.rows(hub)[REVIEW]["pending_switch"].items() if k != "at"},
                         {"launcher": "codex", "model": "gpt-5.5-codex", "agent": "codex"})
        # picked against a launcher it no longer runs on: stale
        self.assertEqual(self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/modes", {"launcher": "codex", "from": "work"})[0], 409)

        self.assertEqual(self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/messages", {"text": "Rename them too.", "client_msg_id": "m1", "apply_switch": True})[0], 200)
        rows = self.rows(hub)
        self.assertNotIn(REVIEW, rows)  # one conversation, one row
        new = next(s for s in rows.values() if s.get("engine_history"))
        self.assertEqual((new["agent"], new["launcher"], new["model"], new["title"]), ("codex", "codex", "gpt-5.5-codex", "检查 PR #42 的改动"))
        self.assertNotIn("pending_switch", new)
        self.assertEqual([(l["session_id"], l["agent"]) for l in new["engine_history"]], [(REVIEW, "claude")])
        first, reply = self.items(hub, "macbook", new["session_id"])
        self.assertTrue(first["text"].startswith('<persona-context kind="engine-switch" title="Handoff" from="claude" to="codex" model="gpt-5.5-codex">\n'))
        self.assertTrue(first["text"].endswith("</persona-context>\n\nRename them too."))
        self.assertNotIn("resumed=", first["text"])
        self.assertGreater(first["ts"], new["engine_history"][0]["until"])  # the new stretch starts after the old one ends
        self.assertEqual(reply["role"], "assistant")
        self.assertTrue(self.items(hub, "macbook", REVIEW))  # the old stretch stays readable

        # picked and dropped again, then back to Claude Code: the earlier session is resumed with what happened meanwhile
        sid = new["session_id"]
        self.post(hub, f"/api/v1/sessions/macbook/{sid}/modes", {"launcher": "claude", "from": "codex", "model": "opus"})
        self.assertEqual(self.post(hub, f"/api/v1/sessions/macbook/{sid}/modes", {"launcher": "codex", "from": "codex"}), (200, {}))
        self.assertNotIn("pending_switch", self.rows(hub)[sid])
        self.post(hub, f"/api/v1/sessions/macbook/{sid}/modes", {"launcher": "claude", "from": "codex", "model": "opus"})
        self.post(hub, f"/api/v1/sessions/macbook/{sid}/messages", {"text": "Write the PR description.", "client_msg_id": "m2", "apply_switch": True})
        rows = self.rows(hub)
        self.assertNotIn(sid, rows)
        back = rows[REVIEW]
        self.assertEqual(back["agent"], "claude")
        self.assertEqual([(l["session_id"], l["agent"]) for l in back["engine_history"]], [(REVIEW, "claude"), (sid, "codex")])
        resumed = self.items(hub, "macbook", REVIEW)[-2]["text"]
        self.assertIn('from="codex" to="claude" model="opus" resumed="1"', resumed)
        self.assertIn("Rename them too.", resumed)  # the stretch while it was away
        self.assertTrue(resumed.endswith("Write the PR description."))

    def test_a_codex_agent_moves_to_claude_code(self):
        hub = self.hub(MOCK_ENGINE_SWITCH="1")
        self.assertEqual(self.rows(hub)[ORDERS]["launcher"], "codex")
        self.post(hub, f"/api/v1/sessions/server/{ORDERS}/modes", {"launcher": "claude", "from": "codex", "model": "opus", "effort": "high"})
        self.assertEqual(self.rows(hub)[ORDERS]["pending_switch"]["agent"], "claude")
        self.post(hub, f"/api/v1/sessions/server/{ORDERS}/messages", {"text": "Write the migration.", "client_msg_id": "m3", "apply_switch": True})
        new = next(s for s in self.rows(hub).values() if s.get("engine_history"))
        self.assertEqual((new["machine_id"], new["agent"], new["launcher"], new["effort"], new["permission_mode"]), ("server", "claude", "claude", "high", "default"))

    def test_pending_refuse_and_reset(self):
        hub = self.hub(MOCK_ENGINE_SWITCH="pending,refuse")
        self.assertEqual(self.rows(hub)[REVIEW]["pending_switch"]["agent"], "codex")
        status, body = self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/messages", {"text": "go", "client_msg_id": "m4", "apply_switch": True})
        self.assertEqual((status, body["code"], body["delivered"]), (409, "switch_failed", "none"))
        self.assertNotIn("pending_switch", self.rows(hub)[REVIEW])
        hub.get("/mock/engine_switch?reset=1")
        self.assertEqual(self.rows(hub)[REVIEW]["pending_switch"]["agent"], "codex")

    def test_done_lists_one_row_for_three_stretches(self):
        hub = self.hub(MOCK_ENGINE_SWITCH="done")
        rows = self.rows(hub)
        self.assertNotIn(DONE_CODEX, rows)
        legs = rows[DONE]["engine_history"]
        self.assertEqual([(l["session_id"], l["agent"]) for l in legs], [(DONE, "claude"), (DONE_CODEX, "codex")])
        t1, t2 = legs[0]["until"], legs[1]["until"]
        claude, codex = self.items(hub, "macbook", DONE), self.items(hub, "macbook", DONE_CODEX)
        self.assertTrue(all(x["ts"] <= t1 or x["ts"] > t2 for x in claude))
        self.assertTrue(all(t1 < x["ts"] <= t2 for x in codex))
        self.assertIn('from="claude" to="codex" model="gpt-5.5">', codex[0]["text"])
        resumed = next(x for x in claude if x["ts"] > t2)
        self.assertIn('from="codex" to="claude" model="opus" resumed="1">', resumed["text"])

    def test_launcher_switch_alone_answers_as_before(self):
        hub = self.hub(MOCK_LAUNCHER_SWITCH="1")
        self.assertNotIn("engine_switch", hub.get("/api/v1/health")["features"])
        status, body = self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/modes", {"launcher": "codex", "from": "claude"})
        self.assertEqual((status, body["code"]), (409, "switch_other_agent"))
        self.assertEqual(self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/modes", {"launcher": "work", "from": "claude"}), (200, {}))
        self.assertNotIn("agent", self.rows(hub)[REVIEW]["pending_switch"])

    def test_both_flags_leave_another_launcher_of_the_engine_to_the_launcher_switch(self):
        hub = self.hub(MOCK_ENGINE_SWITCH="1", MOCK_LAUNCHER_SWITCH="1")
        self.assertEqual(self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/modes", {"launcher": "work", "from": "claude"}), (200, {}))
        self.assertNotIn("agent", self.rows(hub)[REVIEW]["pending_switch"])
        self.assertEqual(self.post(hub, f"/api/v1/sessions/macbook/{REVIEW}/modes", {"launcher": "codex", "from": "claude"}), (200, {}))
        self.assertEqual(self.rows(hub)[REVIEW]["pending_switch"]["agent"], "codex")


if __name__ == "__main__":
    unittest.main()
