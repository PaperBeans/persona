// A topic's knowledge points (docs/assistant/knowledge.md "Knowledge points"): the points directly under the topic, then
// one section per subtopic, a subtopic's own subtopics as headings inside its card. Each point shows its kind, its text,
// its reason or cause, where it came from (by name, opening that entry) and when it was last confirmed.
import { t } from "../../i18n";
import { type KBDetail, type KBEntry } from "../../lib/kb";
import { groupPoints, pointDay, pointSources, pointWarns, type PointGroup, type TopicPoint, pointKindLabel } from "../../lib/kb-points";
import { href } from "../../lib/router";
import { Card, onPlainClick, SectionTitle, TonePill } from "./KBParts";

export function TopicPoints({ detail, onOpen }: { detail: KBDetail; onOpen: (e: KBEntry) => void }) {
  const root = groupPoints(detail.points);
  const links = detail.links;
  if (!root.points.length && !root.groups.length) {
    return (
      <section>
        <SectionTitle title={t("knowledge-topicpoints.title")} />
        <Card>
          <p className="px-4 py-3.5 text-[13px] leading-relaxed text-ink-3">{t("knowledge-topicpoints.empty")}</p>
        </Card>
      </section>
    );
  }
  return (
    <>
      {root.points.length > 0 && (
        <section>
          <SectionTitle title={t("knowledge-topicpoints.title")} count={root.points.length} />
          <Card>
            <Points points={root.points} links={links} onOpen={onOpen} />
          </Card>
        </section>
      )}
      {root.groups.map((g) => (
        <section key={g.path.join("/")}>
          <SectionTitle title={g.path[g.path.length - 1]} count={count(g)} />
          <Card>
            <Points points={g.points} links={links} onOpen={onOpen} />
            {g.groups.map((sub) => (
              <div key={sub.path.join("/")} className="border-t border-line first:border-t-0">
                <h4 className="px-4 pt-3 text-[12px] font-semibold text-ink-2">{sub.path[sub.path.length - 1]}</h4>
                <Points points={flat(sub)} links={links} onOpen={onOpen} />
              </div>
            ))}
          </Card>
        </section>
      ))}
    </>
  );
}

/** A group's points with its subgroups' (the page nests once; deeper paths read as their subtopic). */
function flat(g: PointGroup): TopicPoint[] {
  return [...g.points, ...g.groups.flatMap(flat)];
}

function count(g: PointGroup): number {
  return flat(g).length;
}

function Points({ points, links, onOpen }: { points: TopicPoint[]; links?: KBEntry[] | null; onOpen: (e: KBEntry) => void }) {
  if (!points.length) return null;
  return (
    <ul className="divide-y divide-line">
      {points.map((p) => {
        const sources = pointSources(p, links);
        return (
          <li key={p.id} className="px-4 py-2.5">
            <p className="text-[13px] leading-relaxed text-ink">
              <span className="mr-1.5 inline-flex align-[1px]">
                <TonePill tone={pointWarns(p.kind) ? "orange" : "neutral"} strong>{pointKindLabel(p.kind)}</TonePill>
              </span>
              {p.text}
            </p>
            {p.why && <p className="mt-0.5 text-[12.5px] leading-relaxed text-ink-3">{t(p.kind === "pitfall" ? "knowledge-topicpoints.cause" : "knowledge-topicpoints.reason", { text: p.why })}</p>}
            {(sources.length > 0 || p.when) && (
              <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11.5px] text-ink-3">
                {sources.map((s) => (
                  <a
                    key={`${s.kind}/${s.id}`}
                    href={href({ name: "kb", kind: s.kind, id: s.id })}
                    onClick={(ev) => onPlainClick(ev, () => onOpen(s))}
                    className="max-w-[240px] truncate font-medium text-accent-ink hover:underline"
                    title={t("knowledge-topicpoints.source_title", { name: s.name })}
                  >
                    {s.name}
                  </a>
                ))}
                {p.when && <span className="tabular-nums">{t("knowledge-topicpoints.when", { day: pointDay(p.when) })}</span>}
              </p>
            )}
          </li>
        );
      })}
    </ul>
  );
}
