// Paths in messages as links (docs/web/web.md "File paths in messages"): those there on the agent's machine open in the file
// panel, at their line. Plain text (a user's message) through PathText; Markdown (an answer) through
// rehypePathLinks, which also links a code span that is a whole path (`src/a.ts:12`).
import { memo, useMemo } from "react";
import { fileDownloadURL } from "../lib/api";
import { resolveRef } from "../lib/files";
import { openPanel, useReadOnlyFile } from "../lib/panel";
import { codePathRef, findPathRefs, splitAtRefs, uniquePaths, type PathRef } from "../lib/path-refs";
import { usePathLinks } from "../lib/path-presence";
import { t } from "../i18n";

/** Opens a path a message names in the file panel; ⌘ / Ctrl / middle click is the browser's (the raw file). */
export function openPathRef(e: React.MouseEvent, mid: string, cwd: string | undefined, path: string, line?: number, readOnly?: boolean) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  openPanel({ kind: "file", mid, path: resolveRef(cwd ?? "", path) || path, cwd, line, ...(readOnly ? { readOnly: true } : {}) });
}

/** Where a path link points for the browser itself (a new tab: the file inline). */
export function pathHref(mid: string, cwd: string | undefined, path: string) {
  return fileDownloadURL(mid, resolveRef(cwd ?? "", path) || path, cwd, { inline: true });
}

/** The paths to ask about for a text (a message, as written: Markdown's code spans are its backticks). */
export function textPaths(text: string): string[] {
  return uniquePaths(findPathRefs(text));
}

/** A streaming text without its last word, which may be a path still being written (/tmp/jar…). */
export function writtenText(text: string): string {
  return text.slice(0, Math.max(0, text.search(/\s\S*$/)));
}

/** Plain text with the paths that are there linked. */
export const PathText = memo(function PathText({ text, mid, cwd, readOnly }: { text: string; mid?: string; cwd?: string; readOnly?: boolean }) {
  const inheritedReadOnly = useReadOnlyFile();
  const ro = Boolean(readOnly || inheritedReadOnly);
  const refs = useMemo(() => (mid ? findPathRefs(text) : []), [text, mid]);
  const { linkable, key } = usePathLinks(mid, cwd, useMemo(() => uniquePaths(refs), [refs]));
  const parts = useMemo(() => splitAtRefs(text, refs, linkable), [text, refs, key]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!mid || parts.length === 1) return <>{text}</>;
  return (
    <>
      {parts.map((p, i) =>
        typeof p === "string" ? (
          p
        ) : (
          <a
            key={i}
            href={pathHref(mid, cwd, p.path)}
            target="_blank"
            rel="noreferrer noopener"
            title={refTitle(p)}
            className="path-link"
            onClick={(e) => openPathRef(e, mid, cwd, p.path, p.line, ro || undefined)}
          >
            {text.slice(p.start, p.end)}
          </a>
        ),
      )}
    </>
  );
});

function refTitle(r: PathRef) {
  return r.line ? t("ui-pathlinks.path_line", { path: r.path, line: r.line }) : r.path;
}

// ── Markdown ──────────────────────────────────────────────

// The little of hast this needs.
type HNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HNode[] };

/**
 * A rehype plugin: text naming a path `linkable` says yes to becomes a link (href the path, data-line its line), and
 * so does a code span that is one. Links, code blocks and formulas are left alone.
 */
export function rehypePathLinks(linkable: (path: string) => boolean) {
  return () => (tree: HNode) => walk(tree, linkable);
}

function walk(node: HNode, linkable: (path: string) => boolean) {
  if (!node.children) return;
  const out: HNode[] = [];
  for (const c of node.children) {
    if (c.type === "text" && c.value) {
      const parts = splitAtRefs(c.value, findPathRefs(c.value), linkable);
      if (parts.length === 1) out.push(c);
      else for (const p of parts) if (typeof p === "string") p && out.push({ type: "text", value: p });
      else out.push(anchor(p, [{ type: "text", value: c.value.slice(p.start, p.end) }]));
      continue;
    }
    if (c.type !== "element" || c.tagName === "a" || c.tagName === "pre" || isMath(c)) {
      out.push(c);
      continue;
    }
    if (c.tagName === "code") {
      const ref = codePathRef(textOf(c));
      out.push(ref && linkable(ref.path) ? anchor(ref, [c]) : c);
      continue;
    }
    walk(c, linkable);
    out.push(c);
  }
  node.children = out;
}

function anchor(r: PathRef, children: HNode[]): HNode {
  return { type: "element", tagName: "a", properties: { href: r.path, dataLine: r.line, title: refTitle(r) }, children };
}

function isMath(n: HNode) {
  const cls = n.properties?.className;
  return Array.isArray(cls) && cls.some((c) => typeof c === "string" && /katex|math/.test(c));
}

function textOf(n: HNode): string {
  return n.type === "text" ? (n.value ?? "") : (n.children ?? []).map(textOf).join("");
}
