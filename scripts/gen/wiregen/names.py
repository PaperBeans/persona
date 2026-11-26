"""Names and comments shared by every language's generator."""

from __future__ import annotations

import re


INITIALISMS = {"id": "ID", "url": "URL", "api": "API", "http": "HTTP", "rpc": "RPC", "ui": "UI", "tz": "TZ",
               "os": "OS", "cli": "CLI", "json": "JSON"}


def camel(s: str) -> str:
    return "".join(INITIALISMS.get(p, p[:1].upper() + p[1:]) for p in re.split(r"[_.\-]", s) if p)


def go_field(s: str) -> str:
    return "".join(INITIALISMS.get(p, p[:1].upper() + p[1:]) for p in s.split("_"))


def lower_camel(s: str) -> str:
    parts = re.split(r"[_.\-]", s)
    return parts[0] + "".join(p[:1].upper() + p[1:] for p in parts[1:])


def plural(s: str) -> str:
    if s.endswith("y") and not s.endswith("ey"):
        return s[:-1] + "ies"
    if s.endswith("s"):
        return s + "es"
    return s + "s"


def upper_snake(s: str) -> str:
    s = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", s)
    return re.sub(r"[^A-Za-z0-9]+", "_", s).upper()


def named(name: str, doc: str) -> str:
    """A Go doc comment starting with the name: "View: a view…"."""
    if not doc:
        return ""
    if len(doc) > 1 and doc[0].isupper() and doc[1].islower():
        doc = doc[0].lower() + doc[1:]
    return f"{name}: {doc}"


def comment(prefix: str, text: str, width: int = 118) -> list[str]:
    if not text:
        return []
    words, lines, cur = text.split(), [], ""
    for w in words:
        if cur and len(prefix) + len(cur) + 1 + len(w) > width:
            lines.append(prefix + cur)
            cur = w
        else:
            cur = f"{cur} {w}" if cur else w
    if cur:
        lines.append(prefix + cur)
    return lines



def sentence(text: str) -> str:
    """text ending in a full stop, as a reason written into a doc comment."""
    text = text.strip()
    return text if text.endswith((".", "!", "?")) else text + "."
