#!/usr/bin/env python3
"""Where the assistant's tokens go: model usage per round kind, from its Claude Code transcripts (#6878).

    scripts/diag/assistant_token_report.py [--since 2026-11-01] [--json] [path ...]

A round starts with a message typed into a conversation: the owner's, or one Persona sends (a supervision event, a
follow-up or scheduled run, a task notice, another conversation's message...). Every model call until the next such
message counts for that round. Paths are transcript files or directories searched for *.jsonl; by default the
assistant directory's projects under ~/.claude*/projects and $CLAUDE_CONFIG_DIR/projects.

It also sizes each supervision event's text and the part of it that is the general handling manual (#5665).

Prints counts and sizes only, never message content, so the output can be pasted into an issue.
"""
import argparse
import collections
import glob
import json
import os
import re
import sys

USAGE_FIELDS = ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens")

# The leading <persona-context> blocks a message can start with (snapshot, message source, forwarded text).
LEAD_CONTEXT = re.compile(r"(?s)^\s*<persona-context\b([^>]*)>(.*?)</persona-context>\s*")
SOURCE_KIND = re.compile(r'\bkind="message-source"')

# First lines of the messages Persona types in (server/internal/prompts/injected.go), for transcripts written before
# messages carried their source. They match the Chinese prompts the hub sends, so they stay in Chinese.
FIRST_LINE_KINDS = [
    ("supervision", re.compile(r"^📋 督办：")),
    ("followup", re.compile(r"^⏰（Persona 叫你接着做）")),  # limit_continue: the usage limit reset
    ("wake", re.compile(r"^⏰（Persona 叫醒你")),
    ("followup", re.compile(r"^⏰ ")),
    ("ask_later", re.compile(r"^🔔 你 .+ 用 ask_user 问过用户")),
    ("task_notice", re.compile(r"^🔔 任务「")),
    ("inbox", re.compile(r"^📥 收件箱事件")),
    ("system", re.compile(r"^⚠️ 运行你的 node 刚重启过")),  # lost_tasks
    ("conversation", re.compile(r"^这条消息不是用户本人发的，是 Persona 的另一段对话")),
]
# A message relayed from another conversation (server/internal/prompts/assistant/relay.md) opens with a context block
# that names the sending conversation twice; prompts.ParseRelaySource reads it the same way.
RELAY_TITLE = re.compile(r'\btitle="来自对话「')
RELAY_BODY = re.compile(r"(?s)^这条消息不是用户本人发的，是 Persona 的另一段对话（([^）\s]+)）用 \S+ 发来的.*发回 (\S+?)；")
# Where the general handling manual starts in a supervision event (server/internal/prompts/assistant/supervise.md).
MANUAL_START = re.compile(r"(?m)^怎么处理")


def default_paths():
    # Claude Code's config directory is not always ~/.claude (CLAUDE_CONFIG_DIR; launchers keep their own).
    roots = set(glob.glob(os.path.expanduser("~/.claude*/projects")))
    if os.environ.get("CLAUDE_CONFIG_DIR"):
        roots.add(os.path.join(os.path.expanduser(os.environ["CLAUDE_CONFIG_DIR"]), "projects"))
    # Claude Code names a project directory after its path with "/" and "." as "-": ~/.persona/assistant ends in
    # "-persona-assistant".
    return sorted(d for r in roots for d in glob.glob(os.path.join(r, "*-persona-assistant")))


def transcript_files(paths):
    for p in paths:
        if os.path.isdir(p):
            yield from sorted(glob.glob(os.path.join(p, "**", "*.jsonl"), recursive=True))
        elif os.path.isfile(p):
            yield p


def message_text(entry):
    """The typed text of a user line, or None when the line is a tool result, meta or a compaction summary."""
    if entry.get("type") != "user" or entry.get("isMeta") or entry.get("isCompactSummary"):
        return None
    content = (entry.get("message") or {}).get("content")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        if any(isinstance(b, dict) and b.get("type") == "tool_result" for b in content):
            return None
        texts = [b.get("text", "") for b in content if isinstance(b, dict) and b.get("type") == "text"]
        return "\n".join(texts) if texts else None
    return None


