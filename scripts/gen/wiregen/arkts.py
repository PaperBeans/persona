"""ArkTS: HarmonyOS accepts a schema's plain TypeScript subset (interfaces, string enums and their ordered values), so
its output is the TypeScript generator's, without what ArkTS lacks (an ID's
brand, an intersection type)."""

from __future__ import annotations

from .model import Def
from .ts import gen_ts


def gen_arkts(defs: dict[str, Def], header: list[str] | None = None, *, defects: str = "ExtensionWire",
              modules: dict[str, str] | None = None) -> str:
    return gen_ts(defs, header=header, defects=defects, flavor="arkts", modules=modules)
