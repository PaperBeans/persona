#!/usr/bin/env python3
"""Check making and editing an avatar from an image in the web app (#2404, #15449) against a mock hub.

Start dedicated mock hubs and Vite dev servers pointing at them, then run:
    PORT=18793 MOCK_LOOK_UPLOADS=demo python3 scripts/mock/mock_hub.py
    PERSONA_HUB_URL=http://127.0.0.1:18793 npx vite --host 127.0.0.1 --port 5179 --strictPort   # in web/
    # Optional old-hub server (MOCK_LOOK_UPLOADS=basic, without look_uploads_states):
    PORT=18794 MOCK_LOOK_UPLOADS=basic python3 scripts/mock/mock_hub.py
    PERSONA_HUB_URL=http://127.0.0.1:18794 npx vite --host 127.0.0.1 --port 5180 --strictPort   # in web/
    python3 scripts/test/web_avatar_upload_e2e.py http://127.0.0.1:5179 [--basic http://127.0.0.1:5180] [--shots <dir>] [--lang en|zh-Hans|ja]

Covers:
- Hub without `look_uploads`: no Upload card, no Edit, no Delete.
- Old hub with `look_uploads` only (`MOCK_LOOK_UPLOADS=basic`, or WS hello without `look_uploads_states`):
  single-image upload sheet unchanged (no Theme color, no Pictures for each state, no Edit button).
- New hub with `look_uploads_states` (`MOCK_LOOK_UPLOADS=demo`, seeding `豆豆` and `Mikan`):
  - Upload card at end of picker; Edit and Delete on uploaded cards (hover / keyboard Tab, always on touch).
  - Default upload sheet: Theme color (with Automatic swatch) and Pictures for each state collapsed.
  - Expanded Theme color (custom color pick) and Pictures for each state (animated state upload with playing crop
    preview, status mark switch toggled, playing state row preview, and animated main upload).
  - Refusal shown as hub words it, upload progress, POST body verified (original animated bytes + crop + states + color).
  - Edit sheet of `Mikan` (u-demo0002): filled from package (playing working dot, mark switches, color), saving only
    what changed via PATCH /api/v1/looks/uploads/{id} and refreshing the list.
  - Settings › Advanced › Appearance: Match Avatar offered and applied for an uploaded avatar with colors.
  - prefers-reduced-motion: animated previews show frame 0 only (data-playing="false").
Wide (1280) and phone width (390) in light and dark; screenshots contain only made-up data.
"""
import argparse
import base64
import json
import os
import struct
import tempfile
import zlib

from playwright.sync_api import expect, sync_playwright


def _png_chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)


def made_up_png(path, w=960, h=640):
    """A landscape picture (sky, sun, hills) without Pillow, so the crop has something to show."""
    rows = []
    for y in range(h):
        row = bytearray(b"\x00")
        for x in range(w):
            if (x - w * 0.62) ** 2 + (y - h * 0.38) ** 2 < (h * 0.16) ** 2:
                px = (255, 214, 92)
            elif y > h * 0.62 + 40 * ((x / w * 6.28) % 3.14 - 1.57) ** 2 / 2.5:
                px = (60 + y * 40 // h, 140 - y * 40 // h, 90)
            else:
                t = y / h
                px = (int(250 - 120 * t), int(150 + 60 * t), int(120 + 120 * t))
            row += bytes(px) + b"\xff"
        rows.append(bytes(row))
    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\n" + _png_chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0))
                + _png_chunk(b"IDAT", zlib.compress(b"".join(rows), 6)) + _png_chunk(b"IEND", b""))
    return path


def made_up_apng(path, w=160, h=120):
    """A 3-frame looping APNG (160x120) without Pillow, so animated upload and live previews can be checked."""
    palette = [(64, 152, 236, 255), (242, 168, 56, 255), (76, 192, 132, 255)]
    out = bytearray(b"\x89PNG\r\n\x1a\n")
    out += _png_chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0))
    out += _png_chunk(b"acTL", struct.pack(">II", len(palette), 0))
    seq = 0
    for idx, accent in enumerate(palette):
        rows = bytearray()
        cx, cy = w * (0.35 + 0.15 * idx), h * 0.5
        for y in range(h):
            rows.append(0)
            for x in range(w):
                if (x - cx) ** 2 + (y - cy) ** 2 < (h * 0.28) ** 2:
                    rows.extend(accent)
                else:
                    rows.extend((38, 52, 78, 255))
        zdata = zlib.compress(bytes(rows), 6)
        out += _png_chunk(b"fcTL", struct.pack(">IIIIIHHBB", seq, w, h, 0, 0, 180, 1000, 0, 0))
        seq += 1
        if idx == 0:
            out += _png_chunk(b"IDAT", zdata)
        else:
            out += _png_chunk(b"fdAT", struct.pack(">I", seq) + zdata)
            seq += 1
    out += _png_chunk(b"IEND", b"")
    with open(path, "wb") as f:
        f.write(bytes(out))
    return path


