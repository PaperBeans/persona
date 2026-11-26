// A stored Health conversation in the default panel while its module is off. This is deliberately separate from
// ChatView: useTranscript's readOnly mode never subscribes as the visible writer, and no composer, pending-answer card,
// fork, rewind, rename, archive or delete action is mounted. Transcript rows and ChatSearch stay the real ones.
import { useEffect, useMemo, useRef, useState } from "react";
import { lineItemIds, type ChatSearchHit } from "../../lib/chat-search";
import { ReadOnlyFileContext } from "../../lib/panel";
import { useStore } from "../../lib/store";
import { chatLines } from "../../lib/turns";
import { Button, Loading } from "../../ui/atoms";
import { conversationTitle } from "../assistant/conversations";
import { Empty } from "../assistant/parts";
import { ChatLineView } from "./Lines";
import { ReadOnlyHeader } from "./ReadOnlyHeader";
import { ChatSearch, type LoadOutcome } from "./ChatSearch";
import { useTranscript } from "./useTranscript";
import { t } from "../../i18n";

export function ReadOnlyChatView({ mid, sid, onToggleSidebar, headerActions }: { mid: string; sid: string; onToggleSidebar: () => void; headerActions?: React.ReactNode }) {
  const session = useStore((s) => (s.sessions[mid] ?? []).find((x) => x.session_id === sid));
  const tr = useTranscript(mid, sid, { assistant: true, readOnly: true, updatedAt: session?.updated_at, rev: session?.rev });
  const lines = useMemo(() => chatLines(tr.items), [tr.items]);
  const [search, setSearch] = useState({ open: false, query: "" });
  const scroller = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = scroller.current;
    if (el && tr.firstRead) el.scrollTop = el.scrollHeight;
  }, [sid, tr.firstRead]);
  const pick = (hit: ChatSearchHit) => {
    setSearch((s) => ({ ...s, open: false }));
    const line = lines.find((l) => lineItemIds(l).includes(hit.itemId));
    if (line) requestAnimationFrame(() => document.querySelector(`[data-line="${CSS.escape(line.id)}"]`)?.scrollIntoView({ block: "center" }));
  };
  const loadEarlierForSearch = async (onPage: (n: number) => void): Promise<LoadOutcome> => {
    const read = await tr.loadEarlier({ waitForRetry: true });
    if (read) onPage(1);
    return null;
  };
  return (
    <div className="flex h-full min-w-0 flex-col" data-testid="health-readonly-chat">
      <ReadOnlyHeader title={session ? conversationTitle(session) : "Health"} actions={headerActions} onToggleSidebar={onToggleSidebar} onSearch={() => setSearch((s) => ({ ...s, open: true }))} />
      <div ref={scroller} data-chat-scroll className="min-h-0 flex-1 overflow-y-auto">
        <div data-chat-content className="mx-auto flex w-full max-w-[880px] flex-col gap-3.5 px-5 pt-7 pb-10 sm:px-8">
          {tr.hasMore && (
            <div className="flex justify-center">
              <Button tone="quiet" busy={tr.loadingEarlier} onClick={() => void tr.loadEarlier()}>{t("spaces.load_earlier")}</Button>
            </div>
          )}
          {tr.loading && !tr.items.length && <div className="flex justify-center py-16"><Loading label={t("chat-chatview.loading_chat")} /></div>}
          {tr.error && !tr.items.length && (
            <div className="flex flex-col items-center gap-3 py-16 text-center">
              <p className="text-meta text-ink-2">{tr.error}</p>
              <Button onClick={tr.reload} icon="retry">{t("chat-chatview.retry")}</Button>
            </div>
          )}
          {!tr.loading && !tr.error && !tr.items.length && <Empty icon="heart" title={t("spaces.empty_title")}>{t("spaces.empty_readonly_body")}</Empty>}
          <ReadOnlyFileContext.Provider value={true}>
            {lines.map((line) => <ChatLineView key={line.id} line={line} mid={mid} sid={sid} assistant expandFold={tr.expandFold} />)}
          </ReadOnlyFileContext.Provider>
        </div>
      </div>
      <div role="status" data-chat-readonly className="flex shrink-0 items-center justify-center border-t border-line px-4 py-3 text-label text-ink-3">
        {t("chat-chatview.view_only")}
      </div>
      <ChatSearch
        open={search.open}
        query={search.query}
        onQuery={(query) => setSearch((s) => ({ ...s, query }))}
        items={tr.items}
        rounds={lines.filter((line) => line.kind === "segment").length}
        hasMore={tr.hasMore}
        onLoadEarlier={loadEarlierForSearch}
        onPick={pick}
        onClose={() => setSearch((s) => ({ ...s, open: false }))}
      />
    </div>
  );
}
