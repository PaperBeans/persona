// What the switch divider's chip opens (#5054, EngineSwitchDivider.tsx): the handoff the new engine was given, under a
// title saying what it is, selectable; the rest of a handoff the hub cut for the page loads on "View full text". Off the
// first screen: the divider loads it as it is drawn, so it is in before the chip is first pressed.
import { ENGINE_SWITCH_KIND, engineSwitchOf, type EngineSwitch } from "../../lib/engine-switch";
import { withoutCutMark } from "../../lib/tool-detail";
import { parseUserText } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { t } from "../../i18n";
import { useFullText } from "./FullText";

/** The handoff in the whole text of the message, once fetched. */
function wholeHandoff(raw: string): string | undefined {
  for (const c of parseUserText(raw).contexts) if (c.kind === ENGINE_SWITCH_KIND) return engineSwitchOf(c)?.text;
  return undefined;
}

/** `id`: the panel's (the chip's aria-controls). `item`: the message's transcript item, for the rest of a handoff the
 *  hub cut for the page (`full_size`). */
export function EngineHandoffPanel({ id, sw, item, mid, sid }: { id: string; sw: EngineSwitch; item?: Item; mid: string; sid: string }) {
  const cut = item?.full_size ? item : undefined;
  const full = useFullText(cut, mid, sid);
  const handoff = cut ? ((!full.more && wholeHandoff(full.text)) || withoutCutMark(sw.text)) : sw.text;
  return (
    <div id={id} className="mx-auto mt-2 max-w-full rounded-card border border-line-strong bg-inset px-4 py-3 select-text" data-engine-handoff>
      <div className="mb-1.5 text-[11px] font-medium text-ink-3">{t("chat-engine.handoff_title")}</div>
      <pre className="max-h-64 overflow-y-auto font-mono text-[12px] leading-[1.55] whitespace-pre-wrap text-tool-ink [overflow-wrap:anywhere]">{handoff}</pre>
      {full.more}
    </div>
  );
}
