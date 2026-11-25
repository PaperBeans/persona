// Terminal escape stripping for text written by a shell, a CLI or an agent: launcher errors, notebook output. Colour and
// cursor codes are CSI, titles, hyperlinks and shell integration are OSC, DCS / SOS / PM / APC carry other payloads, and
// the rest are two-byte ESC sequences. The C1 forms (U+0080–U+009F) exist too. Everything but LF and TAB goes, and CR or
// CRLF becomes LF. Pure.

// OSC: ends at BEL, ST (ESC \ or U+009C) or, unterminated, the end of the line or text.
const OSC = /\u001b\][^\n]*?(?:\u0007|\u001b\\|\u009c|(?=\n)|$)/g;
const C1_OSC = /\u009d[^\n]*?(?:\u0007|\u009c|(?=\n)|$)/g;

// DCS, SOS, PM and APC: the same terminators.
const STRING = /\u001b[PX^_][^\n]*?(?:\u0007|\u001b\\|\u009c|(?=\n)|$)/g;
const C1_STRING = /[\u0090\u0098\u009e\u009f][^\n]*?(?:\u0007|\u009c|(?=\n)|$)/g;

// CSI: parameters, intermediates, final byte.
const CSI = /\u001b\[[0-9:;<=>?]*[ -/]*[@-~]/g;
const C1_CSI = /\u009b[0-9:;<=>?]*[ -/]*[@-~]/g;

// A CSI cut off before its final byte: its parameters go too, or they would read as text.
const CSI_TAIL = /\u001b\[[0-9:;<=>?]*[ -/]*/g;
const C1_CSI_TAIL = /\u009b[0-9:;<=>?]*[ -/]*/g;

// Everything else ESC introduces: intermediates and one final byte (`\x1b=`, `\x1b(B`, `\x1b7`…). A lone ESC is left to
// the control pass below.
const ESC = /\u001b[ -/]*[0-~]/g;

// C0 (but LF and TAB), DEL and the C1 controls.
// eslint-disable-next-line no-control-regex
const CONTROLS = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g;

/** `s` without terminal escapes and control characters: CR and CRLF become LF, LF and TAB stay, invalid-looking bytes are
 *  untouched. A string with nothing to strip comes back unchanged. */
export function stripAnsi(s: string): string {
  return s
    .replace(/\r\n/g, "\n")
    .replace(/\r/g, "\n")
    .replace(OSC, "")
    .replace(C1_OSC, "")
    .replace(STRING, "")
    .replace(C1_STRING, "")
    .replace(CSI, "")
    .replace(C1_CSI, "")
    .replace(CSI_TAIL, "")
    .replace(C1_CSI_TAIL, "")
    .replace(ESC, "")
    .replace(CONTROLS, "");
}

export interface AnsiRun {
  text: string;
  fg?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

// eslint-disable-next-line no-control-regex
const SGR_CSI_RE = /\u001b\[([0-9;]*)([A-Za-z])/g;

const FG_COLOR: Record<number, string> = {
  30: "text-ink-3",
  31: "text-red",
  32: "text-green",
  33: "text-orange",
  34: "text-accent-ink",
  35: "text-[var(--term-magenta)]",
  36: "text-[var(--term-cyan)]",
  37: "text-ink",
  90: "text-ink-3",
  91: "text-red",
  92: "text-green",
  93: "text-orange",
  94: "text-accent-ink",
  95: "text-[var(--term-magenta)]",
  96: "text-[var(--term-cyan)]",
  97: "text-ink",
};

/** Parse ANSI SGR escapes into styled runs. Returns null if `text` contains no ESC byte. */
export function parseAnsi(text: string): AnsiRun[] | null {
  if (!text.includes("\u001b")) return null;
  const runs: AnsiRun[] = [];
  let fg: string | undefined;
  let bold = false;
  let dim = false;
  let italic = false;
  let underline = false;

  const push = (chunk: string) => {
    if (!chunk) return;
    const prev = runs[runs.length - 1];
    if (
      prev &&
      prev.fg === fg &&
      Boolean(prev.bold) === bold &&
      Boolean(prev.dim) === dim &&
      Boolean(prev.italic) === italic &&
      Boolean(prev.underline) === underline
    ) {
      prev.text += chunk;
    } else {
      const run: AnsiRun = { text: chunk };
      if (fg) run.fg = fg;
      if (bold) run.bold = true;
      if (dim) run.dim = true;
      if (italic) run.italic = true;
      if (underline) run.underline = true;
      runs.push(run);
    }
  };

  SGR_CSI_RE.lastIndex = 0;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = SGR_CSI_RE.exec(text)) !== null) {
    if (m.index > last) push(text.slice(last, m.index));
    last = SGR_CSI_RE.lastIndex;
    if (m[2] !== "m") continue;
    const raw = m[1];
    const codes = raw === "" ? [0] : raw.split(";").map((s) => (s === "" ? 0 : Number(s)));
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i];
      if (c === 0) {
        fg = undefined;
        bold = false;
        dim = false;
        italic = false;
        underline = false;
      } else if (c === 1) {
        bold = true;
      } else if (c === 2) {
        dim = true;
      } else if (c === 3) {
        italic = true;
      } else if (c === 4) {
        underline = true;
      } else if (c === 22) {
        bold = false;
        dim = false;
      } else if (c === 23) {
        italic = false;
      } else if (c === 24) {
        underline = false;
      } else if (c === 39) {
        fg = undefined;
      } else if (c === 38 || c === 48) {
        if (codes[i + 1] === 5) i += 2;
        else if (codes[i + 1] === 2) i += 4;
      } else if (FG_COLOR[c]) {
        fg = FG_COLOR[c];
      }
    }
  }
  if (last < text.length) push(text.slice(last));
  return runs;
}
