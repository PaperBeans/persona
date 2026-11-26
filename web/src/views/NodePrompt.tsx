// The two small dialogs the palette's node commands ask with (lib/node-palette.ts): a name for a rename, a yes for a
// close. Mounted once, with the sidebar.
import { useState, useSyncExternalStore } from "react";
import { answerAsk, getAsk, subscribeAsk } from "../lib/node-ask";
import { Button, Dialog } from "../ui/atoms";
import { t } from "../i18n";

export function NodePrompt() {
  const ask = useSyncExternalStore(subscribeAsk, getAsk, getAsk);
  if (!ask) return null;
  // a new question starts over (its own text)
  return ask.kind === "text" ? <TextAsk key={ask.title + ask.value} ask={ask} /> : <ConfirmAsk ask={ask} />;
}

function TextAsk({ ask }: { ask: Extract<NonNullable<ReturnType<typeof getAsk>>, { kind: "text" }> }) {
  const [text, setText] = useState(ask.value);
  const done = (v: string | null) => {
    answerAsk();
    ask.resolve(v);
  };
  const next = text.trim();
  return (
    <Dialog
      open
      onClose={() => done(null)}
      title={ask.title}
      width={380}
      footer={
        <>
          <Button tone="quiet" onClick={() => done(null)}>
            {t("common.cancel")}
          </Button>
          <Button tone="primary" disabled={!next || next === ask.value.trim()} onClick={() => done(next)}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (next && next !== ask.value.trim()) done(next);
        }}
      >
        <label className="flex flex-col gap-1">
          <span className="text-[12px] text-ink-3">{ask.label}</span>
          <input
            value={text}
            autoFocus
            onFocus={(e) => e.currentTarget.select()}
            onChange={(e) => setText(e.target.value)}
            className="h-9 rounded-control bg-surface px-3 text-[13px] text-ink shadow-btn outline-none focus:shadow-[inset_0_0_0_1px_var(--accent)]"
          />
        </label>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

function ConfirmAsk({ ask }: { ask: Extract<NonNullable<ReturnType<typeof getAsk>>, { kind: "confirm" }> }) {
  const done = (v: boolean) => {
    answerAsk();
    ask.resolve(v);
  };
  return (
    <Dialog
      open
      onClose={() => done(false)}
      title={ask.title}
      width={380}
      footer={
        <>
          <Button tone="quiet" onClick={() => done(false)}>
            {t("common.cancel")}
          </Button>
          <Button tone="danger" onClick={() => done(true)}>
            {ask.label}
          </Button>
        </>
      }
    >
      <p className="text-[13px] leading-relaxed text-ink-2">{ask.body}</p>
    </Dialog>
  );
}
