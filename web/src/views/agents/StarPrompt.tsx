// The GitHub star prompts of the Agents pages (#5761, lib/star-prompt.ts, docs/design/DESIGN_SYSTEM.md "GitHub star
// prompts"): the pill at the foot of the empty start area, and the line in the hub notice's slot once this browser has
// started enough Agents. Both open the project in a new tab, where the user's GitHub session is.
import { hideStarPill, PERSONA_REPO_URL, showsStarCard, showsStarPill, starPromptLater, starPromptStarred, useStarPrompt } from "../../lib/star-prompt";
import { runtimeNeedsYou } from "../../lib/runtime-list";
import { useStore, type State } from "../../lib/store";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

/** On the Agents page's start area (nothing open), pinned to its foot: Star on GitHub, and ✕ to hide it for good. */
export function StarPill() {
  const prompt = useStarPrompt();
  if (!showsStarPill(prompt)) return null;
  return (
    <div data-testid="star-pill" className="pointer-events-none absolute inset-x-0 bottom-4 flex justify-center px-4">
      <div className="pointer-events-auto flex items-center rounded-full bg-surface text-xs text-ink-2 shadow-hairline" style={{ animation: "fade-in 200ms ease-out both" }}>
        <a href={PERSONA_REPO_URL} target="_blank" rel="noopener noreferrer" onClick={starPromptStarred}
          className="touch-hit flex items-center gap-1.5 whitespace-nowrap rounded-full py-1 pr-1.5 pl-3 transition-colors hover:text-ink">
          <Icon name="star" size={12} />
          {t("lib-star-prompt.star")}
        </a>
        <button type="button" data-testid="star-pill-hide" aria-label={t("lib-star-prompt.hide")} title={t("lib-star-prompt.hide")} onClick={hideStarPill}
          className="touch-hit mr-1 shrink-0 rounded-full p-1 text-ink-3 hover:bg-hover hover:text-ink-2">
          <Icon name="x" size={11} stroke={2} />
        </button>
      </div>
    </div>
  );
}

/** Nothing waits for the user: no pending card and no session in Needs you, so the line pushes no waiting row down. */
const nothingWaits = (s: State) => s.interactions.length === 0 && !Object.values(s.sessions).some((list) => list.some(runtimeNeedsYou));

/** The milestone line under the hub notice, as quiet as it: from the threshold, while nothing waits and no search is on. */
export function StarPromptLine({ searching = false }: { searching?: boolean }) {
  const prompt = useStarPrompt();
  const idle = useStore(nothingWaits);
  if (searching || !idle || !showsStarCard(prompt, Date.now())) return null;
  // The words whole and the action under them: beside them, the sidebar's width cut the sentence short.
  return (
    <div data-testid="star-prompt" className="relative z-10 mt-1.5 flex items-start gap-1 rounded-[7px] px-2 py-1.5 hover:bg-hover">
      <span className="flex min-w-0 flex-1 items-start gap-2">
        <span className="mt-[3px] flex size-3.5 shrink-0 items-center justify-center text-accent-ink"><Icon name="star" size={13} stroke={2.2} /></span>
        <span className="flex min-w-0 flex-1 flex-col items-start gap-1">
          <span className="text-sm leading-[1.45] text-ink-2">{t("lib-star-prompt.card")}</span>
          <a href={PERSONA_REPO_URL} target="_blank" rel="noopener noreferrer" data-testid="star-prompt-star" onClick={starPromptStarred}
            className="touch-hit -ml-2 whitespace-nowrap rounded-control px-2 py-0.5 text-xs font-medium text-accent-ink hover:bg-hover">
            {t("lib-star-prompt.star")}
          </a>
        </span>
      </span>
      <button type="button" data-testid="star-prompt-later" aria-label={t("lib-star-prompt.later")} title={t("lib-star-prompt.later")} onClick={starPromptLater}
        className="touch-hit -mt-0.5 shrink-0 rounded-control p-1 text-ink-3 hover:bg-hover hover:text-ink-2">
        <Icon name="x" size={12} stroke={2} />
      </button>
    </div>
  );
}
