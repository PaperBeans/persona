"""App Store Connect calls in asc.py: what is tried again after a dropped connection or an error answer, and what is not
(#4034)."""
import atexit
import contextlib
import http.client
import io
import json
import os
from pathlib import Path
import ssl
import sys
import tempfile
import unittest
import urllib.error
from unittest.mock import MagicMock, patch

HERE = Path(__file__).resolve().parent
_config = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
json.dump({"app": {"bundle_id": "com.example.app"}, "app_store": {"app_id": "1234567890"}}, _config)
_config.close()
atexit.register(os.unlink, _config.name)
os.environ["PERSONA_CONFIG"] = _config.name
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parent))
import asc  # noqa: E402

# What the auto-tf rounds hit (#4034): the TLS handshake ended early, which urllib raises as a URLError.
HANDSHAKE_EOF = urllib.error.URLError(ssl.SSLEOFError(8, "[SSL: UNEXPECTED_EOF_WHILE_READING] EOF occurred in violation of protocol"))


def answer(body: bytes = b'{"data": []}'):
    resp = MagicMock()
    resp.__enter__.return_value.read.return_value = body
    return resp


def http_error(code: int, body: str = "{}"):
    return urllib.error.HTTPError(asc.API + "/v1/builds", code, "error", {}, io.BytesIO(body.encode()))


class RequestRetryTests(unittest.TestCase):
    def setUp(self):
        for patcher in (patch.object(asc, "token", return_value="t"), patch.object(asc.time, "sleep")):
            mock = patcher.start()
            self.addCleanup(patcher.stop)
        self.sleep = mock
        self.stderr = io.StringIO()
        redirect = contextlib.redirect_stderr(self.stderr)
        redirect.__enter__()
        self.addCleanup(redirect.__exit__, None, None, None)

    def call(self, side_effect, method="GET"):
        with patch.object(asc.urllib.request, "urlopen", side_effect=side_effect) as urlopen:
            try:
                return asc.request(method, "/v1/builds", params={"filter[app]": "1"}), urlopen
            except asc.APIError as e:
                e.urlopen = urlopen
                raise

    def test_a_handshake_ended_early_is_tried_again(self):
        result, urlopen = self.call([HANDSHAKE_EOF, answer(b'{"data": [1]}')])
        self.assertEqual(result, {"data": [1]})
        self.assertEqual(urlopen.call_count, 2)
        self.sleep.assert_called_once_with(2)
        self.assertIn("UNEXPECTED_EOF_WHILE_READING", self.stderr.getvalue())
        self.assertIn("trying again in 2 s", self.stderr.getvalue())

    def test_waits_double_up_to_a_cap_then_one_line_error(self):
        with self.assertRaises(asc.NetworkError) as raised:
            self.call([HANDSHAKE_EOF] * asc.TRIES)
        self.assertEqual(raised.exception.urlopen.call_count, asc.TRIES)
        self.assertEqual([c.args[0] for c in self.sleep.call_args_list], [2, 4, 8, 16, 30])
        self.assertIsInstance(raised.exception, asc.APIError)  # callers that let API failures pass let it pass too
        self.assertEqual(raised.exception.status, 0)
        message = str(raised.exception)
        self.assertIn(f"after {asc.TRIES} tries", message)
        self.assertIn("UNEXPECTED_EOF_WHILE_READING", message)
        self.assertNotIn("?", message)  # the path only, not the query

    def test_a_reset_or_timeout_while_reading_is_tried_again(self):
        for err in (ConnectionResetError(54, "Connection reset by peer"), http.client.RemoteDisconnected("closed"),
                    TimeoutError("The read operation timed out"), http.client.IncompleteRead(b"{")):
            with self.subTest(err=type(err).__name__):
                result, urlopen = self.call([err, answer()])
                self.assertEqual(result, {"data": []})
                self.assertEqual(urlopen.call_count, 2)

    def test_429_and_5xx_are_tried_again(self):
        result, urlopen = self.call([http_error(503), http_error(429), answer()])
        self.assertEqual(result, {"data": []})
        self.assertEqual(urlopen.call_count, 3)

    def test_5xx_that_lasts_keeps_its_status(self):
        with self.assertRaises(asc.APIError) as raised:
            self.call([http_error(502, "bad gateway")] * asc.TRIES)
        self.assertEqual(raised.exception.status, 502)

    def test_4xx_fails_at_once(self):
        for code in (401, 404, 409, 422):
            with self.subTest(code=code):
                self.sleep.reset_mock()
                with self.assertRaises(asc.APIError) as raised:
                    self.call([http_error(code, '{"errors": []}')])
                self.assertEqual(raised.exception.status, code)
                self.assertEqual(raised.exception.urlopen.call_count, 1)
                self.sleep.assert_not_called()

    def test_a_post_that_never_got_out_is_tried_again(self):
        result, urlopen = self.call([HANDSHAKE_EOF, answer(b'{"data": {"id": "g"}}')], method="POST")
        self.assertEqual(result, {"data": {"id": "g"}})
        self.assertEqual(urlopen.call_count, 2)

    def test_a_post_that_may_have_been_taken_is_not_tried_again(self):
        # The request went out and the connection broke before the answer: App Store Connect may have made the resource.
        with self.assertRaises(asc.NetworkError) as raised:
            self.call([http.client.RemoteDisconnected("closed"), answer()], method="POST")
        self.assertEqual(raised.exception.urlopen.call_count, 1)
        self.assertIn("not tried again", str(raised.exception))
        self.sleep.assert_not_called()

    def test_a_patch_after_a_reset_is_tried_again(self):  # it sets attributes, so a repeat changes nothing
        result, urlopen = self.call([ConnectionResetError(), answer(b"")], method="PATCH")
        self.assertIsNone(result)
        self.assertEqual(urlopen.call_count, 2)

    def test_a_certificate_that_fails_is_not_tried_again(self):
        bad = urllib.error.URLError(ssl.SSLCertVerificationError(1, "certificate verify failed"))
        with self.assertRaises(asc.NetworkError) as raised:
            self.call([bad, answer()])
        self.assertEqual(raised.exception.urlopen.call_count, 1)
        self.sleep.assert_not_called()


if __name__ == "__main__":
    unittest.main()
