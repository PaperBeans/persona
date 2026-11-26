// The file viewer's small lexer, loaded with the viewer, notebook and editor rather than with the first screen's path,
// Git and diff helpers in lib/files.ts. The code below is the same lexer those viewers already used.

import { extOf, fileName } from "./files";

// ── a small lexer: comments, strings, numbers (and JSON keys / constants, diff lines) ─────────

export type TokenKind = "comment" | "string" | "number" | "key" | "const" | "add" | "del" | "meta";

export interface Token {
  from: number;
  to: number;
  kind: TokenKind;
}

/** What is still open at the end of a line: a block comment or a multi-line string, and how it ends. */
export type LexState = { kind: "comment" | "string"; end: string } | null;

export interface Lang {
  id: string;
  line?: string[];
  block?: [string, string][];
  /** String delimiters, longest first; the ones in `multiline` may span lines. */
  strings?: string[];
  multiline?: string[];
  /** Backslash does not escape in these (shell '…', Go `…`). */
  raw?: string[];
  consts?: Set<string>;
  /** A quote opens a string only when the text before it matches (YAML: after "key:" / "- "; HTML: an attribute's =). */
  quoteAfter?: RegExp;
  json?: boolean;
  diff?: boolean;
}

const C_LIKE = { line: ["//"], block: [["/*", "*/"]] as [string, string][] };
const HASH = { line: ["#"] };
const BOOL = new Set(["true", "false", "null", "nil", "None", "True", "False", "undefined"]);

