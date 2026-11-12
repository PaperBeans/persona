#!/usr/bin/env python3
"""End-to-end check of provider import links (#5420, docs/integrations/launchers.md "Import links"): opening the web's
link (`/settings?tab=models#import=<data>`) cleans the address bar and opens Add provider on the paste view in its link
mode, read at once, and Continue fills in the gateway's steps; a link of another version is refused in plain words with
nothing to continue with; cindy's `cindy://provider/import` pasted into "Paste a configuration" reads as a link; a
provider's ⋯ › Copy import link copies a link without a key by default, and with the key pasted into it only after the
switch shows the warning; the copied link opens back into the same provider.

Needs the web page against the fake hub with MOCK_PROVIDERS=1 and Playwright for Python:

    MOCK_PROVIDERS=1 PORT=19021 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:19021 npx vite --port 5421 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_provider_link_e2e.py http://127.0.0.1:5421 \
        --hub http://127.0.0.1:19021 [--shots <dir>]

Run it against a fresh mock: it saves a provider. Prints one line per check and a verdict; exits 1 on the first failure.
--shots saves 1280 / 390 wide, light and dark. English UI; drives the installed Chrome (channel="chrome").
"""
import argparse
import base64
import json
import os
import re
import sys
import urllib.request
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect, sync_playwright

GATEWAY = {"kind": "custom", "name": "Example Gateway",
           "endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://gateway.example.invalid",
                          "models": ["example-sonnet", {"id": "example-opus", "name": "Example Opus", "contextWindow": 200000}]}]}
CINDY = {"kind": "custom", "name": "Example AI", "auth": {"method": "apiKey", "apiKey": "FAKE-EXAMPLE-KEY"},
         "endpoints": [{"protocol": "openai-chat", "baseUrl": "https://example.invalid/v1"}]}
KEY = "FAKE-LINK-KEY-0001"


def data(payload):
    raw = json.dumps(payload, separators=(",", ":"), ensure_ascii=False).encode()
    return base64.urlsafe_b64encode(raw).decode().rstrip("=")


def payload_of(link):
    frag = parse_qs(urlsplit(link).fragment)
    d = frag["import"][0]
    return json.loads(base64.urlsafe_b64decode(d + "=" * (-len(d) % 4)))


def ok(msg):
    print("ok  " + msg, flush=True)


class Run:
    def __init__(self, page, base, hub, token, shots):
        self.page, self.base, self.hub, self.token, self.shots = page, base, hub, token, shots

    def log(self):
        req = urllib.request.Request(self.hub + "/mock/providers/log", headers={"Authorization": "Bearer " + self.token})
        with urllib.request.urlopen(req, timeout=5) as r:
            return json.load(r)

    def shot(self, name):
        if not self.shots:
            return
        w = self.page.viewport_size["width"]
        for theme in ("light", "dark"):
            self.page.evaluate(f"document.documentElement.classList.toggle('dark', {'true' if theme == 'dark' else 'false'})")
            self.page.wait_for_timeout(350)
            self.page.screenshot(path=os.path.join(self.shots, f"link-{name}-{w}-{theme}.png"))
        self.page.evaluate("document.documentElement.classList.remove('dark')")

    def sign_in(self):
        self.page.goto(self.base + "/overview?token=" + self.token)
        self.page.locator("aside").first.wait_for()

    def open_link(self, fragment):
        """A full load of the link (from another path: a fragment change alone would not reload the app)."""
        self.page.goto(self.base + "/overview")
        self.page.locator("aside").first.wait_for()
        self.page.goto(self.base + "/settings?tab=models#" + fragment)
        return self.dialog("Add provider")

    def open_models(self):
        self.page.goto(self.base + "/settings?tab=models")
        expect(self.page.get_by_text("gw_full", exact=True).first).to_be_visible()

    def dialog(self, title):
        d = self.page.get_by_role("dialog", name=title)
        expect(d).to_be_visible()
        return d


