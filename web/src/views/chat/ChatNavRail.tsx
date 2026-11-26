import { lazy, memo, Suspense, useCallback, useMemo, useRef, useState } from "react";
import { indexedNavTicks, navRailTicks } from "../../lib/nav-rail";
import type { QuestionMark } from "../../lib/question-states";
import { chatLines, type ChatLine } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { userInputRevision, type UserInputPosition } from "../../lib/user-input-index";
import { NavRail } from "./NavRail";
import { useInputPreviews } from "./useInputPreviews";
import { useUserInputs } from "./useUserInputs";

const QuestionMarksHost = lazy(() => import("./QuestionMarksHost"));

export interface IndexedJump {
  input: UserInputPosition;
  history: string;
  historyChanged: () => void;
}

/** The full index and preview cache load with the rail. The transcript keeps its own live read. */
export const ChatNavRail = memo(function ChatNavRail({ mid, sid, lines, items, retained, history, replaceEpoch, stretchStart, reload,
  scroller, onJump, questionsOn, loading, busy, retainedBusy }: {
  mid: string; sid: string; lines: ChatLine[]; items: Item[]; retained?: Item[];
  history?: string; replaceEpoch?: number; stretchStart?: number; reload: () => void;
  scroller: HTMLDivElement | null; onJump: (id: string, indexed?: IndexedJump) => Promise<boolean | void>;
  questionsOn: boolean; loading: boolean; busy?: string; retainedBusy?: boolean;
}) {
  const revision = useMemo(() => userInputRevision(retained ?? items), [retained, items]);
  const { scope, index, historyChanged } = useUserInputs(mid, sid, history, revision, !loading, reload, replaceEpoch);
  const inputIndex = useMemo(() => {
    if (!index || stretchStart === undefined) return index;
    // In A → B → A, previous stretches have their own rows and remain outside the live stretch's navigation.
    const inputs = index.inputs.filter((input) => input.seq >= stretchStart);
    return { ...index, total: inputs.length, inputs };
  }, [index, stretchStart]);
  const live = useMemo(() => retained ? { items: retained, lines: chatLines(retained) } : undefined, [retained]);
  const markLines = useMemo(() => {
    if (!live) return lines;
    const seen = new Set(lines.map((line) => line.id));
    return [...lines, ...live.lines.filter((line) => !seen.has(line.id))];
  }, [lines, live]);
  const lastBusy = retainedBusy ? live?.lines.findLast((line) => line.kind === "segment")?.id : busy;
  const ticks = useMemo(() => {
    return inputIndex ? indexedNavTicks(inputIndex, lines, items, live) : navRailTicks(lines);
  }, [inputIndex, lines, items, live]);
  const { loadPreview, previewAt } = useInputPreviews(mid, sid, scope, inputIndex, ticks, historyChanged);
  const [marks, setMarks] = useState<ReadonlyMap<string, QuestionMark>>();
  const questionOpened = useRef<((id: string) => void) | undefined>(undefined);
  const setOpened = useCallback((callback: ((id: string) => void) | undefined) => { questionOpened.current = callback; }, []);
  const openQuestion = useCallback(async (id: string) => {
    const input = inputIndex?.inputs.find((item) => id === `seg:${item.id}`);
    if (await onJump(id, input && inputIndex ? { input, history: inputIndex.history, historyChanged } : undefined)) {
      questionOpened.current?.(id);
    }
  }, [onJump, inputIndex, historyChanged]);
  return (
    <>
      {questionsOn && (
        <Suspense fallback={null}>
          <QuestionMarksHost mid={mid} sid={sid} on={!loading} lines={markLines} busy={lastBusy} ticks={ticks} scroller={scroller} onMarks={setMarks} onOpened={setOpened} />
        </Suspense>
      )}
      {ticks.length > 0 && <NavRail ticks={ticks} total={inputIndex?.total} scroller={scroller} onJump={openQuestion} reset={scope} marks={marks}
        onPreview={loadPreview} previewAt={previewAt} />}
    </>
  );
});
