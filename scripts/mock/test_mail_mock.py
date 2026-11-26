#!/usr/bin/env python3
"""The mock hub's mail with the mail module off (scripts/mock/mail_mock.py, #15995): no mail routes, as a hub without
the module answers, and the Inbox item's stored text still readable.

    python3 scripts/mock/test_mail_mock.py
"""
import os
from pathlib import Path
import re
import sys
import types
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import hub_modules_mock  # noqa: E402
import mail_mock  # noqa: E402

ITEM = {"id": "mail:acc1:t1:501", "source": "mail", "title": "Weekly sync", "preview": "Hi, the agenda", "updated_at": "2026-11-20T09:00:00Z",
        "mail": {"account_id": "acc1", "mailbox": "INBOX", "uids": [500, 501], "from": {"name": "Ana", "email": "ana@example.com"}}}


class Handler:
    def __init__(self):
        self.sent = None

    def send_json(self, body, status=200):
        self.sent = (status, body)
        return True


class Registry:
    def __init__(self, hub):
        self.hub, self.routes = hub, []

    def route(self, methods, pattern, handler):
        self.routes.append((methods, pattern, handler))

    def answer(self, method, path):
        """The first registered route that matches, as mock_domains picks it."""
        for methods, pattern, handler in self.routes:
            m = re.fullmatch(pattern, path)
            if m and (method == methods or method in methods):
                h = Handler()
                req = types.SimpleNamespace(method=method, path=path, query={}, match=m, body=b"")
                return handler(h, req), h.sent
        return None, None


def hub(**over):
    return types.SimpleNamespace(**{"NO_SOURCES": False, "ago": lambda **_: "2026-11-20T09:00:00Z", "INBOX": [ITEM], "INBOX_HISTORY": [],
                                    "MAIL_MESSAGES": {}, "MAIL_BODIES": {501: "Hi,\n\nThe agenda is below.\nAna"}, **over})


def registered(mode, **over):
    with patch.object(hub_modules_mock, "MODE", mode):
        d = Registry(hub(**over))
        mail_mock.register(d)
        return d


class MailOffTests(unittest.TestCase):
    def test_off_only_when_the_hub_names_modules_without_mail(self):
        for mode, off in (("", False), ("module.mail", False), ("module.lark,module.mail", False), ("none", True), ("module.lark", True)):
            with patch.object(hub_modules_mock, "MODE", mode):
                self.assertEqual(mail_mock.mail_off(), off, mode)

    def test_off_answers_404_on_every_mail_route(self):
        d = registered("module.lark")
        for method, path in (("GET", "/api/v1/mail/accounts"), ("GET", "/api/v1/mail/messages/acc1/501"), ("POST", "/api/v1/mail/send"),
                             ("POST", "/api/v1/mail/messages/acc1/501/flags")):
            self.assertEqual(d.answer(method, path)[1], (404, {"error": "mail is not set up"}), path)

    def test_off_keeps_the_stored_text_of_a_mail_item(self):
        d = registered("module.lark")
        for path in ("/api/v1/inbox/mail:acc1:t1:501", "/api/v1/inbox/mail%3Aacc1%3At1%3A501"):
            _, (status, body) = d.answer("GET", path)
            self.assertEqual(status, 200)
            self.assertEqual(body["id"], ITEM["id"])
            head, _, text = body["context"].partition("\n\n")
            self.assertEqual(text, "Hi,\n\nThe agenda is below.\nAna")
            self.assertIn("Ana <ana@example.com>", head)
            self.assertIn("Weekly sync", head)
        # an item that is not a mail, or not there, is the hub's own to answer
        self.assertEqual(d.answer("GET", "/api/v1/inbox/mail:acc1:none:1"), (False, None))
        self.assertEqual(d.answer("GET", "/api/v1/inbox/lark:f1:100"), (None, None))

    def test_stored_text_is_clipped_as_the_hub_clips_it(self):
        d = registered("none", MAIL_BODIES={501: "x" * 5000})
        _, (_, body) = d.answer("GET", "/api/v1/inbox/mail:acc1:t1:501")
        self.assertEqual(len(body["context"].partition("\n\n")[2]), 3000)

    def test_the_item_page_says_whether_the_stored_text_was_clipped(self):
        path = "/api/v1/inbox/mail:acc1:t1:501"
        with patch.dict(os.environ, {"MOCK_MAIL_STORED_TEXT": ""}):
            self.assertIs(registered("none").answer("GET", path)[1][1]["mail_body_truncated"], False)
            self.assertIs(registered("none", MAIL_BODIES={501: "x" * 5000}).answer("GET", path)[1][1]["mail_body_truncated"], True)
        with patch.dict(os.environ, {"MOCK_MAIL_STORED_TEXT": "clipped"}):
            body = registered("none").answer("GET", path)[1][1]
            self.assertIs(body["mail_body_truncated"], True)
            text = body["context"].partition("\n\n")[2]
            self.assertEqual(len(text), 3000)
            self.assertTrue(text.startswith("Hi,\n\nThe agenda is below.\nAna"))
        with patch.dict(os.environ, {"MOCK_MAIL_STORED_TEXT": "unknown"}):
            body = registered("none").answer("GET", path)[1][1]
            self.assertNotIn("mail_body_truncated", body)
            self.assertEqual(body["context"].partition("\n\n")[2], "Hi,\n\nThe agenda is below.\nAna")
        with patch.dict(os.environ, {"MOCK_MAIL_STORED_TEXT": "whole"}), self.assertRaises(SystemExit):
            registered("none").answer("GET", path)

    def test_on_or_an_older_hub_serves_mail_and_leaves_the_inbox_route_alone(self):
        for mode in ("", "module.mail"):
            d = registered(mode)
            self.assertEqual(d.answer("GET", "/api/v1/mail/accounts")[1][0], 200, mode)
            self.assertEqual(d.answer("GET", "/api/v1/inbox/mail:acc1:t1:501"), (None, None), mode)


if __name__ == "__main__":
    unittest.main()
