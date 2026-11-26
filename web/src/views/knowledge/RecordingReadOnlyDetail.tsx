// The default Knowledge panel's read-only view of a recording that already exists. It stays available when the
// recording module is off; the enabled module supplies the full interactive detail instead. Only Core read endpoints
// are used here, and this file never imports the recording module.
import { useEffect, useState } from "react";
import type { ModuleKBDetailProps } from "../../core/module-host";
import { t } from "../../i18n";
import { api, apiBlob, enc, isAbort } from "../../lib/api";
import { knownZone, wallClock, type KBSegment, type RecordingPhoto } from "../../lib/kb";
import { Markdown } from "../../ui/Markdown";
import { Card, Marked, SectionTitle } from "./KBParts";

type Props = ModuleKBDetailProps;
type Auth = Props["auth"];

/** Stored content only: no record, upload, rename, transcription, regeneration, live view or handoff actions. */
export function RecordingReadOnlyDetail({ detail: d, query, auth, current, children }: Props) {
  return <>{children({
    hideProfile: !!d?.recap,
    sections: d && (
      <>
        <StoredMarkdown title={t("knowledge-kbdetail.recording_minutes")} markdown={d.minutes?.markdown} />
        <StoredMarkdown title={t("knowledge-kbdetail.recording_day_recap")} markdown={d.recap?.markdown} />
        <StoredPhotos id={d.id} kind={d.meta?.kind} auth={auth} current={current} />
      </>
    ),
    tail: d && <Transcript meta={d.meta} segments={d.segments} query={query} />,
  })}</>;
}

function StoredMarkdown({ title, markdown }: { title: string; markdown?: string }) {
  if (!markdown?.trim()) return null;
  return (
    <section>
      <SectionTitle title={title} />
      <Card>
        <div className="px-4 py-3.5">
          <Markdown text={markdown} small />
        </div>
      </Card>
    </section>
  );
}

/** The transcript already stored on the hub, including its saved clock times; no live lines are read or added. */
function Transcript({ meta, segments, query }: { meta?: Record<string, unknown> | null; segments?: KBSegment[] | null; query?: string }) {
  const lines = (segments ?? []).filter((s) => s?.echo !== true && typeof s?.text === "string" && s.text.trim());
  if (!lines.length) return null;
  return (
    <section>
      <SectionTitle title={t("knowledge-kbdetail.recording_transcript")} count={lines.length} />
      <Card>
        <ol className="px-4 py-1.5">
          {lines.map((line, i) => (
            <li key={i} className="flex gap-3 py-1.5 text-meta leading-relaxed">
              <span className="w-14 shrink-0 pt-px text-ink-3 tabular-nums">{transcriptClock(meta, line.start)}</span>
              <span className="min-w-0 flex-1 break-words text-ink select-text">{query ? <Marked text={line.text.trim()} query={query} /> : line.text.trim()}</span>
            </li>
          ))}
        </ol>
      </Card>
    </section>
  );
}

function transcriptClock(meta: Record<string, unknown> | null | undefined, seconds: number): string {
  const started = typeof meta?.started_at === "string" ? meta.started_at : "";
  const start = Date.parse(started);
  if (!Number.isFinite(start)) return offsetClock(seconds);
  const zone = knownZone(meta?.tz) ? meta.tz : undefined;
  const c = wallClock(new Date(start + seconds * 1000), zone);
  return [c.h, c.m, c.s].map((n) => String(n).padStart(2, "0")).join(":");
}

function offsetClock(seconds: number): string {
  const whole = Math.max(0, Math.floor(seconds));
  const h = Math.floor(whole / 3600);
  const m = Math.floor((whole % 3600) / 60);
  const s = String(whole % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

/** A missing photo list (including a hub that has not moved this read into Core yet) hides only this section. */
function StoredPhotos({ id, kind, auth, current }: { id: string; kind: unknown; auth: Auth; current: () => boolean }) {
  const [photos, setPhotos] = useState<RecordingPhoto[]>([]);
  const meeting = kind === "meeting";
  useEffect(() => {
    if (!meeting) {
      setPhotos([]);
      return;
    }
    const ac = new AbortController();
    api<{ photos?: RecordingPhoto[] | null }>(`/recordings/${enc(id)}/photos`, { ...auth, signal: ac.signal }).then((r) => {
      if (ac.signal.aborted || !current()) return;
      setPhotos((r.photos ?? []).filter((p) => p && Number.isInteger(p.n) && p.n > 0 && typeof p.id === "string" && p.id));
    }, () => {
      if (!ac.signal.aborted && current()) setPhotos([]);
    });
    return () => ac.abort();
  }, [id, meeting, auth, current]);
  if (!photos.length) return null;
  return (
    <section>
      <SectionTitle title={t("knowledge-kbdetail.recording_photos")} count={photos.length} />
      <Card>
        <ol className="grid gap-4 p-4 sm:grid-cols-2">
          {photos.map((photo) => (
            <li key={photo.n} className="min-w-0">
              <PhotoImage id={id} photo={photo} auth={auth} current={current} />
              {(photo.caption || photo.note) && <p className="mt-2 break-words text-meta leading-relaxed text-ink select-text">{photo.caption || photo.note}</p>}
              {photo.text && <pre className="mt-1 max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-control bg-field px-3 py-2 font-sans text-meta leading-relaxed text-ink-2 select-text">{photo.text}</pre>}
            </li>
          ))}
        </ol>
      </Card>
    </section>
  );
}

function PhotoImage({ id, photo, auth, current }: { id: string; photo: RecordingPhoto; auth: Auth; current: () => boolean }) {
  const [url, setUrl] = useState<string>();
  useEffect(() => {
    const ac = new AbortController();
    let made: string | undefined;
    apiBlob(`/recordings/${enc(id)}/photos/${photo.n}`, { ...auth, signal: ac.signal }).then((blob) => {
      if (ac.signal.aborted || !current()) return;
      made = URL.createObjectURL(blob);
      setUrl(made);
    }, (err) => {
      if (!isAbort(err) && current()) setUrl(undefined);
    });
    return () => {
      ac.abort();
      if (made) URL.revokeObjectURL(made);
    };
  }, [id, photo.n, auth, current]);
  if (!url) return null;
  const alt = photo.caption || photo.note || t("knowledge-kbdetail.recording_photo_label", { n: photo.n });
  return <img src={url} alt={alt} className="h-48 w-full rounded-control bg-field object-contain" />;
}