def png_size(data):
    assert data[:8] == b"\x89PNG\r\n\x1a\n", "the upload is not a PNG"
    return struct.unpack(">II", data[16:24])


# The uploaded looks MOCK_LOOK_UPLOADS=demo seeds (scripts/mock/mock_hub.py).
DEMO = "豆豆"
DEMO_STATES = "Mikan"

WORDS = {
    "en": {
        "upload": "Upload an image as an avatar", "dialog": "New avatar from an image", "edit_dialog": "Edit avatar",
        "name": "Name", "subtitle": "Description (optional)", "send": "Upload", "save": "Save",
        "edit": "Edit {}", "delete": "Delete {}", "confirm": "Delete", "cancel": "Cancel", "back": "Back",
        "keep_current": "Keep current",
        "mochi": "Mochi, the default avatar", "zoom": "Zoom", "avatar": "Avatar", "making": "Making the avatar…",
        "color_custom": "Choose color", "apply_crop": "Done", "match_avatar": "Match avatar", "theme_color": "Theme color",
    },
    "zh-Hans": {
        "upload": "上传图片做形象", "dialog": "用图片做形象", "edit_dialog": "编辑形象",
        "name": "名字", "subtitle": "描述（可选）", "send": "上传", "save": "保存",
        "edit": "编辑「{}」", "delete": "删除「{}」", "confirm": "删除", "cancel": "取消", "back": "返回",
        "keep_current": "保留原图",
        "mochi": "团子，默认形象", "zoom": "缩放", "avatar": "形象", "making": "正在生成形象…",
        "color_custom": "自选颜色", "apply_crop": "完成", "match_avatar": "跟随形象", "theme_color": "主题色",
    },
    "ja": {
        "upload": "画像をアップロードしてアバターにする", "dialog": "画像からアバターを作成", "edit_dialog": "アバターを編集",
        "name": "名前", "subtitle": "説明（任意）", "send": "アップロード", "save": "保存",
        "edit": "「{}」を編集", "delete": "「{}」を削除", "confirm": "削除", "cancel": "キャンセル", "back": "戻る",
        "keep_current": "元の画像を維持",
        "mochi": "だんご（デフォルトのアバター）", "zoom": "ズーム", "avatar": "アバター", "making": "アバターを作成しています…",
        "color_custom": "色を選ぶ", "apply_crop": "完了", "match_avatar": "アバターに合わせる", "theme_color": "テーマカラー",
    },
}


def _new_context(browser, width, theme, lang, reduced_motion=None):
    phone = width < 640
    kwargs = dict(
        viewport={"width": width, "height": 900 if not phone else 844},
        color_scheme=theme,
        has_touch=phone,
        is_mobile=phone,
        device_scale_factor=2,
    )
    if reduced_motion:
        kwargs["reduced_motion"] = reduced_motion
    context = browser.new_context(**kwargs)
    context.add_init_script(f"localStorage.setItem('persona.theme', {theme!r}); localStorage.setItem('persona.language', {lang!r})")
    return context


def _filter_hello_features(context, drop):
    def socket(route):
        server = route.connect_to_server()

        def message(raw):
            data = json.loads(raw)
            if data.get("type") == "hello":
                data["features"] = [f for f in data.get("features", []) if f not in drop]
            route.send(json.dumps(data))

        server.on_message(message)

    context.route_web_socket("**/api/v1/ws?*", socket)


def check_no_uploads(browser, base, lang):
    """A hub without look_uploads: no Upload card, no Edit and no Delete."""
    w = WORDS[lang]
    context = _new_context(browser, 1280, "light", lang)
    _filter_hello_features(context, {"look_uploads", "look_uploads_states"})
    try:
        page = context.new_page()
        page.set_default_timeout(15000)
        page.goto(base + "/settings?tab=avatar&token=mock")
        picker = page.locator('section[data-settings-group="avatar"]').get_by_role("radiogroup", name=w["avatar"])
        expect(picker.get_by_role("radio", name=w["mochi"], exact=True)).to_be_visible()
        expect(picker.get_by_role("radio", name=DEMO, exact=True)).to_be_visible()
        expect(picker.get_by_role("button", name=w["upload"], exact=True)).to_have_count(0)
        expect(picker.get_by_role("button", name=w["edit"].format(DEMO), exact=True)).to_have_count(0)
        expect(picker.get_by_role("button", name=w["delete"].format(DEMO), exact=True)).to_have_count(0)
        print("PASS avatar upload hidden without look_uploads", flush=True)
    finally:
        context.close()


