// Thin first-screen shell for assistant cards (docs/assistant/assistant-views.md): keeps the exported constants,
// contexts and openTargetOf in place while deferring the card renderer (ui/AssistantCardBody.tsx) and its static
// dependencies (lib/assistant-view.ts, lib/assistant-html.ts, lib/assistant-views.ts, lib/extension-ui.ts) off the
// cold first screen and outside the idle preload tier.
// Keeps the side-effect import of views/extensions/view-sections-lazy.tsx on the first screen so ViewBody and
// ExtensionChart stay registered for the idle preload (#3614) and Rollup keeps ExtensionChart out of the admin chunk
// with zero impact on IDLE_TIER_BUDGET.
import { createContext, Suspense } from "react";
import { openTarget, type OpenTarget } from "../lib/assistant-blocks";
import type { AssistantView } from "../lib/extension-wire";
import { openTask } from "../lib/handoff";
import { lazyPage } from "../lib/lazy-page";
import { openLink } from "../lib/links";
import { openPanel } from "../lib/panel";
import { navigate } from "../lib/router";
import "../views/extensions/view-sections-lazy";
import { t } from "../i18n";
import { toast } from "./atoms";

/** The fenced blocks drawn as cards. */
export const CARD_LANGUAGES = ["persona-view", "persona-card"];

/** The hub feature that says card answers, live cards and registered blocks are there (docs/assistant/assistant-views.md). */
export const VIEWS_FEATURE = "assistant_views";

/** The assistant conversation the cards drawn below belong to (none: an agent's chat, where nothing acts). */
export const CardConversation = createContext<string>("");
/** The text item a block is drawn from (none while it streams: drawn, nothing acts). */
export const CardItem = createContext<{ id: string; text: string } | null>(null);

/** Whether html components run here: matches WEB_HTML_RUNTIME_VERIFIED (dev pages and tests may set it for themselves). */
export const HtmlRuntime = createContext<boolean>(true);

const AssistantCardBody = lazyPage(() => import("./AssistantCardBody").then((m) => m.AssistantCardBody));
const LiveCardBody = lazyPage(() => import("./AssistantCardBody").then((m) => m.LiveCardBody));

export function AssistantCard(props: { lang: string; code: string; fallback: React.ReactNode }) {
  return (
    <Suspense fallback={null}>
      <AssistantCardBody {...props} />
    </Suspense>
  );
}

/** A live card of the feed (kind view): the newest version the hub sent, drawn in place. */
export function LiveCard(props: { conversation: string; id: string; first?: AssistantView }) {
  return (
    <Suspense fallback={null}>
      <LiveCardBody {...props} />
    </Suspense>
  );
}

/** Opens what an open action names, inside the app; a target the web does not open says so, never the system.
 *  `manager`: the chat's the card is in (openLink). */
export function openTargetOf(target: string, manager?: string) {
  const to: OpenTarget | undefined = openTarget(target);
  switch (to?.kind) {
    case "web":
      openLink(to.url, undefined, manager);
      return;
    case "session":
      navigate({ name: "session", mid: to.mid, sid: to.sid });
      return;
    case "task":
      openTask(to.id);
      return;
    case "kb":
      navigate({ name: "kb", kind: to.path[0], id: to.path.slice(1).join("/") || undefined });
      return;
    case "file":
      openPanel({ kind: "file", mid: to.mid, path: to.path });
      return;
    default:
      toast(t("ui-assistantcard.cannot_open"), "error");
  }
}