const LANGS: Record<string, Lang> = {
  swift: { id: "Swift", ...C_LIKE, strings: ['"""', '"'], multiline: ['"""'], consts: BOOL },
  go: { id: "Go", ...C_LIKE, strings: ['"', "'", "`"], multiline: ["`"], raw: ["`"], consts: BOOL },
  js: { id: "JavaScript", ...C_LIKE, strings: ['"', "'", "`"], multiline: ["`"], consts: BOOL },
  ts: { id: "TypeScript", ...C_LIKE, strings: ['"', "'", "`"], multiline: ["`"], consts: BOOL },
  rust: { id: "Rust", ...C_LIKE, strings: ['"'], multiline: ['"'], consts: BOOL },
  c: { id: "C / C++", ...C_LIKE, strings: ['"', "'"], consts: BOOL },
  objc: { id: "Objective-C", ...C_LIKE, strings: ['"', "'"], consts: new Set(["YES", "NO", "nil", "NULL", "true", "false"]) },
  java: { id: "Java", ...C_LIKE, strings: ['"""', '"', "'"], multiline: ['"""'], consts: BOOL },
  kotlin: { id: "Kotlin", ...C_LIKE, strings: ['"""', '"', "'"], multiline: ['"""'], consts: BOOL },
  scala: { id: "Scala", ...C_LIKE, strings: ['"""', '"', "'"], multiline: ['"""'], consts: BOOL },
  groovy: { id: "Groovy", ...C_LIKE, strings: ["'''", '"""', '"', "'"], multiline: ["'''", '"""'], consts: BOOL },
  csharp: { id: "C#", ...C_LIKE, strings: ['"', "'"], consts: BOOL },
  dart: { id: "Dart", ...C_LIKE, strings: ["'''", '"""', '"', "'"], multiline: ["'''", '"""'], consts: BOOL },
  php: { id: "PHP", line: ["//", "#"], block: [["/*", "*/"]], strings: ['"', "'"], consts: BOOL },
  css: { id: "CSS", block: [["/*", "*/"]], strings: ['"', "'"] },
  scss: { id: "SCSS", ...C_LIKE, strings: ['"', "'"] },
  proto: { id: "Protobuf", ...C_LIKE, strings: ['"', "'"], consts: BOOL },
  python: { id: "Python", ...HASH, strings: ['"""', "'''", '"', "'"], multiline: ['"""', "'''"], consts: BOOL },
  shell: { id: "Shell", ...HASH, strings: ['"', "'"], raw: ["'"], consts: BOOL },
  ruby: { id: "Ruby", ...HASH, strings: ['"', "'"], consts: BOOL },
  yaml: { id: "YAML", ...HASH, strings: ['"', "'"], quoteAfter: /(^|[:\-[{,])\s*$/, consts: new Set(["true", "false", "null", "yes", "no", "on", "off", "~"]) },
  toml: { id: "TOML", ...HASH, strings: ['"""', "'''", '"', "'"], multiline: ['"""', "'''"], raw: ["'''", "'"], consts: BOOL },
  ini: { id: "INI", line: ["#", ";"], strings: ['"'] },
  docker: { id: "Dockerfile", ...HASH, strings: ['"', "'"] },
  make: { id: "Makefile", ...HASH, strings: ['"', "'"] },
  cmake: { id: "CMake", ...HASH, strings: ['"'] },
  hcl: { id: "HCL", line: ["#", "//"], block: [["/*", "*/"]], strings: ['"'], consts: BOOL },
  r: { id: "R", ...HASH, strings: ['"', "'"], consts: new Set(["TRUE", "FALSE", "NULL", "NA"]) },
  perl: { id: "Perl", ...HASH, strings: ['"', "'"] },
  elixir: { id: "Elixir", ...HASH, strings: ['"""', '"', "'"], multiline: ['"""'], consts: BOOL },
  sql: { id: "SQL", line: ["--"], block: [["/*", "*/"]], strings: ["'", '"'], consts: new Set(["NULL", "TRUE", "FALSE", "null", "true", "false"]) },
  lua: { id: "Lua", line: ["--"], block: [["--[[", "]]"]], strings: ['"', "'"], consts: BOOL },
  haskell: { id: "Haskell", line: ["--"], block: [["{-", "-}"]], strings: ['"'], consts: new Set(["True", "False"]) },
  graphql: { id: "GraphQL", ...HASH, strings: ['"""', '"'], multiline: ['"""'], consts: BOOL },
  html: { id: "HTML", block: [["<!--", "-->"]], strings: ['"', "'"], quoteAfter: /=\s*$/ },
  xml: { id: "XML", block: [["<!--", "-->"]], strings: ['"', "'"], quoteAfter: /=\s*$/ },
  json: { id: "JSON", line: ["//"], block: [["/*", "*/"]], strings: ['"'], consts: new Set(["true", "false", "null"]), json: true },
  diff: { id: "diff", diff: true },
  markdown: { id: "Markdown" },
  text: { id: "" },
};

const BY_EXT: Record<string, string> = {
  swift: "swift", go: "go", js: "js", mjs: "js", cjs: "js", jsx: "js", ts: "ts", tsx: "ts", mts: "ts", cts: "ts",
  rs: "rust", c: "c", h: "c", cc: "c", cpp: "c", cxx: "c", hpp: "c", hh: "c", hxx: "c", m: "objc", mm: "objc",
  java: "java", kt: "kotlin", kts: "kotlin", scala: "scala", sc: "scala", groovy: "groovy", gradle: "groovy",
  cs: "csharp", dart: "dart", php: "php", css: "css", scss: "scss", less: "scss", sass: "scss", proto: "proto",
  py: "python", pyi: "python", sh: "shell", bash: "shell", zsh: "shell", fish: "shell", ksh: "shell", rb: "ruby",
  yml: "yaml", yaml: "yaml", toml: "toml", ini: "ini", cfg: "ini", conf: "ini", env: "ini", properties: "ini",
  dockerfile: "docker", mk: "make", cmake: "cmake", tf: "hcl", tfvars: "hcl", hcl: "hcl", r: "r", pl: "perl",
  pm: "perl", ex: "elixir", exs: "elixir", sql: "sql", lua: "lua", hs: "haskell", graphql: "graphql", gql: "graphql",
  html: "html", htm: "html", vue: "html", svelte: "html", xml: "xml", plist: "xml", svg: "xml", storyboard: "xml",
  xib: "xml", xcscheme: "xml", json: "json", jsonc: "json", json5: "json", jsonl: "json", ndjson: "json",
  geojson: "json", webmanifest: "json", diff: "diff", patch: "diff", md: "markdown", markdown: "markdown",
};

const BY_NAME: Record<string, string> = {
  dockerfile: "docker", containerfile: "docker", makefile: "make", gnumakefile: "make", "cmakelists.txt": "cmake",
  podfile: "ruby", gemfile: "ruby", rakefile: "ruby", fastfile: "ruby", brewfile: "ruby", ".zshrc": "shell",
  ".bashrc": "shell", ".bash_profile": "shell", ".profile": "shell", ".zprofile": "shell", ".zshenv": "shell",
  ".env": "ini", ".gitignore": "ini", ".gitconfig": "ini", ".editorconfig": "ini", ".npmrc": "ini",
  ".dockerignore": "ini", "go.mod": "go", "go.sum": "text", "package.json": "json",
};

/** The language of a file from its name, then its first line (#!/usr/bin/env python3, <?xml); null: plain text. */
export function languageOf(name: string, firstLine = ""): Lang | null {
  const n = fileName(name).toLowerCase();
  let id = BY_NAME[n] ?? (n.startsWith("dockerfile.") || n.endsWith(".dockerfile") ? "docker" : n.startsWith(".env.") ? "ini" : BY_EXT[extOf(n)]);
  if (!id && firstLine.startsWith("#!")) {
    // #!/usr/bin/env python3, #!/bin/bash -e → python, bash
    const words = firstLine.slice(2).trim().split(/\s+/);
    const cmd = words[0].endsWith("/env") ? (words.find((w, i) => i > 0 && !w.startsWith("-")) ?? "") : words[0];
    const prog = fileName(cmd).replace(/[\d.]+$/, "");
    id = /^(ba|z|k|da|fi)?sh$/.test(prog) ? "shell" : prog === "python" ? "python" : prog === "node" || prog === "deno" || prog === "bun" ? "js" : prog === "ruby" ? "ruby" : prog === "perl" ? "perl" : "";
  }
  if (!id && firstLine.startsWith("<?xml")) id = "xml";
  if (!id || id === "text") return null;
  return LANGS[id];
}

const IDENT = /[A-Za-z0-9_$]/;
const NUMBER = /(?:0[xX][0-9a-fA-F_]+|0[bB][01_]+|0[oO][0-7_]+|(?:\d[\d_]*)?\.?\d[\d_]*(?:[eE][+-]?\d+)?)[a-zA-Z]{0,3}/y;
const WORD = /[A-Za-z_$~][A-Za-z0-9_$]*/y;

/** One line's tokens, carrying a block comment / multi-line string from the line before and to the next. */
export function lexLine(line: string, lang: Lang, state: LexState): { tokens: Token[]; state: LexState } {
  const tokens: Token[] = [];
  const n = line.length;
  if (lang.diff) {
    if (/^(diff --git|index |--- |\+\+\+ |@@)/.test(line)) tokens.push({ from: 0, to: n, kind: "meta" });
    else if (line.startsWith("+")) tokens.push({ from: 0, to: n, kind: "add" });
    else if (line.startsWith("-")) tokens.push({ from: 0, to: n, kind: "del" });
    return { tokens, state: null };
  }
  let i = 0;
  if (state) {
    const end = findEnd(line, 0, state.end, state.kind === "string" && !lang.raw?.includes(state.end));
    if (end < 0) return { tokens: n ? [{ from: 0, to: n, kind: state.kind }] : [], state };
    tokens.push({ from: 0, to: end, kind: state.kind });
    i = end;
  }
  outer: while (i < n) {
    const c = line[i];
    for (const lc of lang.line ?? []) {
      // "#" is a comment only at the start or after a space: not in $#, ${#a}, url#frag, "#fff"
      if (line.startsWith(lc, i) && (lc !== "#" || i === 0 || /\s/.test(line[i - 1]))) {
        tokens.push({ from: i, to: n, kind: "comment" });
        break outer;
      }
    }
    for (const [open, close] of lang.block ?? []) {
      if (line.startsWith(open, i)) {
        const end = findEnd(line, i + open.length, close, false);
        if (end < 0) {
          tokens.push({ from: i, to: n, kind: "comment" });
          return { tokens, state: { kind: "comment", end: close } };
        }
        tokens.push({ from: i, to: end, kind: "comment" });
        i = end;
        continue outer;
      }
    }
    for (const d of lang.strings ?? []) {
      if (line.startsWith(d, i) && (!lang.quoteAfter || lang.quoteAfter.test(line.slice(Math.max(0, i - 24), i)))) {
        const end = findEnd(line, i + d.length, d, !lang.raw?.includes(d));
        if (end < 0) {
          tokens.push({ from: i, to: n, kind: "string" });
          if (lang.multiline?.includes(d)) return { tokens, state: { kind: "string", end: d } };
          break outer;
        }
        let kind: TokenKind = "string";
        if (lang.json && /^\s*:/.test(line.slice(end, end + 40))) kind = "key";
        tokens.push({ from: i, to: end, kind });
        i = end;
        continue outer;
      }
    }
    if (/[0-9]/.test(c) || (c === "." && /[0-9]/.test(line[i + 1] ?? ""))) {
      if (i === 0 || !IDENT.test(line[i - 1])) {
        NUMBER.lastIndex = i;
        const m = NUMBER.exec(line);
        if (m && m[0]) {
          tokens.push({ from: i, to: i + m[0].length, kind: "number" });
          i += m[0].length;
          continue;
        }
      }
    }
    if (/[A-Za-z_$~]/.test(c)) {
      WORD.lastIndex = i;
      const m = WORD.exec(line);
      const w = m?.[0] ?? c;
      if (lang.consts?.has(w) && (i === 0 || !IDENT.test(line[i - 1]))) tokens.push({ from: i, to: i + w.length, kind: "const" });
      i += w.length;
      continue;
    }
    i++;
  }
  return { tokens, state: null };
}

/** Where a string / comment that started before `from` ends (just past its closing delimiter), or -1. */
function findEnd(line: string, from: number, close: string, escapes: boolean): number {
  for (let j = from; j < line.length; j++) {
    if (escapes && line[j] === "\\") {
      j++;
      continue;
    }
    if (line.startsWith(close, j)) return j + close.length;
  }
  return -1;
}

/** The lexer's state at the start of each line, up to `upTo` (exclusive), extending `states` in place. */
export function extendStates(lines: string[], lang: Lang, states: LexState[], upTo: number) {
  if (!states.length) states.push(null);
  const end = Math.min(upTo, lines.length);
  for (let i = states.length - 1; i < end; i++) states[i + 1] = lexLine(lines[i], lang, states[i]).state;
}