def check_basic_old_hub(browser, base, basic_base, width, theme, lang, image, shots):
    """Old hub with look_uploads only (MOCK_LOOK_UPLOADS=basic): legacy single-image sheet, no states/color/Edit."""
    w = WORDS[lang]
    phone = width < 640
    target = basic_base or base
    context = _new_context(browser, width, theme, lang)
    if not basic_base:
        _filter_hello_features(context, {"look_uploads_states"})
    name = f"Old Hub {width} {theme}"
    try:
        page = context.new_page()
        page.set_default_timeout(15000)
        errors = []
        page.on("pageerror", lambda err: errors.append(str(err)))
        page.goto(target + "/settings?tab=avatar&token=mock")
        picker = page.locator('section[data-settings-group="avatar"]').get_by_role("radiogroup", name=w["avatar"])
        upload = picker.get_by_role("button", name=w["upload"], exact=True)
        expect(upload).to_be_visible()
        if not basic_base:
            expect(picker.get_by_role("radio", name=DEMO, exact=True)).to_be_visible()
            expect(picker.get_by_role("button", name=w["delete"].format(DEMO), exact=True)).to_have_count(1)
            expect(picker.get_by_role("button", name=w["edit"].format(DEMO), exact=True)).to_have_count(0)

        upload.click()
        dialog = page.get_by_role("dialog", name=w["dialog"])
        expect(dialog).to_be_visible()
        dialog.locator('[data-testid="look-upload-file"]').set_input_files(image)
        expect(dialog.locator('[data-testid="look-upload-crop"]')).to_be_visible()
        page.wait_for_function("() => { const c = document.querySelector('[data-testid=look-upload-crop] canvas'); if (!c || !c.width) return false; const d = c.getContext('2d').getImageData(c.width / 2, c.height / 2, 1, 1).data; return d[3] > 0; }")
        dialog.get_by_label(w["name"], exact=True).fill(name)
        expect(dialog.locator('[data-testid="look-color-toggle"]')).to_have_count(0)
        expect(dialog.locator('[data-testid="look-states-toggle"]')).to_have_count(0)
        if shots:
            page.wait_for_timeout(300)
            (page if phone else dialog).screenshot(path=os.path.join(shots, f"old-hub-upload-{width}-{theme}.png"))

        # Upload on the basic hub and confirm the uploaded card has Delete and NO Edit, then clean up.
        if basic_base:
            dialog.get_by_role("button", name=w["send"], exact=True).click()
            expect(dialog).to_have_count(0)
            made = picker.get_by_role("radio", name=name, exact=True)
            expect(made).to_be_visible()
            expect(picker.get_by_role("button", name=w["delete"].format(name), exact=True)).to_have_count(1)
            expect(picker.get_by_role("button", name=w["edit"].format(name), exact=True)).to_have_count(0)
            trash = picker.get_by_role("button", name=w["delete"].format(name), exact=True)
            if not phone:
                made.hover()
            trash.click()
            confirm = page.get_by_role("dialog")
            expect(confirm).to_be_visible()
            confirm.get_by_role("button", name=w["confirm"], exact=True).click()
            expect(page.get_by_role("dialog")).to_have_count(0)
            expect(made).to_have_count(0)
        assert not errors, errors
        print(f"PASS old hub single-image sheet {width}px {theme}", flush=True)
    finally:
        context.close()


def check_reduced_motion(browser, base, lang, apng):
    """prefers-reduced-motion: animated previews show frame 0 on canvas instead of playing <img>."""
    w = WORDS[lang]
    context = _new_context(browser, 1280, "light", lang, reduced_motion="reduce")
    try:
        page = context.new_page()
        page.set_default_timeout(15000)
        page.goto(base + "/settings?tab=avatar&token=mock")
        picker = page.locator('section[data-settings-group="avatar"]').get_by_role("radiogroup", name=w["avatar"])
        picker.get_by_role("button", name=w["upload"], exact=True).click()
        dialog = page.get_by_role("dialog", name=w["dialog"])
        expect(dialog).to_be_visible()
        dialog.locator('[data-testid="look-upload-file"]').set_input_files(apng)
        expect(dialog.locator('[data-testid="look-upload-crop"]')).to_be_visible()
        expect(dialog.locator('[data-testid="look-upload-animated"]')).to_have_count(0)
        expect(dialog.locator('[data-testid="look-upload-crop"] canvas')).to_have_count(1)
        print("PASS reduced motion keeps animated previews on frame 0", flush=True)
    finally:
        context.close()


