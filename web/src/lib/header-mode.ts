// The Agent header's permission mode (#3956, as iOS `SessionModes` from #3719 and Android #3957; its tones are
// ModeTone in docs/design/DESIGN_SYSTEM.md): the mode's symbol in a tone that says how much it lets the agent do without asking, and the rules of the mode
// menu the header and the composer's chip share (which modes it offers, which picks ask first).
import { t } from "../i18n";
import type { IconName } from "../ui/icons";
import type { MenuItem } from "../ui/menu-model";
import { stateOf } from "./format";
import { BYPASS_MODE, BYPASS_WARNING, canSwitchToBypass, hasModes, modeTitle, PERMISSION_MODES, switchableModes } from "./session-actions";
import type { Session } from "./types";

/** Ask First (the quiet gray), Plan Only (teal), Auto-Edit and Auto (purple), Allow Everything (red); unknown: not reported yet. */
export type ModeTone = "asks" | "plans" | "acts" | "allows" | "unknown";

/** A mode not reported yet, or one this app does not know, is `unknown`: never a guessed Ask First, which would make an agent
 *  started in Allow Everything look safe. */
export function headerTone(mode: string | undefined): ModeTone {
  switch (mode) {
    case "default":
      return "asks";
    case "plan":
      return "plans";
    case "acceptEdits":
    case "auto":
      return "acts";
    case BYPASS_MODE:
      return "allows";
    default:
      return "unknown";
  }
}

/** The mode menu's own symbol per mode (iOS hand.raised, pencil.line, list.bullet.clipboard, wand.and.sparkles,
 *  exclamationmark.shield; the plain shield when unknown). */
export function headerIcon(mode: string | undefined): IconName {
  switch (mode) {
    case "default":
      return "hand";
    case "acceptEdits":
      return "pencil";
    case "plan":
      return "list";
    case "auto":
      return "sparkles";
    case BYPASS_MODE:
      return "shieldAlert";
    default:
      return "shield";
  }
}

/** The mode can be changed from here: an agent with modes that runs (the composer's chips' rule). */
export function modesChangeable(s: Session): boolean {
  return hasModes(s) && stateOf(s) !== "offline";
}

/** The header shows an agent's mode when there is one to see or to change (iOS `showsInHeader`): Claude Code's, before its
 *  first prompt too. Not an assistant conversation (its page has its own controls), nor an agent whose mode can be neither
 *  read nor changed (Codex). */
export function showsInHeader(s: Session | undefined, assistant?: boolean): boolean {
  return Boolean(s && !assistant && !s.assistant && (s.permission_mode || modesChangeable(s)));
}

/** An assistant conversation (and Antigravity, as before) switches without asking. */
const asksFirst = (s: Session, assistant?: boolean) => !(assistant || s.assistant || s.agent === "agy");

/** What a pick asks first (#3719): going into Allow Everything, and leaving it when the agent cannot come back to it from
 *  here (`bypass_available` false: only a new start picks it). */
export function modeConfirmation(s: Session, to: string, assistant?: boolean): "allow" | "leave" | null {
  if (!asksFirst(s, assistant) || to === s.permission_mode) return null;
  if (to === BYPASS_MODE) return "allow";
  if (s.permission_mode === BYPASS_MODE && !s.bypass_available) return "leave";
  return null;
}

/** The mode menu's rows, the header's and the composer chip's: the modes it can switch to, the current one checked, the
 *  picks that ask first asking (the row turns into its question, a second choice runs it), and Allow Everything shown
 *  disabled with its reason where the agent cannot take it while it runs. `locked`: nothing can be picked (not running). */
export function modeMenuItems(s: Session, pick: (mode: string) => void, opts: { assistant?: boolean; locked?: boolean } = {}): MenuItem[] {
  const mode = s.permission_mode;
  const rows: MenuItem[] = switchableModes(opts.assistant || s.assistant, s.agent, canSwitchToBypass(s)).map((m) => {
    const ask = modeConfirmation(s, m.id, opts.assistant);
    return {
      key: m.id,
      label: m.title,
      detail: m.detail,
      radio: true,
      checked: mode === m.id,
      disabled: opts.locked,
      ...(ask === "allow" ? { danger: true, confirm: t("chat-sessiondialogs.bypass_confirm"), confirmDetail: BYPASS_WARNING } : {}),
      ...(ask === "leave" ? { confirm: t("chat-sessiondialogs.leave_bypass_confirm", { mode: modeTitle(m.id) }), confirmDetail: t("chat-sessiondialogs.leave_bypass_detail") } : {}),
      onSelect: () => pick(m.id),
    };
  });
  const offered = rows.some((r) => r.key === BYPASS_MODE);
  if (!offered && asksFirst(s, opts.assistant) && !opts.locked && hasModes(s)) {
    const bypass = PERMISSION_MODES.find((m) => m.id === BYPASS_MODE)!;
    rows.push({ key: BYPASS_MODE, label: bypass.title, detail: bypass.detail, disabled: true, onSelect: () => {} });
  }
  return rows;
}
