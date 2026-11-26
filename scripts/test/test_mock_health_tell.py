"""Tell Health's mock accepts one receipt and returns it again for the same invocation."""
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "mock"))
import health_tell_mock as tell
import mock_domains


class Handler:
    def send_json(self, value, status=200):
        self.value, self.status = value, status


class HealthTellMockTests(unittest.TestCase):
    def setUp(self):
        tell.ENABLED = True
        tell.reset()

    def request(self, body):
        handler = Handler()
        tell.tell(handler, SimpleNamespace(json=lambda: body))
        return handler

    def test_receipt_and_duplicate_preserve_the_original_id(self):
        first = self.request({"client_id": "sample-invocation", "text": "Sample workout"})
        second = self.request({"client_id": "sample-invocation", "text": "Different text"})
        self.assertEqual(first.status, 202)
        self.assertEqual(first.value["status"], "received")
        self.assertEqual(second.status, 200)
        self.assertEqual(second.value, dict(first.value, duplicate=True))
        self.assertNotIn("duplicate", first.value)

    def test_invalid_input_and_disabled_capability(self):
        for body in [[], {}, {"client_id": "short", "text": "Sample"},
                     {"client_id": "sample-id", "text": "  "},
                     {"client_id": "sample-id", "text": "a" * 8193}]:
            self.assertEqual(self.request(body).status, 400)
        tell.ENABLED = False
        self.assertEqual(self.request({"client_id": "sample-id", "text": "Sample"}).status, 404)

    def test_domain_registers_the_capability_and_reset(self):
        registry = mock_domains.Registry({})
        domain = mock_domains.Domain(registry, "health_tell_mock")
        with patch.dict("os.environ", {"MOCK_HEALTH_TELL": "off"}):
            tell.register(domain)
        self.assertFalse(tell.ENABLED)
        with patch.dict("os.environ", {"MOCK_HEALTH_TELL": "on"}):
            tell.register(domain)
        self.assertTrue(tell.ENABLED)
        self.request({"client_id": "sample-id", "text": "Sample"})
        tell.reset()
        self.assertEqual(tell.RECEIPTS, {})


if __name__ == "__main__":
    unittest.main()
