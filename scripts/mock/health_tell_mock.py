"""Synthetic receipt-only Tell Health route; no conversation or model execution (#14715)."""
import os
import re
import threading
import uuid

LOCK = threading.RLock()
RECEIPTS = {}
ENABLED = True


def reset():
    with LOCK:
        RECEIPTS.clear()


def tell(handler, request):
    if not ENABLED:
        handler.send_json({"error": "Tell Health is unavailable"}, 404)
        return
    body = request.json()
    if not isinstance(body, dict):
        handler.send_json({"error": "An object is required"}, 400)
        return
    client_id, text = body.get("client_id"), body.get("text")
    if not isinstance(client_id, str) or not re.fullmatch(r"[A-Za-z0-9._:-]{8,128}", client_id):
        handler.send_json({"error": "A valid client_id is required"}, 400)
        return
    if not isinstance(text, str) or not text.strip() or len(text.encode("utf-8")) > 8192:
        handler.send_json({"error": "Text must contain 1–8192 bytes"}, 400)
        return
    with LOCK:
        if client_id in RECEIPTS:
            handler.send_json(dict(RECEIPTS[client_id], duplicate=True))
        else:
            receipt = {"id": "tell_" + uuid.uuid4().hex[:16], "status": "received"}
            RECEIPTS[client_id] = receipt
            handler.send_json(receipt, 202)


def register(domain):
    global ENABLED
    ENABLED = os.environ.get("MOCK_HEALTH_TELL") != "off"
    reset()
    domain.help("MOCK_HEALTH_TELL=off  omit Tell Health and refuse its route (#14715)")
    domain.feature("health_tell", when=lambda: ENABLED)
    domain.route("POST", "/api/v1/assistant/health/tell", tell)
    domain.on_reset(reset)
