// The rules of an Agent's permission mode menu (#3956, as iOS `SessionModes` from #3719 and Android #3957): which modes the
// composer's picker offers, which picks ask first, and each mode's symbol. The web's header no longer has a mode button
// (#5421, cindy's: the mode is the composer's picker and Session info).
import { t } from "../i18n";
import type { IconName } from "../ui/icons";
import type { MenuItem } from "../ui/menu-model";
import { BYPASS_MODE, BYPASS_WARNING, canSwitchToBypass, hasModes, modeTitle, PERMISSION_MODES, switchableModes } from "./session-actions";
import type { Session } from "./types";

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

/** The composer picker's symbol per mode (#5324, cindy's: a raised hand, code, a list, sparkles, a warning triangle); a
 *  mode this app does not know the plain shield. Not reported yet reads as Default permissions, as the picker's label does. */
export function pickerIcon(mode: string | undefined): IconName {
  switch (mode) {
    case undefined:
    case "":
    case "default":
      return "palm";
    case "acceptEdits":
      return "code";
    case "plan":
      return "list";
    case "auto":
      return "sparkles";
    case BYPASS_MODE:
      return "warning";
    default:
      return "shield";
  }
}

/** Where unchecking Plan mode (the + panel's check row, #5324) goes: the mode the agent had before it, as this page saw it,
 *  while it can still switch to it; else Default permissions. Never Full access, which only the picker chooses, with its
 *  confirmation. */
export function leavePlanTo(s: Session, before: string | undefined, assistant?: boolean): string {
  if (!before || before === "plan" || before === BYPASS_MODE) return "default";
  const offered = switchableModes(assistant || s.assistant, s.agent, canSwitchToBypass(s));
  return offered.some((m) => m.id === before) ? before : "default";
}