def check(browser, base, width, theme, lang, image, apng, shots):
    w = WORDS[lang]
    phone = width < 640
    context = _new_context(browser, width, theme, lang)
    page = context.new_page()
    page.set_default_timeout(15000)
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    name = f"Sunset {width} {theme}"
    apng_bytes = open(apng, "rb").read()
    try:
        page.goto(base + "/settings?tab=avatar&token=mock")
        group = page.locator('section[data-settings-group="avatar"]')
        picker = group.get_by_role("radiogroup", name=w["avatar"])
        upload = picker.get_by_role("button", name=w["upload"], exact=True)
        expect(upload).to_be_visible()
        demo = picker.get_by_role("radio", name=DEMO, exact=True)
        expect(demo).to_be_visible()
        # Upload is the last card, in the same grid as the looks and as big as one
        assert picker.evaluate("g => g.lastElementChild.getAttribute('aria-label')") == w["upload"]
        card = demo.bounding_box()
        box = upload.bounding_box()
        assert abs(card["height"] - box["height"]) < 1 and abs(card["width"] - box["width"]) < 1, (card, box)
        edit_btn = picker.get_by_role("button", name=w["edit"].format(DEMO), exact=True)
        trash = picker.get_by_role("button", name=w["delete"].format(DEMO), exact=True)
        reveal = trash.locator("xpath=..")
        if phone:
            expect(reveal).to_have_css("opacity", "1")
        else:
            expect(reveal).to_have_css("opacity", "0")
            demo.focus()
            page.keyboard.press("Tab")
            expect(edit_btn).to_be_focused()
            expect(reveal).to_have_css("opacity", "1")
            page.keyboard.press("Tab")
            expect(trash).to_be_focused()
            expect(reveal).to_have_css("opacity", "1")
            page.locator("body").evaluate("b => document.activeElement.blur()")
            demo.hover()
            expect(reveal).to_have_css("opacity", "1")
            if shots:
                group.screenshot(path=os.path.join(shots, f"picker-hover-{width}-{theme}.png"))
            page.mouse.move(0, 0)
            page.locator("body").evaluate("b => document.activeElement.blur()")

        # 1. Default upload sheet (collapsed Theme color + Pictures for each state)
        upload.click()
        dialog = page.get_by_role("dialog", name=w["dialog"])
        expect(dialog).to_be_visible()
        send = dialog.get_by_role("button", name=w["send"], exact=True)
        expect(send).to_be_disabled()
        color_toggle = dialog.locator('[data-testid="look-color-toggle"]')
        states_toggle = dialog.locator('[data-testid="look-states-toggle"]')
        expect(color_toggle).to_have_attribute("aria-expanded", "false")
        expect(states_toggle).to_have_attribute("aria-expanded", "false")

        dialog.locator('[data-testid="look-upload-file"]').set_input_files(image)
        crop = dialog.locator('[data-testid="look-upload-crop"]')
        expect(crop).to_be_visible()
        page.wait_for_function("() => { const c = document.querySelector('[data-testid=look-upload-crop] canvas'); if (!c || !c.width) return false; const d = c.getContext('2d').getImageData(c.width / 2, c.height / 2, 1, 1).data; return d[3] > 0; }")
        # Automatic swatch appears on collapsed Theme color row
        expect(dialog.locator('[data-testid="look-color-summary-swatch"]')).to_be_visible()

        zoom = dialog.get_by_role("slider", name=w["zoom"])
        zoom.fill("1.6")
        assert abs(float(zoom.input_value()) - 1.6) < 0.01
        pixel = "() => Array.from(document.querySelector('[data-testid=look-upload-crop] canvas').getContext('2d').getImageData(10, 10, 1, 1).data).join()"
        before = page.evaluate(pixel)
        r = crop.bounding_box()
        page.mouse.move(r["x"] + r["width"] / 2, r["y"] + r["height"] / 2)
        page.mouse.down()
        page.mouse.move(r["x"] + r["width"] / 2 - 60, r["y"] + r["height"] / 2 + 40, steps=6)
        page.mouse.up()
        page.wait_for_function(f"was => ({pixel})() !== was", arg=before)
        if not phone:
            page.mouse.move(r["x"] + r["width"] * 0.3, r["y"] + r["height"] * 0.3)
            page.mouse.wheel(0, -120)
            page.wait_for_function("() => Number(document.querySelector('[role=dialog] input[type=range]').value) > 1.6")
        before_z = float(zoom.input_value())
        crop.focus()
        page.keyboard.press("+")
        page.keyboard.press("ArrowLeft")
        page.wait_for_function("z => Number(document.querySelector('[role=dialog] input[type=range]').value) > z", arg=before_z)
        dialog.get_by_label(w["name"], exact=True).fill(f"  {name}  ")
        dialog.get_by_label(w["subtitle"], exact=True).fill("Warm evening colors")
        expect(send).to_be_enabled()
        if shots:
            page.wait_for_timeout(300)
            (page if phone else dialog).screenshot(path=os.path.join(shots, f"upload-default-{width}-{theme}.png"))

        # 2. Expand Theme color & Pictures for each state, upload an animated APNG for working state, pick a color
        color_toggle.click()
        expect(color_toggle).to_have_attribute("aria-expanded", "true")
        expect(dialog.locator('[data-testid="look-auto-swatch"]')).to_be_visible()
        dialog.get_by_role("radio", name=w["color_custom"], exact=True).click()
        dialog.locator('[data-testid="look-color-input"]').fill("#3a7bd5")

        states_toggle.click()
        expect(states_toggle).to_have_attribute("aria-expanded", "true")
        expect(dialog.locator("[data-state-row]")).to_have_count(5)
        dialog.locator('[data-testid="look-upload-state-file-working"]').set_input_files(apng)
        # State crop sub-view replaces the dialog title ("Crop — Working") and shows a playing animated preview
        crop_dlg = page.get_by_role("dialog")
        expect(crop_dlg.locator('[data-testid="look-upload-animated"]')).to_be_visible()
        mark_switch = crop_dlg.get_by_role("switch")
        expect(mark_switch).to_have_attribute("aria-checked", "true")
        mark_switch.click()
        expect(mark_switch).to_have_attribute("aria-checked", "false")
        crop_dlg.get_by_role("button", name=w["apply_crop"], exact=True).click()
        expect(dialog).to_be_visible()
        expect(dialog.locator('[data-state-preview="working"]')).to_have_attribute("data-playing", "true")
        if shots:
            page.wait_for_timeout(350)
            # Scroll dialog body down so both color picker and state rows are clearly visible in the shot
            dialog.locator("[data-dialog-body]").evaluate("el => { el.scrollTop = el.scrollHeight; }")
            page.wait_for_timeout(150)
            (page if phone else dialog).screenshot(path=os.path.join(shots, f"upload-expanded-{width}-{theme}.png"))

        # 3. Refusal shown as it is, then POST with states + color verified
        refusal = "There are already 50 uploaded avatars. Delete one first."
        state = {"refused": False, "body": None, "held": None}

        def route_post(rt):
            if rt.request.method != "POST":
                return rt.continue_()
            if not state["refused"]:
                state["refused"] = True
                return rt.fulfill(status=409, content_type="application/json", body=json.dumps({"error": refusal, "code": "too_many"}))
            state["body"] = json.loads(rt.request.post_data)
            state["held"] = rt

        page.route("**/api/v1/looks/uploads", route_post)
        send.click()
        expect(dialog.get_by_role("alert")).to_have_text(refusal)
        expect(send).to_be_enabled()
        send.click()
        expect(send).to_be_disabled()
        expect(dialog.get_by_text(w["making"], exact=True).or_(dialog.get_by_role("progressbar"))).to_be_visible()
        for _ in range(100):
            if state["held"]:
                break
            page.wait_for_timeout(50)
        assert state["held"], "the upload never reached the hub"
        state["held"].continue_()
        expect(dialog).to_have_count(0)
        page.unroute("**/api/v1/looks/uploads", route_post)

        body = state["body"]
        assert body and body["name"] == name and body["subtitle"] == "Warm evening colors" and body["choose"] is True
        assert body["color"] == "#3A7BD5", body.get("color")
        side = png_size(base64.b64decode(body["image"]))
        assert side[0] == side[1] and 64 <= side[0] <= 1024, side
        wk = body["states"]["working"]
        assert base64.b64decode(wk["image"]) == apng_bytes, "animated state did not send original APNG bytes"
        assert wk["mark"] is False and wk["crop"]["side"] >= 64, wk

        made = picker.get_by_role("radio", name=name, exact=True)
        expect(made).to_have_attribute("aria-checked", "true")
        expect(picker.get_by_role("radio", checked=True)).to_have_count(1)
        expect(made).to_contain_text("Warm evening colors")

        # 4. Match Avatar offered and applied for the newly uploaded avatar (with colors)
        page.goto(base + "/settings?tab=advanced&token=mock")
        tc = page.locator('[data-settings-row="theme-color"]')
        match_btn = tc.get_by_role("radio").filter(has_text=w["match_avatar"])
        expect(match_btn).to_be_visible()
        match_btn.click()
        expect(match_btn).to_have_attribute("aria-checked", "true")
        assert page.evaluate("() => document.documentElement.dataset.accent") == "persona"
        assert page.evaluate("() => !!document.getElementById('persona-accent')?.textContent") is True
        if shots:
            page.wait_for_timeout(300)
            tc.screenshot(path=os.path.join(shots, f"match-avatar-{width}-{theme}.png"))

        # 5. Edit sheet of the demo look (Mikan, u-demo0002) and PATCH with only changed fields
        page.goto(base + "/settings?tab=avatar&token=mock")
        picker = page.locator('section[data-settings-group="avatar"]').get_by_role("radiogroup", name=w["avatar"])
        mikan = picker.get_by_role("radio", name=DEMO_STATES, exact=True)
        expect(mikan).to_be_visible()
        mikan_edit = picker.get_by_role("button", name=w["edit"].format(DEMO_STATES), exact=True)
        if not phone:
            mikan.hover()
        mikan_edit.click()
        edit_dlg = page.get_by_role("dialog", name=w["edit_dialog"])
        expect(edit_dlg).to_be_visible()
        save_btn = edit_dlg.get_by_role("button", name=w["save"], exact=True)
        expect(save_btn).to_be_disabled()
        expect(edit_dlg.locator('[data-state-preview="working"]')).to_have_attribute("data-playing", "true")
        working_sw = edit_dlg.locator('[data-state-row="working"]').get_by_role("switch")
        offline_sw = edit_dlg.locator('[data-state-row="offline"]').get_by_role("switch")
        expect(working_sw).to_have_attribute("aria-checked", "true")
        expect(offline_sw).to_have_attribute("aria-checked", "false")
        if shots:
            page.wait_for_timeout(350)
            (page if phone else edit_dlg).screenshot(path=os.path.join(shots, f"edit-demo-{width}-{theme}.png"))

        # Toggle working mark off and save: verify PATCH sends ONLY the changed state mark (and restore after)
        patches = []

        def route_patch(rt):
            if rt.request.method == "PATCH":
                patches.append(json.loads(rt.request.post_data))
            rt.continue_()

        page.route("**/api/v1/looks/uploads/*", route_patch)
        working_sw.click()
        expect(save_btn).to_be_enabled()
        save_btn.click()
        expect(edit_dlg).to_have_count(0)
        assert patches == [{"states": {"working": {"mark": False}}}], patches

        # Re-open Edit on Mikan to confirm refreshed state and restore working mark=true for subsequent runs
        if not phone:
            mikan.hover()
        mikan_edit.click()
        expect(edit_dlg).to_be_visible()
        working_sw = edit_dlg.locator('[data-state-row="working"]').get_by_role("switch")
        expect(working_sw).to_have_attribute("aria-checked", "false")
        working_sw.click()
        edit_dlg.get_by_role("button", name=w["save"], exact=True).click()
        expect(edit_dlg).to_have_count(0)
        page.unroute("**/api/v1/looks/uploads/*", route_patch)

        # 6. Delete the newly uploaded look and confirm default returns
        made = picker.get_by_role("radio", name=name, exact=True)
        made_trash = picker.get_by_role("button", name=w["delete"].format(name), exact=True)
        if not phone:
            made.hover()
        made_trash.click()
        confirm = page.get_by_role("dialog")
        expect(confirm).to_be_visible()
        confirm.get_by_role("button", name=w["confirm"], exact=True).click()
        expect(page.get_by_role("dialog")).to_have_count(0)
        expect(made).to_have_count(0)
        expect(picker.get_by_role("radio", name=w["mochi"], exact=True)).to_have_attribute("aria-checked", "true")

        # 7. The profile page has the same picker
        page.goto(base + "/assistant?token=mock")
        profile = page.get_by_role("radiogroup", name=w["avatar"])
        expect(profile.get_by_role("button", name=w["upload"], exact=True)).to_be_visible()
        assert not errors, errors
        print(f"PASS avatar upload+states+edit {width}px {theme}", flush=True)
    except Exception:
        print(f"FAIL avatar upload {width}px {theme}; path={page.url.removeprefix(base)}; page errors={errors}", flush=True)
        if shots:
            page.screenshot(path=os.path.join(shots, f"failure-{width}-{theme}.png"))
        raise
    finally:
        context.close()


