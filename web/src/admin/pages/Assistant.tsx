// "Assistant cards" (docs/operations/admin.md "Assistant cards", docs/assistant/assistant-views.md "Prompts"): the main
// conversation's rich UI, always on (#5509), what each app does with it today, and a preview of the example cards.
// The preview is local only: it draws the synthetic examples with the extensions' components, a press or a form only
// changes this page's state, and an html card shows its text fallback (no model page runs here). It never calls the hub.
import { useState } from "react";
import { t } from "../../i18n";
import { Button } from "../../ui/atoms";
import { ViewSections } from "../../views/extensions/ViewBody";
import { fieldValues, type UIAction, type UIItem } from "../../lib/extension-ui";
import { example, EXAMPLE_ORDER, previewAction, submitSummary, type ExampleId } from "../assistant-examples";
import { AdminIcon } from "../icons";
import { devDialog } from "../router";
import { Card, Line, Notice, PageHeader, Segmented, Tag, type Tone } from "../parts";

export function Assistant() {
  return (
    <>
      <PageHeader title={t("admin-pages-assistant.title")} subtitle={t("admin-pages-assistant.subtitle")} />
      <SupportCard />
      <PreviewCard />
    </>
  );
}

const SUPPORT: { key: string; tone: Tone }[] = [
  { key: "web", tone: "green" },
  { key: "apple", tone: "green" },
  { key: "android", tone: "orange" },
  { key: "watch", tone: "gray" },
];

function SupportCard() {
  return (
    <Card title={t("admin-pages-assistant.support_title")} footer={t("admin-pages-assistant.support_footer")}>
      {SUPPORT.map((s) => (
        <Line key={s.key} label={t(`admin-pages-assistant.support_${s.key}`)} hint={t(`admin-pages-assistant.support_${s.key}_hint`)}>
          <Tag tone={s.tone}>{t(`admin-pages-assistant.support_${s.key}_tag`)}</Tag>
        </Line>
      ))}
    </Card>
  );
}

function firstExample(): ExampleId {
  const d = devDialog().replace(/^example-/, "");
  return (EXAMPLE_ORDER as readonly string[]).includes(d) ? (d as ExampleId) : "decision";
}

function PreviewCard() {
  const [pick, setPick] = useState<ExampleId>(firstExample);
  const [updated, setUpdated] = useState(false);
  const name = pick === "progress" && updated ? "progress-updated" : pick;
  return (
    <Card
      title={t("admin-pages-assistant.preview_title")}
      right={
        <Tag tone="accent">
          <AdminIcon name="lock" size={11} />
          {t("admin-pages-assistant.preview_local")}
        </Tag>
      }
      footer={t("admin-pages-assistant.preview_footer")}
    >
      <div className="flex flex-col gap-3 p-3">
        <Segmented
          className="self-start"
          label={t("admin-pages-assistant.preview_pick")}
          value={pick}
          options={EXAMPLE_ORDER.map((id) => ({ value: id, label: t(`admin-pages-assistant.example_${id}`) }))}
          onChange={(id) => (setPick(id), setUpdated(false))}
        />
        {pick === "progress" && (
          <Segmented
            className="self-start"
            label={t("admin-pages-assistant.progress_pick")}
            value={updated ? "after" : "before"}
            options={[
              { value: "before", label: t("admin-pages-assistant.progress_before") },
              { value: "after", label: t("admin-pages-assistant.progress_after") },
            ]}
            onChange={(v) => setUpdated(v === "after")}
          />
        )}
        <Preview key={name} name={name} />
      </div>
    </Card>
  );
}

/** One example, with its own local state: the fields' values and what a press would have sent. */
export function Preview({ name }: { name: string }) {
  const ex = example(name);
  const start = () => fieldValues(ex?.view);
  const [values, setValues] = useState<Record<string, string>>(start);
  const [sent, setSent] = useState<string[]>([]);
  const [sends, setSends] = useState(false);
  if (!ex) return <p className="px-1 text-[12px] text-red">{t("admin-pages-assistant.example_missing")}</p>;
  // Replies show their text and fields; navigation only describes its target. Nothing is sent or opened.
  const press = (a: UIAction) => {
    const result = previewAction(ex.raw, a.id);
    if (!result) return;
    setSends(result.sends);
    setSent([result.text, ...(result.sends && a.submit && ex.view ? submitSummary(ex.view, values) : [])]);
  };
  const choose = (item: UIItem, value: string) => item.name && setValues((v) => ({ ...v, [item.name!]: value }));
  return (
    <div className="grid gap-3 md:grid-cols-[minmax(0,420px)_minmax(0,1fr)]" data-preview={name}>
      <div className="min-w-0 rounded-card bg-page p-3 shadow-hairline">
        <div className="mb-2 flex items-center gap-1.5 px-1 text-[11px] text-ink-3">
          <AdminIcon name="spark" size={12} />
          <span className="truncate">{t("admin-pages-assistant.preview_frame")}</span>
        </div>
        {ex.view ? (
          <div className="card-ui flex flex-col gap-3 text-[13px] leading-normal" data-assistant-card="persona-view">
            {ex.title && <p className="px-1 text-[13px] font-semibold text-ink">{ex.title}</p>}
            <ViewSections
              view={ex.view}
              compact
              values={values}
              setValue={(n, v) => setValues((cur) => ({ ...cur, [n]: v }))}
              busy=""
              onPress={press}
              onChoose={choose}
            />
          </div>
        ) : (
          <div className="flex flex-col gap-2 rounded-card bg-surface p-3 shadow-card">
            <div className="flex flex-wrap items-center gap-2">
              <p className="min-w-0 flex-1 truncate text-[13px] font-semibold text-ink">{ex.title}</p>
              <Tag tone="orange">{t("admin-pages-assistant.html_fallback_tag")}</Tag>
            </div>
            <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-ink-2">{ex.fallback}</p>
            <p className="text-[11.5px] leading-relaxed text-ink-3">{t("admin-pages-assistant.html_not_run")}</p>
          </div>
        )}
      </div>
      <div className="flex min-w-0 flex-col gap-3 [&>[role=status]]:mb-0">
        {sent.length > 0 ? (
          <Notice
            tone="accent"
            icon="info"
            title={t(sends ? "admin-pages-assistant.would_send_title" : "admin-pages-assistant.preview_local")}
            action={
              <Button tone="quiet" icon="undo" onClick={() => (setSent([]), setValues(start()))}>
                {t("admin-pages-assistant.reset")}
              </Button>
            }
          >
            <span className="block break-words">{sends ? t("admin-pages-assistant.would_send", { text: sent[0] }) : sent[0]}</span>
            {sent.slice(1).map((line, i) => (
              <span key={i} className="block break-words">
                {line}
              </span>
            ))}
          </Notice>
        ) : (
          <p className="px-1 text-[12px] leading-relaxed text-ink-3">{ex.html ? t("admin-pages-assistant.html_hint") : t("admin-pages-assistant.press_hint")}</p>
        )}
        <div className="rounded-card bg-inset px-3.5 py-3 shadow-hairline">
          <div className="mb-1 text-[11.5px] font-medium text-ink-3">{t("admin-pages-assistant.fallback_title")}</div>
          <p className="whitespace-pre-wrap text-[12.5px] leading-relaxed text-ink-2">{ex.fallback}</p>
        </div>
      </div>
    </div>
  );
}
