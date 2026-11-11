#!/usr/bin/env python3
"""Minimal App Store Connect API client with no third-party dependencies.

The ES256 JWT is signed with the system `openssl`, so this runs on a stock
macOS Python. Credentials come from the config (docs/operations/config.md), overridable with ASC_KEY_ID, ASC_ISSUER_ID,
ASC_KEY_PATH in the environment.

The App Store Connect app id (not the bundle id) is read from the config
(app_store.app_id in the config) — set once the App record exists in App Store Connect.

Usage as a CLI for quick reads:
  python3 scripts/release/asc.py GET /v1/apps
"""
import base64
import http.client
import json
import os
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import persona_config  # noqa: E402

API = "https://api.appstoreconnect.apple.com"
CONFIG = persona_config.load()
STORE = CONFIG.get("app_store", {})
BUNDLE_ID = persona_config.get(CONFIG, "app.bundle_id", "")
KEY_ID = os.environ.get("ASC_KEY_ID") or STORE.get("key_id", "")
ISSUER = os.environ.get("ASC_ISSUER_ID") or STORE.get("issuer_id", "")
KEY_PATH = os.path.expanduser(os.environ.get("ASC_KEY_PATH") or STORE.get("key_path") or f"~/.appstoreconnect/AuthKey_{KEY_ID}.p8")


def app_id() -> str:
    """App Store Connect numeric app id, from app_store.app_id in the config (docs/operations/config.md)."""
    value = str(STORE.get("app_id", "")).strip()
    if not value.isdigit():
        sys.exit(
            f"app_store.app_id in {persona_config.path()} must be the numeric Apple ID of the App record, got {value!r}.\n"
            f"Create the record on the web first (App Store Connect › Apps › + › New App, bundle id {BUNDLE_ID}). "
            "See docs/operations/release.md."
        )
    return value


_token = (0.0, "")


def _b64(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def _der_to_raw(der: bytes) -> bytes:
    """Convert an ASN.1 DER ECDSA signature to the raw r||s form JWT expects."""
    def read_int(buf, i):
        assert buf[i] == 0x02
        n = buf[i + 1]
        return int.from_bytes(buf[i + 2:i + 2 + n], "big"), i + 2 + n
    i = 2 if der[1] < 0x80 else 3
    r, i = read_int(der, i)
    s, _ = read_int(der, i)
    return r.to_bytes(32, "big") + s.to_bytes(32, "big")


def token() -> str:
    global _token
    if _token[0] > time.time() + 60:
        return _token[1]
    if not os.path.exists(KEY_PATH):
        sys.exit(f"App Store Connect key not found: {KEY_PATH} (set ASC_KEY_PATH)")
    now = int(time.time())
    header = _b64(json.dumps({"alg": "ES256", "kid": KEY_ID, "typ": "JWT"}).encode())
    payload = _b64(json.dumps({"iss": ISSUER, "iat": now, "exp": now + 1200, "aud": "appstoreconnect-v1"}).encode())
    signing_input = f"{header}.{payload}".encode()
    der = subprocess.run(["openssl", "dgst", "-sha256", "-sign", KEY_PATH], input=signing_input,
                         capture_output=True, check=True).stdout
    _token = (now + 1200, f"{header}.{payload}.{_b64(_der_to_raw(der))}")
    return _token[1]


class APIError(Exception):
    def __init__(self, status, body):
        super().__init__(f"HTTP {status}: {body}")
        self.status, self.body = status, body


class NetworkError(APIError):
    """No answer from App Store Connect: the connection failed or broke on every try (status 0). An APIError, so the
    callers that let an API failure pass (the external group after a release) let this pass too."""
    def __init__(self, method: str, path: str, err: BaseException, tries: int, note: str = ""):
        Exception.__init__(self, f"{method} {path.split('?')[0]}: no answer from App Store Connect after "
                                 f"{tries} {'try' if tries == 1 else 'tries'} ({err}){note}")
        self.status, self.body = 0, str(err)


# One try and five more; the waits between them double from 2 s up to 30 s, about a minute in all.
TRIES = 6
MAX_DELAY = 30

# A connection that fails or breaks for a moment (#4034): App Store Connect now and then ends the TLS handshake early
# (URLError of "[SSL: UNEXPECTED_EOF_WHILE_READING]"), resets the connection or doesn't answer in time.
TRANSIENT = (urllib.error.URLError, ConnectionError, TimeoutError, ssl.SSLError, http.client.IncompleteRead,
             http.client.BadStatusLine)


def _delay(attempt: int) -> int:
    return min(2 ** attempt, MAX_DELAY)


def _transient(err: BaseException) -> bool:
    reason = getattr(err, "reason", err)
    return not isinstance(reason, ssl.SSLCertVerificationError)  # a certificate that fails is no hiccup


def request(method: str, path: str, body=None, params=None):
    """One API call. HTTP 429 and 5xx, and a connection that fails or breaks, are tried again with growing waits; any
    other answer raises APIError at once (a 4xx is the caller's to handle). A POST is tried again after a network
    error only when urllib says it never got out (URLError: while connecting or sending), because a POST repeated
    after App Store Connect took it can make a second resource (two beta groups of one name, #945)."""
    url = path if path.startswith("http") else API + path
    if params:
        url += ("&" if "?" in url else "?") + urllib.parse.urlencode(params, safe=",[]")
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={
        "Authorization": f"Bearer {token()}", "Content-Type": "application/json"})
    last = None
    for attempt in range(1, TRIES + 1):
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                raw = resp.read()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as e:  # an answer (HTTPError is also a URLError, so it comes first)
            if e.code != 429 and e.code < 500:
                raise APIError(e.code, e.read().decode()) from None
            last = APIError(e.code, e.read().decode(errors="replace"))
        except TRANSIENT as e:
            if not _transient(e):
                raise NetworkError(method, path, e, attempt) from None
            if method == "POST" and not isinstance(e, urllib.error.URLError):
                raise NetworkError(method, path, e, attempt,
                                   "; not tried again, since App Store Connect may have taken it") from None
            last = NetworkError(method, path, e, attempt)
        if attempt < TRIES:
            print(f"{method} {path.split('?')[0]}: {last.body.strip()[:200]}; trying again in {_delay(attempt)} s",
                  file=sys.stderr)
            time.sleep(_delay(attempt))
    raise last


def get_all(path: str, params=None):
    """GET every page of a collection and return the concatenated data."""
    out, url = [], path
    params = dict(params or {}, limit=200)
    while url:
        page = request("GET", url, params=params)
        out += page["data"]
        url, params = page.get("links", {}).get("next"), None
    return out


def create(type_: str, attributes: dict, relationships: dict, path: str):
    rels = {k: {"data": {"type": t, "id": i}} for k, (t, i) in relationships.items()}
    return request("POST", path, {"data": {"type": type_, "attributes": attributes, "relationships": rels}})["data"]


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit(__doc__)
    method, path = sys.argv[1], sys.argv[2]
    print(json.dumps(request(method, path), indent=1, ensure_ascii=False))