def clean_address(r):
    href = r.page.evaluate("location.href")
    assert "import" not in href and "#" not in href, href
    assert urlsplit(href).path.endswith("/settings") and "tab=models" in href, href
    # nothing in this tab's history state or the Back list holds it either
    assert "import=" not in json.dumps(r.page.evaluate("history.state")), "history.state keeps the link"


def open_link(r):
    parses = len(r.log()["parse"])
    d = r.open_link("import=" + data(GATEWAY))
    clean_address(r)
    expect(d.get_by_text("Opened from a link")).to_be_visible()
    expect(d.get_by_role("textbox", name="Paste a configuration")).to_have_count(0)
    expect(d.get_by_text("Example Gateway").first).to_be_visible()
    expect(d.get_by_text("Read as an import link.")).to_have_count(0)
    # once (twice under the dev server's StrictMode, the first answer dropped)
    read = len(r.log()["parse"]) - parses
    assert read in (1, 2), f"the link was read {read} times"
    r.shot("open")
    ok("opening a link cleans the address bar and opens Add provider on the link view, read at once")
    d.get_by_role("button", name="Continue").click()
    expect(d.get_by_label("Display name")).to_have_value("Example Gateway")
    expect(d.get_by_label("URL")).to_have_value("https://gateway.example.invalid")
    # the link brings no key: whoever opens it types their own
    key = d.get_by_label("API Key", exact=False).first
    expect(key).to_have_value("")
    key.fill("FAKE-OWN-KEY-0001")
    r.shot("connect")
    d.get_by_role("button", name="Next").click()
    expect(d.get_by_text("Example Opus").first).to_be_visible()
    r.shot("models")
    d.get_by_role("button", name="Save").click()
    expect(d).to_be_hidden()
    post = r.log()["post"]
    assert post.get("display_name") == "Example Gateway" and post.get("api_key") == "<set>", post
    assert post.get("base_url") == "https://gateway.example.invalid", post
    assert [m["id"] for m in post.get("models", [])] == ["example-sonnet", "example-opus"], post.get("models")
    ok("Continue fills in the gateway's steps without a key; with the user's own key Save saves the link's name, address and models")


def refused(r):
    d = r.open_link("import=" + data(GATEWAY) + "&v=2")
    clean_address(r)
    expect(d.get_by_text(re.compile("version 2"))).to_be_visible()
    expect(d.get_by_role("button", name="Continue")).to_be_disabled()
    r.shot("refused")
    d.get_by_role("button", name="Paste something else").click()
    expect(d.get_by_role("textbox", name="Paste a configuration")).to_be_visible()
    expect(d.get_by_role("textbox", name="Paste a configuration")).to_have_value("")
    d.get_by_role("button", name="Cancel").click()
    ok("a link of another version is refused in plain words; Paste something else opens an empty text box")


def pasted_cindy(r):
    r.open_models()
    r.page.get_by_role("button", name="Add provider").click()
    d = r.dialog("Add provider")
    d.get_by_role("button", name="Paste a configuration").click()
    d.get_by_role("textbox", name="Paste a configuration").fill("cindy://provider/import?v=1&data=" + data(CINDY))
    expect(d.get_by_text("Read as an import link.")).to_be_visible()
    expect(d.get_by_text("Example AI").first).to_be_visible()
    r.shot("cindy")
    d.get_by_role("button", name="Cancel").click()
    ok("cindy's link pasted into Paste a configuration reads as an import link")


