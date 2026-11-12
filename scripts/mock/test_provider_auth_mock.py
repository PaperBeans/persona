"""mock_hub.py's typed-in gateway probes sign in as the hub's do (#5329, launcher_auth_auto, docs/integrations/launchers.md;
the hub's own cases are in server/internal/assistant): `auth` "auto" tries Bearer, then x-api-key, and names the one that
worked (the model list never says: it may answer without looking at the key); a method asked for is the only one sent; Responses and Chat Completions use Bearer. The fake gateway takes only
x-api-key with "xapikey" in its address, only Bearer with "bearer-only", else both. Each case runs its own mock hub on a
free port."""
import unittest

import readiness_mock
from test_readiness_mock import hub

KEY = "sk-example-123456"
BAD_KEY = "401 认证失败：网关不认这个 Key"  # the mock's answer to a key with "bad" in it
REFUSED = readiness_mock.t("zh-Hans", "assistant-models_probe.status_401")  # the hub's words for a refused method


def probe(h, kind, url, **body):
    status, out = h.call("POST", "/api/v1/launchers/" + kind, dict({"base_url": url, "api_key": KEY, "model": "m1"}, **body))
    assert status == 200, out
    return out


class ProviderAuthMockTests(unittest.TestCase):
    def test_features(self):
        _, health = hub(self, MOCK_PROVIDERS="1").call("GET", "/api/v1/health")
        self.assertIn("launcher_auth_auto", health["features"])
        _, plain = hub(self).call("GET", "/api/v1/health")
        self.assertNotIn("launcher_auth_auto", plain["features"])

    def test_auto_names_the_method_that_worked(self):
        h = hub(self, MOCK_PROVIDERS="1")
        for url, want in (("https://gw.example.com", "bearer"), ("https://xapikey.example.com", "api_key"),
                          ("https://bearer-only.example.com", "bearer")):
            with self.subTest(url=url):
                self.assertEqual(probe(h, "test", url, auth="auto"), {"ok": True, "ms": 412, "model": "m1", "auth": want})
                found = probe(h, "discover", url, auth="auto")
                self.assertNotIn("auth", found)
                self.assertEqual(len(found["models"]), 3)
        # both refused (a bad key): the failure, no method
        out = probe(h, "test", "https://gw.example.com", auth="auto", api_key="sk-bad-123456")
        self.assertEqual((out["ok"], out["error"]), (False, BAD_KEY))
        self.assertNotIn("auth", out)
        self.assertEqual([t["auth"] for t in h.call("GET", "/mock/providers/log")[1]["test"]], ["auto"] * 4)

    def test_a_method_asked_for_is_the_only_one(self):
        h = hub(self, MOCK_PROVIDERS="1")
        for url, auth, ok in (("https://xapikey.example.com", "bearer", False), ("https://xapikey.example.com", "api_key", True),
                              ("https://bearer-only.example.com", "api_key", False), ("https://gw.example.com", "api_key", True)):
            with self.subTest(url=url, auth=auth):
                out = probe(h, "test", url, auth=auth)
                self.assertEqual(out["ok"], ok, out)
                self.assertNotIn("auth", out)
                if not ok:
                    self.assertEqual((out["error"], out["code"]), (REFUSED, "auth"))
                listed = probe(h, "discover", url, auth=auth)
                self.assertEqual(bool(listed["models"]), ok, listed)
                self.assertNotIn("auth", listed)

    def test_no_method_sends_what_is_stored_else_both(self):
        h = hub(self, MOCK_PROVIDERS="1")
        # typed by an older client: both headers, whatever the gateway takes
        self.assertTrue(probe(h, "test", "https://xapikey.example.com")["ok"])
        self.assertTrue(probe(h, "test", "https://bearer-only.example.com", auth="")["ok"])
        # by name: gw_full stores Bearer, anthropic_main x-api-key
        self.assertFalse(probe(h, "test", "https://xapikey.example.com", name="gw_full", api_key="")["ok"])
        self.assertTrue(probe(h, "test", "https://xapikey.example.com", name="anthropic_main", api_key="")["ok"])
        self.assertFalse(probe(h, "test", "https://bearer-only.example.com", name="anthropic_main", api_key="")["ok"])

    def test_responses_and_chat_use_bearer(self):
        h = hub(self, MOCK_PROVIDERS="1")
        for api in ("responses", "chat"):
            with self.subTest(api=api):
                out = probe(h, "test", "https://gw.example.com", api=api, auth="auto")
                self.assertTrue(out["ok"], out)
                self.assertNotIn("auth", out)
                self.assertFalse(probe(h, "test", "https://xapikey.example.com", api=api, auth="api_key")["ok"])

    def test_unknown_method_is_refused_in_the_hubs_words(self):
        h = hub(self, MOCK_PROVIDERS="1")
        out = probe(h, "test", "https://gw.example.com", auth="basic")
        self.assertEqual((out["ok"], out["error"]), (False, "认证方式不支持"))
        _, en = h.call("POST", "/api/v1/launchers/discover", {"base_url": "https://gw.example.com", "auth": "basic"}, lang="en")
        self.assertEqual(en, {"models": [], "error": "Unsupported authentication method"})

    def test_without_mock_providers_too(self):
        h = hub(self)
        self.assertEqual(probe(h, "test", "https://xapikey.example.com", auth="auto")["auth"], "api_key")
        self.assertFalse(probe(h, "test", "https://xapikey.example.com", auth="bearer")["ok"])


if __name__ == "__main__":
    unittest.main()
