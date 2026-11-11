#!/usr/bin/env python3
"""Keep a design-system component's composable slot last (#4146).

A component in `android/core/designsystem` that takes a composable slot (`@Composable … -> …`) ends with a function-typed
parameter, so a caller can pass the slot as a trailing lambda: `JSectionHeader(title) { … }`. A parameter added after the
slot binds that lambda to it and breaks every such caller at compile time, which is how #4124 broke `main` (#4145). New
parameters go before the slots.

Components that already end otherwise are listed in the baseline (their callers can't use a trailing lambda, so nothing
breaks); the list may only shrink, as the token baseline does.
"""
import argparse
import json
import re
from pathlib import Path

from android_token_baseline import code_only

ROOT = Path(__file__).resolve().parents[2]
SOURCES = 'android/core/designsystem/src/main'
BASELINE = ROOT / 'android/core/designsystem/tools/slot-order-baseline.json'
FUN = re.compile(r'\bfun\s+(?:<[^>{}]*>\s*)?(?:[\w.]+\.)?([A-Z]\w*)\s*\(')
OPEN, CLOSE = '([{<', ')]}>'


def parameters(code, start):
    """The top-level parameters of the list opening at code[start] == '('."""
    depth, current, out, i = 0, [], [], start
    while i < len(code):
        c = code[i]
        if c == '-' and code.startswith('->', i):
            current.append('->')
            i += 2
            continue
        if c in OPEN:
            depth += 1
            if depth > 1:
                current.append(c)
        elif c in CLOSE:
            depth -= 1
            if depth == 0:
                out.append(''.join(current))
                break
            current.append(c)
        elif c == ',' and depth == 1:
            out.append(''.join(current))
            current = []
        else:
            current.append(c)
        i += 1
    return [p.strip() for p in out if p.strip()]


def split_top(text, sep):
    """[text] split at its first [sep] outside brackets and generics."""
    depth = 0
    for i, c in enumerate(text):
        if c == '-' and text.startswith('->', i):
            continue
        if c in OPEN:
            depth += 1
        elif c in CLOSE and not (c == '>' and i and text[i - 1] == '-'):
            depth -= 1
        elif c == sep and depth == 0:
            return text[:i], text[i + 1:]
    return text, ''


def _wrapped(t):
    """Whether the parentheses at both ends of [t] enclose all of it: `(… -> …)`, not `() -> Unit`."""
    if not (t.startswith('(') and t.endswith(')')):
        return False
    depth = 0
    for i, c in enumerate(t):
        if c == '>' and i and t[i - 1] == '-':
            continue
        if c in OPEN:
            depth += 1
        elif c in CLOSE:
            depth -= 1
            if depth == 0 and i < len(t) - 1:
                return False
    return True


def is_function(type_text):
    """A function type, nullable, annotated, suspending or with a receiver: `(@Composable RowScope.() -> Unit)?`."""
    t = re.sub(r'@[\w.]+(?:\([^)]*\))?', ' ', type_text)
    while True:
        t = re.sub(r'^suspend\s+', '', t.strip()).rstrip('?').strip()
        if not _wrapped(t):
            break
        t = t[1:-1]
    depth = 0
    for i, c in enumerate(t):
        if c == '-' and t.startswith('->', i):
            if depth == 0:
                return True
            continue
        if c == '>' and i and t[i - 1] == '-':
            continue
        if c in OPEN:
            depth += 1
        elif c in CLOSE:
            depth -= 1
    return False


MODIFIER_LINE = re.compile(r'^\s*(?:@[\w.]+(?:\(.*\))?\s*)*(?:(?:public|internal|private|protected|inline|override|actual|expect|suspend)\s+)*$')


def declaration_head(code, fun_at):
    """The annotations and modifiers of the declaration whose `fun` is at [fun_at]: the text before it on its line, and the
    annotation lines right above (`@Composable`, `@OptIn(…)`), not a declaration before them."""
    line_start = code.rfind('\n', 0, fun_at) + 1
    lines = [code[line_start:fun_at]]
    end = line_start - 1
    while end > 0:
        start = code.rfind('\n', 0, end) + 1
        line = code[start:end]
        if not line.strip() or not MODIFIER_LINE.match(line):
            if line.strip():
                break
        lines.append(line)
        end = start - 1
    return '\n'.join(reversed(lines))


def violations(source):
    """(component, last parameter, slot) for each non-private composable component whose slot isn't last."""
    code = code_only(source)
    found = []
    for m in FUN.finditer(code):
        head = declaration_head(code, m.start())
        if '@Composable' not in head or re.search(r'\bprivate\b', head):
            continue
        params = parameters(code, m.end() - 1)
        named = []
        for p in params:
            name, rest = split_top(p, ':')
            type_text = split_top(rest, '=')[0]
            named.append((name.split()[-1] if name.split() else name, type_text))
        slots = [name for name, t in named if '@Composable' in t and is_function(t)]
        if slots and not is_function(named[-1][1]):
            found.append((m.group(1), named[-1][0], slots[-1]))
    return found


def scan(root):
    counts, details = {}, {}
    for path in sorted((root / SOURCES).rglob('*.kt')):
        rel = path.relative_to(root).as_posix()
        for name, last, slot in violations(path.read_text()):
            key = f'{rel}::{name}'
            counts[key] = counts.get(key, 0) + 1
            details.setdefault(key, []).append((last, slot))
    return counts, details


def main():
    parser = argparse.ArgumentParser(description="Check that design-system components keep a composable slot last.")
    parser.add_argument('--root', type=Path, default=ROOT, help='repository root')
    parser.add_argument('--baseline', type=Path, default=BASELINE, help='baseline file')
    parser.add_argument('--update-baseline', action='store_true', help='accept the current components and rewrite the baseline')
    args = parser.parse_args()
    counts, details = scan(args.root)
    if args.update_baseline:
        args.baseline.parent.mkdir(parents=True, exist_ok=True)
        args.baseline.write_text(json.dumps({'version': 1, 'components': counts}, indent=2, sort_keys=True) + '\n')
        print(f'slot order: baseline rewritten, {len(counts)} component(s) end without a slot')
        return 0
    allowed = json.loads(args.baseline.read_text())['components'] if args.baseline.exists() else {}
    failures = []
    for key, n in sorted(counts.items()):
        if n > allowed.get(key, 0):
            path, name = key.split('::')
            for last, slot in details[key]:
                failures.append(f'{path}: {name} ends with `{last}` after its composable slot `{slot}`; keep a slot last '
                                f'so callers can pass it as a trailing lambda (new parameters go before the slots)')
    for message in failures:
        print(message)
    shrunk = sorted(k for k, n in allowed.items() if counts.get(k, 0) < n)
    if shrunk:
        print('slot order: these now keep a slot last; run with --update-baseline to drop them: ' + ', '.join(shrunk))
    print(f'slot order: {sum(counts.values())} component(s) end without a slot, {len(failures)} not in the baseline')
    return 1 if failures else 0


if __name__ == '__main__':
    raise SystemExit(main())
