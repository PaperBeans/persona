// The default panel's read-only view of data the Health space already stored (#13430). It shares only Core API and
// parsing helpers with the enabled module: no module chunk, catalog, upload, edit or delete path is reached here.
import { useCallback, useEffect, useMemo, useState } from "react";
import { api, enc, errorText, fileDownloadURL } from "../../lib/api";
import { bytes } from "../../lib/format";
import { healthReportsPath, type HealthReport } from "../../lib/health-reports";
import { memoryEntries, type MemoryEntry, type MemoryFile } from "../../lib/memory";
import { onHubEvent } from "../../lib/store";
import { Button, Dialog, Loading } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { SectionTitle } from "../assistant/parts";
import { t } from "../../i18n";

export function HealthReadOnlyData({ mid, unavailable = "" }: { mid?: string; unavailable?: string }) {
  return (
    <>
      <ReadOnlyReports mid={mid} unavailable={unavailable} />
      <ReadOnlyMemory />
    </>
  );
}

function ReadOnlyReports({ mid, unavailable }: { mid?: string; unavailable: string }) {
  const [list, setList] = useState<HealthReport[] | null>(null);
  const [failure, setFailure] = useState("");
  const [open, setOpen] = useState<HealthReport | null>(null);
  const error = failure || (mid ? "" : unavailable);
  const load = useCallback(async () => {
    if (!mid) return;
    try {
      const r = await api<{ reports?: HealthReport[] }>(healthReportsPath(mid));
      setList(r.reports ?? []);
      setFailure("");
    } catch (e) {
      setFailure(errorText(e));
    }
  }, [mid]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => onHubEvent("hello", () => void load()), [load]);

  return (
    <section className="mt-7" data-testid="health-readonly-reports">
      <SectionTitle right={list?.length ? <span className="text-submeta text-ink-3 tabular-nums">{t("spaces.report_count", { count: list.length })}</span> : undefined}>{t("spaces.reports")}</SectionTitle>
      <div className="overflow-hidden rounded-card bg-surface shadow-card">
        {list === null && !error ? <div className="flex justify-center py-6"><Loading /></div> : !list?.length ? (
          <p className="px-4 py-4 text-meta leading-relaxed text-ink-3">{error ? t("spaces.reports_load_failed", { error }) : t("spaces.reports_empty")}</p>
        ) : (
          <div className="divide-y divide-line">
            {list.map((r) => (
              <button key={r.name} type="button" onClick={() => setOpen(r)} className="flex w-full min-w-0 items-center gap-3 px-4 py-2.5 text-left transition-colors hover:bg-hover">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-inset text-ink-2"><Icon name="file" size={13} /></span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-meta font-medium text-ink">{r.title}</span>
                  <span className="block truncate text-label text-ink-3">{r.record_path ? t("spaces.report_recorded") : t("spaces.report_unrecorded")} · {bytes(r.size)}</span>
                </span>
                <span className="shrink-0 text-submeta text-ink-3 tabular-nums">{new Date(r.modified_at).toLocaleDateString(undefined, { year: "numeric", month: "numeric", day: "numeric" })}</span>
              </button>
            ))}
          </div>
        )}
      </div>
      {open && mid && <ReadOnlyReportDialog key={open.name} mid={mid} report={open} onClose={() => setOpen(null)} />}
    </section>
  );
}

function ReadOnlyReportDialog({ mid, report, onClose }: { mid: string; report: HealthReport; onClose: () => void }) {
  const [record, setRecord] = useState<string | null>(report.record_path ? null : "");
  const [readError, setReadError] = useState("");
  useEffect(() => {
    if (!report.record_path) return;
    let live = true;
    api<{ text?: string }>(`/machines/${enc(mid)}/file?path=${enc(report.record_path)}`)
      .then((f) => { if (live) setRecord(f.text ?? ""); })
      .catch((e) => { if (live) setReadError(errorText(e)); });
    return () => { live = false; };
  }, [mid, report.record_path]);
  return (
    <Dialog
      open
      width={640}
      onClose={onClose}
      title={report.title}
      footer={
        <>
          <span className="flex-1" />
          <Button tone="quiet" onClick={onClose}>{t("spaces.close")}</Button>
          {report.path && <Button tone="primary" href={fileDownloadURL(mid, report.path, undefined, { inline: true })}>{t("spaces.report_open")}</Button>}
        </>
      }
    >
      {readError ? <p className="text-meta leading-relaxed text-ink-3">{t("spaces.reports_load_failed", { error: readError })}</p>
        : record === null ? <div className="flex justify-center py-6"><Loading /></div>
        : record ? <div className="max-h-[60vh] overflow-y-auto"><Markdown text={record} small mid={mid} readOnly /></div>
        : <p className="text-meta leading-relaxed text-ink-3">{t("spaces.report_no_record")}</p>}
    </Dialog>
  );
}

function ReadOnlyMemory() {
  const [files, setFiles] = useState<MemoryFile[] | null>(null);
  const [error, setError] = useState("");
  const [open, setOpen] = useState<MemoryEntry | null>(null);
  const load = useCallback(async () => {
    try {
      const r = await api<{ files?: MemoryFile[] }>("/assistant/memory?space=health");
      setFiles(r.files ?? []);
      setError("");
    } catch (e) {
      setError(errorText(e));
    }
  }, []);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => onHubEvent("hello", () => void load()), [load]);
  const entries = useMemo(() => memoryEntries(files ?? []), [files]);
  return (
    <section className="mt-7" data-testid="health-readonly-memory">
      <SectionTitle right={entries.length ? <span className="text-submeta text-ink-3 tabular-nums">{t("spaces.memory_count", { count: entries.length })}</span> : undefined}>{t("spaces.memory")}</SectionTitle>
      <div className="overflow-hidden rounded-card bg-surface shadow-card">
        {files === null && !error ? <div className="flex justify-center py-6"><Loading /></div> : !entries.length ? (
          <p className="px-4 py-4 text-meta leading-relaxed text-ink-3">{error ? t("spaces.memory_load_failed", { error }) : t("spaces.memory_empty")}</p>
        ) : (
          <div className="divide-y divide-line">
            {entries.map((e) => (
              <button key={e.file.name} type="button" onClick={() => setOpen(e)} className="flex w-full min-w-0 flex-col items-start px-4 py-2.5 text-left transition-colors hover:bg-hover">
                <span className="w-full truncate text-meta font-medium text-ink">{e.title}</span>
                {e.detail && <span className="w-full truncate text-label text-ink-3">{e.detail}</span>}
              </button>
            ))}
          </div>
        )}
      </div>
      <Dialog open={open !== null} onClose={() => setOpen(null)} title={open?.title} footer={<Button tone="quiet" onClick={() => setOpen(null)}>{t("spaces.close")}</Button>}>
        <p className="whitespace-pre-wrap text-meta leading-relaxed text-ink-2">{open?.body}</p>
      </Dialog>
    </section>
  );
}
