import { useEffect, useId, useMemo, useRef, useState } from "react";
import { t } from "../../i18n";
import { Button, cx, IconButton } from "../../ui/atoms";
import { Card, Marked, SectionTitle } from "../../views/knowledge/KBParts";
import type { RecordingView } from "./recording-view";
import { exportTranscriptText, stepTranscriptMatch, transcriptMatchParts, transcriptMatches, transcriptRows, transcriptRowText, type TranscriptRow } from "./transcript-review";

function download(rows: TranscriptRow[], recordingId: string) {
  const link = document.createElement("a");
  const url = URL.createObjectURL(new Blob([exportTranscriptText(rows)], { type: "text/plain;charset=utf-8" }));
  link.href = url;
  link.download = `transcript-${recordingId.replace(/[^\p{L}\p{N}_-]/gu, "_").slice(0, 100)}.txt`;
  document.body.append(link);
  try { link.click(); } finally {
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
  }
}

export function RecordingTranscript({ rec, recordingId, query, highlighted, onJump }: {
  rec: RecordingView;
  recordingId: string;
  query?: string;
  highlighted: string;
  onJump: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [showBookmarks, setShowBookmarks] = useState(false);
  const toggle = useRef<HTMLButtonElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const panel = useId(), index = useId();
  const rows = useMemo(() => transcriptRows(rec), [rec]);
  const bookmarks = useMemo(() => rows.filter(row => row.bookmark), [rows]);
  const matches = useMemo(() => transcriptMatches(rows, search), [rows, search]);
  const matchIds = useMemo(() => new Set(matches.map(row => row.id)), [matches]);
  const position = selected ? matches.findIndex(row => row.id === selected) : -1;
  const hasQuery = open && !!search.trim();

  useEffect(() => { if (open) input.current?.focus({ preventScroll: true }); }, [open]);
  function close() {
    setOpen(false);
    setSearch("");
    setSelected(null);
    setShowBookmarks(false);
    toggle.current?.focus({ preventScroll: true });
  }
  function jump(direction: 1 | -1) {
    const id = stepTranscriptMatch(matches, selected, direction);
    if (!id) return;
    setSelected(id);
    onJump(id);
  }

  return <section onKeyDown={event => {
    if (open && event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      close();
    }
  }}>
    <SectionTitle title={t("recording.transcript")} count={rows.length - bookmarks.length}
      right={rows.length > 0 && <Button ref={toggle} tone="quiet" size="sm" className="touch-hit" icon="search" expanded={open}
        aria-controls={panel} onClick={() => open ? close() : setOpen(true)}>{t("recording.transcript_review")}</Button>} />
    <Card>
      {open && rows.length > 0 && <div id={panel} className="flex flex-col gap-2 px-3 py-2">
        <div className="flex items-center gap-1">
          <input ref={input} type="search" aria-label={t("recording.transcript_find")} placeholder={t("recording.transcript_find")}
            className="h-10 min-w-0 flex-1 rounded-control bg-inset px-3 text-meta text-ink shadow-hairline focus-visible:outline-accent"
            value={search} onChange={event => { setSearch(event.target.value); setSelected(null); }}
            onKeyDown={event => {
              if (event.key === "Enter" && !event.nativeEvent.isComposing) {
                event.preventDefault();
                jump(event.shiftKey ? -1 : 1);
                input.current?.focus({ preventScroll: true });
              }
            }} />
          <IconButton icon="chevronLeft" label={t("recording.transcript_previous")} disabled={!matches.length} onClick={() => jump(-1)} />
          <IconButton icon="chevronRight" label={t("recording.transcript_next")} disabled={!matches.length} onClick={() => jump(1)} />
        </div>
        <div className="flex flex-wrap items-center gap-1">
          {bookmarks.length > 0 && <Button tone="quiet" size="sm" expanded={showBookmarks} aria-controls={index}
            onClick={() => setShowBookmarks(!showBookmarks)}>{t("recording.transcript_bookmarks")}</Button>}
          <Button tone="quiet" size="sm" icon="download" onClick={() => download(rows, recordingId)}>{t("recording.transcript_download")}</Button>
          {hasQuery && <span role="status" className="ml-auto text-submeta text-ink-2">{!matches.length ? t("recording.transcript_no_matches")
            : position >= 0 ? t("recording.transcript_result_position", { current: position + 1, total: matches.length })
              : t("recording.transcript_result_count", { count: matches.length })}</span>}
        </div>
        {showBookmarks && <ol id={index} aria-label={t("recording.transcript_bookmark_index")} className="max-h-48 overflow-auto">
          {bookmarks.map(row => <li key={row.id}><button type="button"
            className="flex min-h-10 w-full items-baseline gap-2 rounded-control px-2 py-1.5 text-left text-meta text-ink hover:bg-hover"
            onClick={() => onJump(row.id)}>
            <span className="shrink-0 tabular-nums text-ink-2">{row.clock}</span>
            <span className="min-w-0 break-words">{row.text || t("recording.bookmark")}</span>
          </button></li>)}
        </ol>}
      </div>}
      {(rec.trimmedAt || !rows.length) && <p className="px-4 py-2.5 text-[12.5px] text-ink-3">
        {t(rec.trimmedAt ? "recording.transcript_expired" : "recording.transcript_empty")}
      </p>}
      <ol className="px-4 py-1.5">
        {rows.map(row => {
          const match = hasQuery && matchIds.has(row.id);
          return <li key={row.id} id={row.id} tabIndex={-1}
            className={cx("flex gap-3 rounded-control py-1.5 text-[13px] leading-relaxed transition-colors",
              (highlighted === row.id || (match && selected === row.id)) && "bg-accent-tint")}>
            <span className={cx("w-[60px] shrink-0 pt-px text-[11.5px] tabular-nums", row.bookmark ? "font-semibold text-accent-ink" : "text-ink-3")}>{row.clock}</span>
            <span className={cx("min-w-0 flex-1 select-text [overflow-wrap:anywhere]", row.bookmark ? "font-medium text-accent-ink" : "text-ink")}>
              {match ? transcriptMatchParts(transcriptRowText(row), search).map((part, i) => part.hit
                ? <mark key={i} className="rounded bg-accent-tint font-semibold text-ink">{part.text}</mark>
                : part.text)
                : query && !row.bookmark ? <Marked text={row.text} query={query} /> : transcriptRowText(row)}
            </span>
          </li>;
        })}
      </ol>
    </Card>
  </section>;
}
