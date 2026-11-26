// The composer's send history (issue #2434, after Moshi): ↑ / ↓ walk the messages sent from this conversation when the
// box is empty or still shows one of them, Ctrl+R opens a search over the messages sent from any conversation. A hook
// of its own, so Composer.tsx only forwards a key and tells it what was sent. Desktop only: a touch screen has no
// Ctrl+R, and its arrows do not exist. While the box is editing the last message (#4503) and still shows it untouched,
// that message counts as the history entry shown: ↑ goes on to the older ones and ↓ back to an empty box, both through
// `setDraft`, which ends the edit (Composer).
import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { hasFinePointer } from "../../lib/viewport";
import { canRecall, opensSearch, readHistory, recallIndexOf, recallStep, recordSent } from "../../lib/send-history";
import { skillDraft, splitSkillDraft } from "../../lib/skill-mentions";
import { editorText, inlineMessage, readInlineDraft } from "../../lib/inline-mentions";
import type { InlineMentionInputHandle } from "./InlineMentionInput";

export interface SendHistory {
  /** The search panel is open. */
  searching: boolean;
  /** Offers a key of the text box; true: it was ours and is handled. */
  onKeyDown: (e: React.KeyboardEvent<HTMLElement>) => boolean;
  /** A message went out. */
  record: (text: string) => void;
  /** A row of the search was chosen: it lands in the box (not sent). */
  pick: (text: string) => void;
  closeSearch: () => void;
}

// History keeps local draft IDs. Transcript edits have the same anchors even if a hub enriched a context body.
function editableIdentity(raw: string) {
  const { skills, text } = splitSkillDraft(raw);
  const doc = readInlineDraft(text);
  return skillDraft(skills, (inlineMessage()?.inlineBody(doc) ?? text).trim());
}

export function useSendHistory(o: {
  draftKey: string;
  draft: string;
  editorText: string;
  setDraft: (text: string) => void;
  setCaret: (at: number) => void;
  input: RefObject<InlineMentionInputHandle | null>;
  disabled?: boolean;
  /** The words the box got when editing the last message began; null when it is not editing. */
  editing?: string | null;
}): SendHistory {
  const { draftKey, draft, setDraft, setCaret, input, disabled, editing } = o;
  const [searching, setSearching] = useState(false);
  // the history message the box shows now, and where it is in the list
  const shown = useRef<{ index: number; text: string } | null>(null);
  useEffect(() => {
    shown.current = null;
    setSearching(false);
  }, [draftKey]);

  const putCaretAtEnd = useCallback(
    (text: string) => {
      const length = editorText(readInlineDraft(splitSkillDraft(text).text)).length;
      setCaret(length);
      requestAnimationFrame(() => {
        input.current?.focus();
        input.current?.setSelectionRange(length, length);
      });
    },
    [input, setCaret],
  );

  const onKeyDown = (e: React.KeyboardEvent<HTMLElement>): boolean => {
    if (disabled || !hasFinePointer()) return false;
    if (opensSearch(e)) {
      e.preventDefault();
      setSearching(true);
      return true;
    }
    if ((e.key !== "ArrowUp" && e.key !== "ArrowDown") || e.shiftKey || e.ctrlKey || e.altKey || e.metaKey) return false;
    const dir = e.key === "ArrowUp" ? "up" : "down";
    if (draft === "") shown.current = null;
    const [start, end] = input.current?.selection() ?? [0, 0];
    if (start !== end) return false;
    const editingThis = editing != null && draft === editing;
    if (editingThis && shown.current?.text !== draft) {
      const list = readHistory(draftKey);
      const identity = editableIdentity(draft);
      shown.current = { index: recallIndexOf(list.map((entry) => ({ ...entry, text: editableIdentity(entry.text) })), identity), text: draft };
    }
    const ok = canRecall({
      draft,
      shown: shown.current?.text ?? null,
      dir,
      firstLine: !o.editorText.slice(0, start).includes("\n"),
      lastLine: !o.editorText.slice(end).includes("\n"),
    });
    if (!ok) return false;
    const list = readHistory(draftKey);
    const at = shown.current?.index ?? -1;
    // an edit with nothing sent from here before it: ↓ still goes back to an empty box
    if ((list.length === 0 && !(editing != null && dir === "down")) || (at < 0 && dir === "down")) return false;
    const next = recallStep(list.length, at, dir);
    e.preventDefault();
    if (next < 0) {
      shown.current = null;
      setDraft("");
      setCaret(0);
      return true;
    }
    const text = list[next].text;
    // an edit with no older message: it stays an edit
    if (editingThis && editableIdentity(text) === editableIdentity(draft)) {
      shown.current = { index: next, text };
      return true;
    }
    shown.current = { index: next, text };
    setDraft(text);
    putCaretAtEnd(text);
    return true;
  };

  return {
    searching,
    onKeyDown,
    record: (text) => {
      shown.current = null;
      recordSent(draftKey, text);
    },
    pick: (text) => {
      shown.current = null;
      setSearching(false);
      setDraft(text);
      putCaretAtEnd(text);
    },
    closeSearch: () => {
      setSearching(false);
      requestAnimationFrame(() => input.current?.focus());
    },
  };
}
