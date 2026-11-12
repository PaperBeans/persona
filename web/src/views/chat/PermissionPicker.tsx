// The composer's permission picker (#5324, cindy's PermissionSelector, at the owner's request): a bare chip after the +,
// the mode's symbol, its name and a chevron, framed only on hover or while its menu is open; Full access tints its words
// red. Its menu is the header's (`modeMenuItems`: into Full access asks again, leaving it asks where it can't come back,
// Full access shown disabled with its reason where the agent can't take it) without Plan mode, which is the + panel's
// check row (`usePlanMode`); each mode's description is its row's tooltip. A Persona conversation keeps all its modes
// (#2416) in the same words.
import { memo, useCallback, useEffect, useMemo, useRef } from "react";
import { leavePlanTo, modeConfirmation, modeMenuItems, pickerIcon } from "../../lib/header-mode";
import { BYPASS_MODE, BYPASS_WARNING, modeTitle, PERMISSION_MODES } from "../../lib/session-actions";
import type { Session } from "../../lib/types";
import { cx, Menu, useMenu } from "../../ui/atoms";
import type { MenuItem } from "../../ui/menu-model";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";
import { applyModes, useModeSet } from "./SessionDialogs";

const PLAN = "plan";

/** The words a mode's chip and row show on hover: what the mode does (Full access's warning when it is on). */
function modeTip(mode: string | undefined): string {
  if (mode === BYPASS_MODE) return BYPASS_WARNING;
  return PERMISSION_MODES.find((m) => m.id === (mode || "default"))?.detail ?? "";
}

/** The picker's rows: the header's, Plan mode left to the + panel, each with its symbol and its description on hover. A
 *  disabled row keeps its reason under its name (a disabled row shows no tooltip), and so does a row asking again. */
export function pickerItems(s: Session, pick: (mode: string) => void, assistant?: boolean): MenuItem[] {
  return modeMenuItems(s, pick, { assistant })
    .filter((r) => r.key !== PLAN)
    .map((r) => ({
      ...r,
      icon: pickerIcon(r.key),
      ...(r.disabled ? {} : { detail: undefined, tip: r.detail }),
      danger: r.danger || (r.key === BYPASS_MODE && r.checked),
    }));
}

export const PermissionPicker = memo(function PermissionPicker({ session, assistant }: { session: Session; assistant?: boolean }) {
  const menu = useMenu();
  const { set } = useModeSet(session);
  const mode = session.permission_mode;
  const danger = mode === BYPASS_MODE;
  const label = modeTitle(mode || "default");
  return (
    <>
      <button
        type="button"
        data-testid="permission-picker"
        data-mode={mode || "default"}
        aria-haspopup="menu"
        aria-expanded={Boolean(menu.anchor)}
        aria-label={t("chat-sessiondialogs.header_mode", { mode: label })}
        title={modeTip(mode)}
        // the caret stays in the message box (Tab still reaches the chip)
        onMouseDown={(e) => e.preventDefault()}
        onClick={menu.open}
        className={cx(
          // as the model pill: no frame at rest, the capsule on hover and while the menu is open
          "touch-hit flex h-[30px] min-w-0 max-w-full shrink items-center gap-1 rounded-full border border-transparent px-2.5 text-[13px] leading-none whitespace-nowrap transition-colors duration-150",
          "hover:border-line-strong hover:bg-hover aria-expanded:border-line-strong aria-expanded:bg-hover focus-visible:rounded-full!",
          // a very narrow box keeps the symbol (the words stay its accessible name and tooltip), as cindy's icon-only chip
          "@max-[300px]/composer:px-2",
          danger ? "text-red" : mode === "auto" ? "text-mode-acts" : "text-ink",
        )}
      >
        <Icon name={pickerIcon(mode)} size={14} stroke={2} className="shrink-0" />
        <span className="min-w-0 truncate @max-[300px]/composer:hidden">{label}</span>
        <Icon name="chevron" size={14} stroke={2} className="shrink-0 @max-[300px]/composer:hidden" />
      </button>
      {menu.anchor && <Menu anchor={menu.anchor} onClose={menu.close} side="top" width={300} items={pickerItems(session, (m) => void set({ permission_mode: m }), assistant)} />}
    </>
  );
});

/** The + panel's Plan mode row (#5324): checked while the session plans; unchecking goes back to the mode it had before, as
 *  this page saw it (`leavePlanTo`). Undefined where the session has no modes to switch. */
export interface PlanMode {
  on: boolean;
  toggle: () => void;
}

// the mode each chat had before Plan mode, as this page saw it
const beforePlan = new Map<string, string>();

export function usePlanMode(session: Session | undefined, assistant?: boolean): PlanMode | undefined {
  const latest = useRef(session);
  latest.current = session;
  const key = session ? `${session.machine_id}/${session.session_id}` : "";
  const mode = session?.permission_mode;
  useEffect(() => {
    if (key && mode && mode !== PLAN) beforePlan.set(key, mode);
  }, [key, mode]);
  const toggle = useCallback(() => {
    const s = latest.current;
    if (!s) return;
    const to = s.permission_mode === PLAN ? leavePlanTo(s, beforePlan.get(`${s.machine_id}/${s.session_id}`), assistant) : PLAN;
    // into Plan mode from a Full access the agent can't come back to asks first, as the picker's row does
    if (modeConfirmation(s, to, assistant) === "leave" && !confirm(`${t("chat-sessiondialogs.leave_bypass_confirm", { mode: modeTitle(to) })}\n\n${t("chat-sessiondialogs.leave_bypass_detail")}`)) return;
    void applyModes(() => latest.current ?? s, { permission_mode: to });
  }, [assistant]);
  const on = mode === PLAN;
  return useMemo(() => (key ? { on, toggle } : undefined), [key, on, toggle]);
}
