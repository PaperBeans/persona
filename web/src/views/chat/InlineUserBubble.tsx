import { useId, useMemo, useState, type ReactNode } from "react";
import { mentionLabel } from "../../lib/inline-mentions";
import { readInlineBody } from "../../lib/inline-message";
import type { Context } from "../../lib/turns";
import { Icon } from "../../ui/icons";
import { PathText } from "../../ui/PathLinks";
import { ContextDetails } from "./ContextDetails";
import { referenceIcon } from "./reference-icon";
import "./inline-reference.css";

export interface InlineBubbleContent {
  remaining: Context[];
  words: ReactNode;
  details: ReactNode;
}

/** The text-only bubble and its original disclosures also work while this chunk is loading. */
export default function InlineUserBubble({ body, contexts, mid, cwd, render }: {
  body: string;
  contexts: Context[];
  mid: string;
  cwd?: string;
  render: (inline?: InlineBubbleContent) => ReactNode;
}) {
  const { document, used } = useMemo(() => readInlineBody(body, contexts), [body, contexts]);
  const remaining = useMemo(() => contexts.filter((context) => !used.has(context)), [contexts, used]);
  const [open, setOpen] = useState<number>();
  const previewId = useId();
  if (!used.size) return render();
  const selected = open === undefined ? undefined : document[open];
  const detail = typeof selected === "object" ? selected : undefined;
  const words = document.map((part, i) => {
    if (typeof part === "string") return <PathText key={i} text={part} mid={mid} cwd={cwd} />;
    const label = mentionLabel(part);
    return (
      <button key={i} type="button" className="inline-reference inline-flex max-w-full items-center gap-1 rounded-chip align-baseline font-medium"
        title={label} aria-expanded={open === i} aria-controls={previewId} onClick={() => setOpen((current) => current === i ? undefined : i)}>
        <Icon name={referenceIcon(part)} size={14} className="shrink-0" />
        <span className="min-w-0 truncate">{label}</span>
      </button>
    );
  });
  return render({
    remaining,
    words,
    details: <div id={previewId} className="w-full" hidden={!detail}>{detail && <ContextDetails contexts={[detail]} />}</div>,
  });
}