def classify(text):
    """The round kind a typed message starts, and the text after its leading context blocks."""
    rest = text
    while True:
        m = LEAD_CONTEXT.match(rest)
        if not m:
            break
        if SOURCE_KIND.search(m.group(1)):
            try:
                meta = json.loads(m.group(2).strip())
            except ValueError:
                meta = {}
            rest = rest[m.end():]
            if meta.get("author") == "user" or not meta.get("kind"):
                return "owner", rest
            return str(meta["kind"]), rest
        if 'kind="context"' in m.group(1) and RELAY_TITLE.search(m.group(1)):
            body = RELAY_BODY.match(m.group(2).strip())
            if body and body.group(1) == body.group(2):
                return "conversation", rest[m.end():]
        rest = rest[m.end():]
    first = rest.lstrip().split("\n", 1)[0]
    for kind, pattern in FIRST_LINE_KINDS:
        if pattern.search(first):
            return kind, rest
    if rest.lstrip().startswith("<command-name>") or rest.lstrip().startswith("<local-command"):
        return "command", rest
    return "owner", rest


class Report:
    def __init__(self, since=None):
        self.since = since
        self.kinds = collections.defaultdict(lambda: collections.Counter())
        self.conversations = collections.defaultdict(lambda: collections.Counter())
        self.events = []  # (total bytes, manual bytes) per supervision event
        self.sidechain = collections.Counter()
        self.files = 0

    def add_file(self, path):
        self.files += 1
        kind = "before_first_message"
        conv = os.path.splitext(os.path.basename(path))[0]
        # message id -> usage counted so far and whether the response began in the window; Claude Code writes one
        # line per content block. Lines before --since still keep this state and the round kind, but count nothing.
        seen = {}
        with open(path, errors="replace") as f:
            for line in f:
                try:
                    e = json.loads(line)
                except ValueError:
                    continue
                if not isinstance(e, dict):
                    continue
                counted = not self.since or str(e.get("timestamp", "")) >= self.since
                if e.get("type") == "user" and e.get("isCompactSummary"):
                    kind = "after_compaction"
                text = message_text(e)
                if text is not None and not e.get("isSidechain"):
                    kind, rest = classify(text)
                    if not counted:
                        continue
                    self.kinds[kind]["rounds"] += 1
                    self.kinds[kind]["typed_bytes"] += len(text.encode())
                    if kind == "supervision":
                        body = rest.encode()
                        m = MANUAL_START.search(rest)
                        manual = len(rest[m.start():].encode()) if m else 0
                        self.events.append((len(text.encode()), len(body), manual))
                    continue
                if e.get("type") != "assistant":
                    continue
                msg = e.get("message") or {}
                usage = msg.get("usage") or {}
                if not usage:
                    continue
                # A line without a message id is a response of its own: gateways can leave the id empty, and one
                # requestId can then cover several responses (transcript.RequestUsage counts them the same way).
                key = msg.get("id") or ("line", e.get("uuid") or id(e))
                prev = seen.get(key)
                target = self.sidechain if e.get("isSidechain") else self.kinds[kind]
                if prev is None:
                    seen[key] = prev = {k: usage.get(k) or 0 for k in USAGE_FIELDS}
                    prev["counted"] = counted
                    if not counted:
                        continue
                    target["model_calls"] += 1
                    for k in USAGE_FIELDS:
                        target[k] += usage.get(k) or 0
                        if not e.get("isSidechain"):
                            self.conversations[conv][k] += usage.get(k) or 0
                    continue
                # A later line of the same response: count only what grew (output tokens stream in), and only for a
                # response that began in the window.
                for k in USAGE_FIELDS:
                    grown = (usage.get(k) or 0) - prev[k]
                    if grown > 0:
                        prev[k] += grown
                        if not prev["counted"]:
                            continue
                        target[k] += grown
                        if not e.get("isSidechain"):
                            self.conversations[conv][k] += grown

    def summary(self):
        total = collections.Counter()
        for c in self.kinds.values():
            total.update({k: c[k] for k in USAGE_FIELDS + ("model_calls",)})
        kinds = {}
        for kind, c in sorted(self.kinds.items(), key=lambda kv: -sum(kv[1][k] for k in USAGE_FIELDS)):
            row = {k: c[k] for k in ("rounds", "typed_bytes", "model_calls") + USAGE_FIELDS}
            for k in USAGE_FIELDS:
                row[k + "_share"] = round(c[k] / total[k], 3) if total[k] else 0.0
            kinds[kind] = row
        events = sorted(self.events)
        ev = {"count": len(events)}
        if events:
            body = sum(b for _, b, _ in events)
            manual = sum(m for _, _, m in events)
            ev.update({"median_bytes": events[len(events) // 2][0], "max_bytes": events[-1][0],
                       "body_bytes": body, "manual_bytes": manual,
                       "manual_share_of_body": round(manual / body, 3) if body else 0.0,
                       "with_manual": sum(1 for _, _, m in events if m)})
        return {"files": self.files, "conversations": len(self.conversations), "total": dict(total),
                "by_kind": kinds, "supervision_events": ev, "sidechain": dict(self.sidechain)}


def print_text(s):
    print(f"{s['files']} transcript files, {s['conversations']} conversations with model calls")
    head = f"{'round kind':22s} {'rounds':>7s} {'calls':>7s} {'input':>12s} {'cache write':>12s} {'cache read':>14s} {'output':>10s}  share(write/read/out)"
    print(head)
    for kind, r in s["by_kind"].items():
        print(f"{kind:22s} {r['rounds']:7d} {r['model_calls']:7d} {r['input_tokens']:12d} "
              f"{r['cache_creation_input_tokens']:12d} {r['cache_read_input_tokens']:14d} {r['output_tokens']:10d}  "
              f"{r['cache_creation_input_tokens_share']:.0%}/{r['cache_read_input_tokens_share']:.0%}/{r['output_tokens_share']:.0%}")
    t = s["total"]
    print(f"{'total':22s} {'':7s} {t.get('model_calls', 0):7d} {t.get('input_tokens', 0):12d} "
          f"{t.get('cache_creation_input_tokens', 0):12d} {t.get('cache_read_input_tokens', 0):14d} {t.get('output_tokens', 0):10d}")
    if s["sidechain"]:
        sc = s["sidechain"]
        print(f"subagents (sidechain): {sc.get('model_calls', 0)} calls, cache write {sc.get('cache_creation_input_tokens', 0)}, "
              f"cache read {sc.get('cache_read_input_tokens', 0)}, output {sc.get('output_tokens', 0)}")
    ev = s["supervision_events"]
    if ev["count"]:
        print(f"supervision events: {ev['count']}, median {ev['median_bytes']} B, largest {ev['max_bytes']} B; "
              f"{ev['with_manual']} carry the handling manual, {ev['manual_share_of_body']:.0%} of their text after the context blocks")


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("paths", nargs="*", help="transcript files or directories (default: the assistant directory's projects)")
    ap.add_argument("--since", help="only lines at or after this ISO time, e.g. 2026-11-01")
    ap.add_argument("--json", action="store_true", help="print JSON")
    args = ap.parse_args(argv)
    paths = args.paths or default_paths()
    if not paths:
        print("no assistant transcripts found; pass a directory", file=sys.stderr)
        return 1
    r = Report(args.since)
    for path in transcript_files(paths):
        r.add_file(path)
    s = r.summary()
    if args.json:
        json.dump(s, sys.stdout, indent=2)
        print()
    else:
        print_text(s)
    return 0


if __name__ == "__main__":
    sys.exit(main())