def copy_link(r):
    r.open_models()
    r.page.get_by_text("gw_full", exact=True).first.click()
    detail = r.page.get_by_role("dialog").first
    expect(detail).to_be_visible()
    detail.get_by_role("button", name="More").click()
    r.page.get_by_role("menuitem", name="Copy import link").click()
    d = r.dialog("Copy import link")
    sw = d.get_by_role("switch", name="Put a key in the link")
    expect(sw).to_have_attribute("aria-checked", "false")
    expect(d.get_by_text("Anyone who gets this link", exact=False)).to_have_count(0)
    r.shot("copy")
    d.get_by_role("button", name="Copy link").click()
    expect(d).to_be_hidden()
    link = r.page.evaluate("navigator.clipboard.readText()")
    p = payload_of(link)
    assert "auth" not in p and p["kind"] == "custom", p
    assert p["endpoints"][0]["protocol"] == "anthropic-messages", p
    ok("Copy import link copies a link without a key by default")

    r.page.get_by_text("gw_full", exact=True).first.click()
    r.page.get_by_role("dialog").first.get_by_role("button", name="More").click()
    r.page.get_by_role("menuitem", name="Copy import link").click()
    d = r.dialog("Copy import link")
    d.get_by_role("switch", name="Put a key in the link").click()
    expect(d.get_by_text("Anyone who gets this link", exact=False)).to_be_visible()
    expect(d.get_by_role("button", name="Copy link")).to_be_disabled()
    d.get_by_label("API key", exact=False).first.fill(KEY)
    r.shot("copy-key")
    d.get_by_role("button", name="Copy link").click()
    expect(d).to_be_hidden()
    keyed = r.page.evaluate("navigator.clipboard.readText()")
    assert payload_of(keyed)["auth"] == {"method": "apiKey", "apiKey": KEY}, payload_of(keyed)
    ok("with the switch on the warning shows first, Copy waits for a key, and the link carries the pasted key")

    # the copied link opens back into the same provider (the mock's matching: its address is gw_full's)
    frag = urlsplit(keyed).fragment
    d = r.open_link(frag)
    clean_address(r)
    expect(d.get_by_role("radiogroup", name="What to do with it")).to_be_visible()
    d.get_by_role("button", name="Cancel").click()
    ok("the copied link opens back on the provider it came from")


def phone(browser, base, hub, token, shots):
    ctx = browser.new_context(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True, is_mobile=True,
                              locale="en-US")
    ctx.grant_permissions(["clipboard-read", "clipboard-write"])
    page = ctx.new_page()
    page.set_default_timeout(10000)
    r = Run(page, base, hub, token, shots)
    r.sign_in()
    d = r.open_link("import=" + data(GATEWAY))
    clean_address(r)
    expect(d.get_by_text("Opened from a link")).to_be_visible()
    expect(d.get_by_text("Gateway").first).to_be_visible()
    r.shot("open")
    width = page.evaluate("document.documentElement.scrollWidth")
    assert width <= 390, f"the link view scrolls sideways at 390 px ({width})"
    d.get_by_role("button", name="Cancel").click()
    # a sheet steps back out of its own history entry as it closes: let that land before going on
    expect(d).to_be_hidden()
    page.wait_for_timeout(500)
    r.open_models()
    page.get_by_text("gw_full", exact=True).first.click()
    page.get_by_role("dialog").first.get_by_role("button", name="More").click()
    page.get_by_role("menuitem", name="Copy import link").click()
    d = r.dialog("Copy import link")
    d.get_by_role("switch", name="Put a key in the link").click()
    d.get_by_label("API key", exact=False).first.fill(KEY)
    r.shot("copy-key")
    width = page.evaluate("document.documentElement.scrollWidth")
    assert width <= 390, f"the copy dialog scrolls sideways at 390 px ({width})"
    ctx.close()
    ok("at phone width the link view and the copy dialog fit")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", default="http://127.0.0.1:19021")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    base, hub = a.base.rstrip("/"), a.hub.rstrip("/")
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            ctx = browser.new_context(viewport={"width": 1280, "height": 860}, device_scale_factor=2, locale="en-US")
            ctx.grant_permissions(["clipboard-read", "clipboard-write"])
            page = ctx.new_page()
            page.set_default_timeout(10000)
            r = Run(page, base, hub, a.token, a.shots)
            r.sign_in()
            open_link(r)
            refused(r)
            pasted_cindy(r)
            copy_link(r)
            ctx.close()
            phone(browser, base, hub, a.token, a.shots)
        except Exception as e:
            print("FAIL", e, flush=True)
            sys.exit(1)
        finally:
            browser.close()
    print("PASS web provider link e2e")


if __name__ == "__main__":
    main()