def _dispatch_file_drag(locator, event_types, file_path=None, mime="image/png"):
    """Simulate native OS file drag events on a locator using a real DataTransfer + File."""
    b64 = base64.b64encode(open(file_path, "rb").read()).decode("ascii") if file_path else None
    name = os.path.basename(file_path) if file_path else "drop.png"
    return locator.evaluate(
        """(el, { eventTypes, b64, name, mime }) => {
            const dt = new DataTransfer();
            const raw = b64 ? Uint8Array.from(atob(b64), c => c.charCodeAt(0)) : new Uint8Array([1, 2, 3]);
            dt.items.add(new File([raw], name, { type: mime }));
            const results = [];
            for (const type of eventTypes) {
                const ev = new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt });
                el.dispatchEvent(ev);
                results.push({ type, defaultPrevented: ev.defaultPrevented, dropEffect: dt.dropEffect });
            }
            return results;
        }""",
        {"eventTypes": event_types, "b64": b64, "name": name, "mime": mime},
    )


def check_drops(browser, base, width, theme, lang, image, apng, shots):
    """Per-slot drag-and-drop (#15612): picker cards, main slot, state rows, state crop sub-view, and near-miss guard."""
    w = WORDS[lang]
    phone = width < 640
    context = _new_context(browser, width, theme, lang)
    page = context.new_page()
    page.set_default_timeout(15000)
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    try:
        page.goto(base + "/settings?tab=avatar&token=mock")
        group = page.locator('section[data-settings-group="avatar"]')
        picker = group.get_by_role("radiogroup", name=w["avatar"])
        upload_card = picker.get_by_role("button", name=w["upload"], exact=True)
        mikan_card = picker.get_by_role("radio", name=DEMO_STATES, exact=True)
        mochi_card = picker.get_by_role("radio", name=w["mochi"], exact=True)
        expect(upload_card).to_be_visible()
        expect(mikan_card).to_be_visible()

        # 1. Built-in card ignores drops and never highlights; grid near-miss guard prevents navigation
        res = _dispatch_file_drag(mochi_card, ["dragover", "drop"], image)
        expect(mochi_card).not_to_have_attribute("data-drop-over", "true")
        assert all(r["defaultPrevented"] for r in res), res
        expect(page.get_by_role("dialog")).to_have_count(0)

        # 2. Picker Upload card drag-over highlight -> drop opens Upload dialog with image ready to crop
        _dispatch_file_drag(upload_card, ["dragenter", "dragover"], image)
        expect(upload_card).to_have_attribute("data-drop-over", "true")
        if shots:
            page.wait_for_timeout(180)
            group.screenshot(path=os.path.join(shots, f"drop-picker-upload-over-{width}-{theme}.png"))
        _dispatch_file_drag(upload_card, ["drop"], image)
        expect(upload_card).not_to_have_attribute("data-drop-over", "true")
        dlg = page.get_by_role("dialog", name=w["dialog"])
        expect(dlg).to_be_visible()
        expect(dlg.locator('[data-testid="look-upload-crop"]')).to_be_visible()
        page.wait_for_function("() => { const c = document.querySelector('[data-testid=look-upload-crop] canvas'); if (!c || !c.width) return false; const d = c.getContext('2d').getImageData(c.width / 2, c.height / 2, 1, 1).data; return d[3] > 0; }")
        if shots:
            page.wait_for_timeout(200)
            (page if phone else dlg).screenshot(path=os.path.join(shots, f"drop-picker-opened-upload-{width}-{theme}.png"))
        dlg.get_by_role("button", name=w["cancel"], exact=True).click()
        expect(page.get_by_role("dialog")).to_have_count(0)

        # 3. Picker uploaded card (Mikan) drag-over highlight -> drop opens Edit dialog with new main picture & Keep current
        _dispatch_file_drag(mikan_card, ["dragenter", "dragover"], image)
        expect(mikan_card).to_have_attribute("data-drop-over", "true")
        if shots:
            page.wait_for_timeout(180)
            group.screenshot(path=os.path.join(shots, f"drop-picker-uploaded-over-{width}-{theme}.png"))
        _dispatch_file_drag(mikan_card, ["drop"], image)
        expect(mikan_card).not_to_have_attribute("data-drop-over", "true")
        edit_dlg = page.get_by_role("dialog", name=w["edit_dialog"])
        expect(edit_dlg).to_be_visible()
        expect(edit_dlg.locator('[data-testid="look-upload-crop"]')).to_be_visible()
        keep_btn = edit_dlg.get_by_role("button", name=w["keep_current"], exact=True)
        expect(keep_btn).to_be_visible()
        if shots:
            page.wait_for_timeout(200)
            (page if phone else edit_dlg).screenshot(path=os.path.join(shots, f"drop-picker-opened-edit-{width}-{theme}.png"))
        keep_btn.click()
        expect(edit_dlg.locator('[data-testid="look-edit-preview"]')).to_be_visible()
        if shots:
            edit_dlg.locator("[data-dialog-body]").evaluate("el => { el.scrollTop = el.scrollHeight; }")
            page.wait_for_timeout(200)
            (page if phone else edit_dlg).screenshot(path=os.path.join(shots, f"drop-edit-states-{width}-{theme}.png"))
        edit_dlg.get_by_role("button", name=w["cancel"], exact=True).click()
        expect(page.get_by_role("dialog")).to_have_count(0)

        # 4. Upload dialog: near-miss guard, main drop-over, states open, state row drop-over, state crop view + replacement
        upload_card.click()
        dlg = page.get_by_role("dialog", name=w["dialog"])
        expect(dlg).to_be_visible()

        # Near-miss drop on dialog content root changes nothing and is defaultPrevented
        near = _dispatch_file_drag(dlg.locator("[data-dialog-body] > div").first, ["dragover", "drop"], image)
        assert all(r["defaultPrevented"] for r in near), near
        expect(dlg.locator('[data-testid="look-upload-crop"]')).to_have_count(0)

        # Drag-over highlight on empty main slot
        main_zone = dlg.locator('[data-testid="look-upload-drop"]')
        _dispatch_file_drag(main_zone, ["dragenter", "dragover"], image)
        expect(main_zone).to_have_attribute("data-drop-over", "true")
        if shots:
            page.wait_for_timeout(180)
            (page if phone else dlg).screenshot(path=os.path.join(shots, f"drop-main-over-{width}-{theme}.png"))
        _dispatch_file_drag(main_zone, ["drop"], image)
        expect(dlg.locator('[data-testid="look-upload-crop"]')).to_be_visible()
        page.wait_for_function("() => { const c = document.querySelector('[data-testid=look-upload-crop] canvas'); if (!c || !c.width) return false; const d = c.getContext('2d').getImageData(c.width / 2, c.height / 2, 1, 1).data; return d[3] > 0; }")

        # Open Pictures for each state
        dlg.locator('[data-testid="look-states-toggle"]').click()
        expect(dlg.locator("[data-state-row]")).to_have_count(5)
        page.wait_for_timeout(350)
        dlg.locator("[data-dialog-body]").evaluate("el => { el.scrollTop = el.scrollHeight; }")
        if shots:
            page.wait_for_timeout(180)
            (page if phone else dlg).screenshot(path=os.path.join(shots, f"drop-upload-states-open-{width}-{theme}.png"))

        # Drag-over highlight on Working state row ONLY
        working_row = dlg.locator('[data-state-row="working"]')
        _dispatch_file_drag(working_row, ["dragenter", "dragover"], apng)
        expect(working_row).to_have_attribute("data-drop-over", "true")
        expect(dlg.locator('[data-state-row="idle"]')).not_to_have_attribute("data-drop-over", "true")
        if shots:
            page.wait_for_timeout(180)
            (page if phone else dlg).screenshot(path=os.path.join(shots, f"drop-state-row-over-{width}-{theme}.png"))

        # Drop animated APNG onto Working row -> opens state crop sub-view playing animation
        _dispatch_file_drag(working_row, ["drop"], apng)
        crop_dlg = page.get_by_role("dialog")
        expect(crop_dlg.locator('[data-testid="look-upload-animated"]')).to_be_visible()
        if shots:
            page.wait_for_timeout(200)
            (page if phone else crop_dlg).screenshot(path=os.path.join(shots, f"drop-state-crop-{width}-{theme}.png"))

        # Drop still PNG onto crop sub-view replaces the state picture cleanly
        state_crop_box = crop_dlg.locator('[data-testid="look-upload-crop"]')
        _dispatch_file_drag(state_crop_box, ["dragenter", "dragover"], image)
        expect(state_crop_box).to_have_attribute("data-drop-over", "true")
        _dispatch_file_drag(state_crop_box, ["drop"], image)
        expect(crop_dlg.locator('[data-testid="look-upload-animated"]')).to_have_count(0)
        expect(crop_dlg.locator('[data-testid="look-upload-crop"] canvas')).to_have_count(1)
        crop_dlg.get_by_role("button", name=w["apply_crop"], exact=True).click()
        expect(dlg).to_be_visible()
        dlg.get_by_role("button", name=w["cancel"], exact=True).click()
        expect(page.get_by_role("dialog")).to_have_count(0)

        assert not errors, errors
        print(f"PASS avatar drop targets {width}px {theme}", flush=True)
    except Exception:
        print(f"FAIL avatar drop targets {width}px {theme}; path={page.url.removeprefix(base)}; page errors={errors}", flush=True)
        if shots:
            page.screenshot(path=os.path.join(shots, f"failure-drops-{width}-{theme}.png"))
        raise
    finally:
        context.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("base")
    parser.add_argument("--basic", help="Optional web URL pointed at MOCK_LOOK_UPLOADS=basic (old hub)")
    parser.add_argument("--shots")
    parser.add_argument("--drops", action="store_true", help="Run only the per-slot drag-and-drop checks and screenshots (#15612)")
    parser.add_argument("--lang", default="en", choices=sorted(WORDS))
    parser.add_argument("--channel", default="chrome", help="Playwright browser channel (default: the installed Chrome)")
    parser.add_argument("--executable", help="Explicit browser executable path (overrides --channel)")
    args = parser.parse_args()
    expect.set_options(timeout=15000)
    if args.shots:
        os.makedirs(args.shots, exist_ok=True)
    base = args.base.rstrip("/")
    basic = args.basic.rstrip("/") if args.basic else None
    with tempfile.TemporaryDirectory(prefix="avatar-upload-") as tmp, sync_playwright() as playwright:
        image = made_up_png(os.path.join(tmp, "sunset.png"))
        apng = made_up_apng(os.path.join(tmp, "wave.png"))
        launch = {"executable_path": args.executable} if args.executable else {"channel": args.channel}
        browser = playwright.chromium.launch(**launch)
        try:
            if args.drops:
                for width in (1280, 390):
                    for theme in ("light", "dark"):
                        check_drops(browser, base, width, theme, args.lang, image, apng, args.shots)
            else:
                check_no_uploads(browser, base, args.lang)
                check_reduced_motion(browser, base, args.lang, apng)
                for width in (1280, 390):
                    for theme in ("light", "dark"):
                        check_basic_old_hub(browser, base, basic, width, theme, args.lang, image, args.shots)
                        check(browser, base, width, theme, args.lang, image, apng, args.shots)
                        check_drops(browser, base, width, theme, args.lang, image, apng, args.shots)
        finally:
            browser.close()
    print("PASS web avatar upload e2e")


if __name__ == "__main__":
    main()
