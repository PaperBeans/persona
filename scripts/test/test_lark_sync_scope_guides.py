"""Keep every translated manual setup guide aligned with the sync authorization packs."""
import json
from pathlib import Path
import re
import unittest
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]


class LarkSyncScopeGuidesTest(unittest.TestCase):
    def test_all_guides_include_every_requested_sync_scope(self):
        source = (ROOT / "server/internal/larksyncwire/types.go").read_text()
        packs = re.findall(r"var Scopes(?:Docs|FullExtra) = \[\]string\{(.*?)\n\}", source, re.S)
        self.assertEqual(len(packs), 2)
        scopes = set(re.findall(r'"([a-z_]+(?::[a-z0-9_.]+)*)"', "\n".join(packs)))
        catalog = json.loads((ROOT / "apple/Shared/Localization/Localizable.xcstrings").read_text())
        apple = catalog["strings"]["Knowledge sync permissions [Lark bot setup]"]["localizations"]
        for lang, folder in [("en", "values"), ("zh-Hans", "values-zh"), ("ja", "values-ja")]:
            web = json.loads((ROOT / f"web/src/i18n/locales/{lang}/settings-feishubot.json").read_text())
            android = ET.parse(ROOT / f"android/feature/settings/src/main/res/{folder}/strings_feishu_bot.xml")
            guides = {
                "web": web["step_sync_permissions"],
                "apple": apple[lang]["stringUnit"]["value"],
                "android": android.find(".//string[@name='settings_feishu_bot_step_sync_permissions']").text,
            }
            for platform, guide in guides.items():
                with self.subTest(platform=platform, lang=lang):
                    actual = set(re.findall(r"\b(?:offline_access|[a-z_]+(?::[a-z0-9_.]+)+)\b", guide))
                    self.assertEqual(scopes, actual)

    def test_setup_screens_render_the_sync_permission_step(self):
        for file, key in [
            ("web/src/views/settings/FeishuBot.tsx", 't("settings-feishubot.step_sync_permissions")'),
            ("apple/Persona/Views/Settings/FeishuBotView.swift", 'step(3, "Knowledge sync permissions [Lark bot setup]")'),
            ("android/feature/settings/src/main/kotlin/persona/feature/settings/FeishuBotScreen.kt", "R.string.settings_feishu_bot_step_sync_permissions"),
        ]:
            with self.subTest(platform=file):
                self.assertIn(key, (ROOT / file).read_text())


if __name__ == "__main__":
    unittest.main()
