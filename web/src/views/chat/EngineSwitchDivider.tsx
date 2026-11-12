// The line across the chat where the conversation moved to another engine (#5054, lib/engine-switch.ts), after cindy's
// agent switch card: "⇄ Switched from Claude Code to Codex · GPT-5.5 · Continued from summary ›" between two hairlines,
// above the first message the new engine got. The chip opens the handoff the new engine was given, when there is one;
// opened or not is remembered per divider. The handoff's panel (EngineHandoffPanel.tsx) is off the first screen: a
// divider loads it as it is drawn, so it is in before the chip is first pressed, and the panel opens only once it is.
import { useEffect, useId, useState } from "react";
import { agentName } from "../../lib/agent-kind";
import type { EngineSwitch } from "../../lib/engine-switch";
import { replyModelName } from "../../lib/model-change";
import type { Item } from "../../lib/types";
import { Collapse } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";
import { useChatContext } from "./chat-context";
import { useExpanded } from "./expanded";

type Panel = typeof import("./EngineHandoffPanel").EngineHandoffPanel;
/** The panel once loaded: a divider drawn after that has it at once. */
let loadedPanel: Panel | undefined;
const loadPanel = () => import("./EngineHandoffPanel").then((m) => (loadedPanel = m.EngineHandoffPanel));

/** The words the divider says: "Switched from Claude Code to Codex". */
export function engineSwitchLabel(sw: Pick<EngineSwitch, "from" | "to">): string {
  return t("chat-lines.engine_switched", { from: agentName(sw.from), to: agentName(sw.to) });
}

/** `id`: the exchange's (where its open state is kept). `item`: the message's transcript item, for the rest of a
 *  handoff the hub cut for the page (`full_size`). */
export function EngineSwitchDivider({ sw, id, item, mid, sid }: { sw: EngineSwitch; id: string; item?: Item; mid: string; sid: string }) {
  const { scope } = useChatContext();
  const [open, setOpen] = useExpanded(`${scope}engine-switch:${id}`);
  const panel = useId();
  const label = engineSwitchLabel(sw);
  const model = sw.model ? replyModelName(sw.model) : "";
  const badge = t(sw.resumed ? "chat-lines.engine_resumed" : "chat-lines.engine_continued");
  const has = Boolean(sw.text.trim());
  const [HandoffPanel, setPanel] = useState<Panel | undefined>(() => loadedPanel);
  const fetchPanel = () => void loadPanel().then((p) => setPanel(() => p)).catch(() => {}); // a failed load tries again on the next press
  useEffect(() => {
    if (has && !HandoffPanel) fetchPanel();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [has, HandoffPanel]);
  // Too narrow for all of it (a long model id): the model and the badge give up their room, the words saying what
  // happened keep theirs (cut only past the chip's whole width). A phone's column has room for less: the engines as the
  // model change divider names models ("Claude Code → Codex") and the badge, no model, the hairlines and gaps narrower.
  const chip = (
    <>
      <Icon name="arrowLeftRight" size={12} className="shrink-0" />
      <span className="max-w-full min-w-0 shrink-0 truncate max-sm:hidden">{label}</span>
      <span className="max-w-full min-w-0 shrink-0 truncate sm:hidden">{`${agentName(sw.from)} → ${agentName(sw.to)}`}</span>
      {model && (
        <>
          <span aria-hidden className="shrink-0 opacity-50 max-sm:hidden">·</span>
          <span className="min-w-0 truncate max-sm:hidden" title={sw.model} data-engine-model>{model}</span>
        </>
      )}
      <span aria-hidden className="shrink-0 opacity-50">·</span>
      <span className="min-w-0 truncate" data-engine-badge>{badge}</span>
      {has && <Icon name="chevronRight" size={12} className="shrink-0 transition-transform" style={{ transform: open ? "rotate(90deg)" : undefined }} />}
    </>
  );
  const pill = "flex min-w-0 items-center gap-1.5 rounded-full border border-line-strong bg-page/50 px-2.5 py-1 text-[11px] text-ink-3";
  return (
    <div className="w-full py-2" data-engine-switch={sw.resumed ? "resumed" : "summary"}>
      <div className="flex w-full min-w-0 items-center gap-3 select-none max-sm:gap-2">
        {/* the line is the separator; the chip after it is the control that opens the handoff */}
        <span role="separator" aria-label={label} className="h-px min-w-4 flex-1 bg-line-strong max-sm:min-w-2" />
        {has ? (
          <button
            type="button"
            onClick={() => {
              if (!HandoffPanel) fetchPanel();
              setOpen((o) => !o);
            }}
            aria-expanded={open}
            aria-controls={panel}
            title={t("chat-lines.engine_handoff_view")}
            className={`${pill} touch-hit cursor-pointer outline-none transition-colors hover:bg-hover hover:text-ink-2 focus-visible:ring-2 focus-visible:ring-accent`}
          >
            {chip}
          </button>
        ) : (
          <span className={pill}>{chip}</span>
        )}
        <span aria-hidden className="h-px min-w-4 flex-1 bg-line-strong max-sm:min-w-2" />
      </div>
      {has && (
        <Collapse open={open && Boolean(HandoffPanel)} lazy>
          {HandoffPanel && <HandoffPanel id={panel} sw={sw} item={item} mid={mid} sid={sid} />}
        </Collapse>
      )}
    </div>
  );
}
