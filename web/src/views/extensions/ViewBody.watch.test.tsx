// @vitest-environment jsdom
// Dashboards phase 2 watch pages (#13020): every display (number, status, table, trend) in every fixture state from
// spec/fixtures/dashboards/watch.json draws its watch section and Dashboards index row cleanly, watch footer controls
// render as expected with single-line buttons, and long watch questions / details / status readings stay readable.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { readView, type UIView } from "../../lib/extension-ui";
import { INERT, ViewSections } from "./ViewBody";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

interface WatchDraw {
  name: string;
  watch: { question: string; display: string };
  tripped: boolean;
  section: { key: string; items: Record<string, unknown>[] };
  glance_row: { value: string; icon?: string; icon_color?: string; spark?: { values: number[]; fill?: boolean; tone?: string } };
}

const FIXTURE: { draws: WatchDraw[] } = JSON.parse(
  readFileSync(resolve(__dirname, "../../../../spec/fixtures/dashboards/watch.json"), "utf8"),
);

const render = (sections: unknown[], extra: Partial<Parameters<typeof ViewSections>[0]> = {}) => {
  const view: UIView = readView({ sections });
  return renderToStaticMarkup(<ViewSections view={view} {...INERT} {...extra} />);
};
const doc = (html: string) => new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");

describe("Dashboards phase 2 watch displays against spec/fixtures/dashboards/watch.json (#13020)", () => {
  it.each(FIXTURE.draws)("$name draws its watch section and index glance row", (caseData) => {
    const html = render([caseData.section]);
    const d = doc(html);
    const metric = caseData.section.items[0] as { label: string; value: string; detail?: string; tone?: string; icon?: string; icon_color?: string; spark?: { tone?: string; fill?: boolean } };
    expect(d.body.textContent).toContain(metric.label);
    expect(d.body.textContent).toContain(metric.value);
    if (metric.detail) expect(d.body.textContent).toContain(metric.detail);
    if (metric.tone === "caution") expect(html).toContain("text-orange");
    if (metric.icon) expect(d.querySelector("svg")).not.toBeNull();
    if (metric.icon_color) expect(html).toContain(`var(--hue-${metric.icon_color})`);
    if (metric.spark) {
      const svg = d.querySelector("svg[viewBox='0 0 64 22']");
      expect(svg).not.toBeNull();
      expect(svg!.querySelector("polyline")).not.toBeNull();
      if (metric.spark.fill) expect(svg!.querySelector("polygon")).not.toBeNull();
      if (metric.spark.tone === "caution") expect(svg!.innerHTML).toContain("var(--orange)");
    }
    const tableItem = caseData.section.items.find((it) => it.type === "table") as { columns: { title: string }[]; rows: string[][] } | undefined;
    if (tableItem) {
      const table = d.querySelector("table")!;
      expect(table).not.toBeNull();
      expect([...table.querySelectorAll("th")].map((th) => th.textContent)).toEqual(tableItem.columns.map((c) => c.title));
      expect(table.querySelectorAll("tbody tr")).toHaveLength(tableItem.rows.length);
    }

    // Index row with the fixture's glance_row fields
    const rowHtml = render([{
      key: "all",
      items: [{
        type: "row",
        key: `page:${caseData.name}`,
        title: caseData.name,
        subtitle: "Checked today 08:02",
        icon: caseData.glance_row.icon ?? "chart",
        ...(caseData.glance_row.icon_color ? { icon_color: caseData.glance_row.icon_color } : {}),
        value: caseData.glance_row.value,
        ...(caseData.glance_row.spark ? { spark: caseData.glance_row.spark } : {}),
        action: { id: "open", push: { ui: "pg_1a2b3c4d5e6f7081" } },
      }],
    }]);
    const rd = doc(rowHtml);
    expect(rd.body.textContent).toContain(caseData.glance_row.value);
    if (caseData.glance_row.icon_color) expect(rowHtml).toContain(`var(--hue-${caseData.glance_row.icon_color})`);
    if (caseData.glance_row.spark) expect(rd.querySelector("svg[viewBox='0 0 56 20'] polyline")).not.toBeNull();
  });

  it("lets a lone metric wrap a long question and detail, while keeping multi-metric labels truncated", () => {
    const lone = doc(render([{
      key: "persona.watch",
      items: [{
        type: "metric",
        key: "persona.reading",
        label: "Rent for a 1-bed apartment within 10 minutes of Elm Street station",
        value: "1,985.5 USD",
        detail: "Below 2,000 USD · Checked yesterday 20:00",
        tone: "caution",
        icon: "warning",
      }],
    }]));
    const loneLabel = [...lone.querySelectorAll("span")].find((el) => el.textContent?.startsWith("Rent for a 1-bed"))!;
    expect(loneLabel.className).toContain("line-clamp-2");
    expect(loneLabel.className).toContain("break-words");
    expect(loneLabel.className).not.toContain("truncate");
    const loneDetail = [...lone.querySelectorAll("div")].find((el) => el.textContent === "Below 2,000 USD · Checked yesterday 20:00")!;
    expect(loneDetail.className).toContain("break-words");
    expect(loneDetail.className).not.toContain("truncate");

    const pair = doc(render([{
      items: [
        { type: "metric", label: "First", value: "1" },
        { type: "metric", label: "Second", value: "2" },
      ],
    }]));
    const firstLabel = [...pair.querySelectorAll("span")].find((el) => el.textContent === "First")!;
    expect(firstLabel.className).toContain("truncate");
  });

  it("caps a row's value width so long status text truncates instead of crushing the title", () => {
    const html = render([{
      items: [{
        type: "row",
        key: "page:status",
        title: "Shop sites",
        subtitle: "Checked today 08:02",
        icon: "warning",
        icon_color: "red",
        value: "Primary checkout gateway degraded across two regions with elevated timeout rate",
        action: { id: "open", push: { ui: "pg_2b3c4d5e6f708192" } },
      }],
    }]);
    const val = [...doc(html).querySelectorAll("span")].find((el) => el.textContent?.startsWith("Primary checkout"))!;
    expect(val.className).toContain("min-w-0");
    expect(val.className).toContain("max-w-[45%]");
    expect(val.className).toContain("truncate");
    expect(val.className).not.toContain("shrink-0");
  });

  it("draws watch footer controls with single-line buttons and switch rows", () => {
    const html = render([{
      key: "persona.footer",
      items: [
        { type: "text", key: "persona.updated", text: "Checked today 08:02 · next check today 20:00", style: "caption" },
        { type: "text", key: "persona.status", text: "Checking now…", style: "caption" },
        { type: "button", key: "persona.refresh", title: "Refresh now", action: { id: "persona.refresh" } },
        { type: "button", key: "persona.pause", title: "Pause updates", action: { id: "persona.pause" } },
        { type: "row", key: "persona.alert", title: "Alert me", subtitle: "Below 2,000 USD", control: { kind: "switch", name: "on", on: true, action: { id: "persona.alert" } } },
        { type: "row", key: "persona.overview", title: "Show on Overview", control: { kind: "switch", name: "overview", on: true, action: { id: "persona.overview" } } },
        { type: "button", key: "persona.pin", title: "Unpin from sidebar", action: { id: "persona.unpin" } },
        { type: "button", key: "persona.rename", title: "Rename", action: { id: "persona.rename" } },
        { type: "button", key: "persona.remove", title: "Remove", role: "destructive", action: { id: "persona.remove", confirm: "Remove?" } },
      ],
    }], { onControl: () => {} });
    const d = doc(html);
    expect(d.body.textContent).toContain("Checked today 08:02 · next check today 20:00");
    expect(d.body.textContent).toContain("Checking now…");
    const switches = [...d.querySelectorAll('[role="switch"]')];
    expect(switches.map((s) => [s.getAttribute("aria-label"), s.getAttribute("aria-checked")])).toEqual([
      ["Alert me", "true"],
      ["Show on Overview", "true"],
    ]);
    const actionButtons = [...d.querySelectorAll("button:not([role=switch])")];
    expect(actionButtons.map((b) => b.textContent)).toEqual(["Refresh now", "Pause updates", "Unpin from sidebar", "Rename", "Remove"]);
    for (const btn of actionButtons) {
      expect(btn.className).toContain("whitespace-nowrap");
      expect(btn.querySelector("span")?.className).toContain("truncate");
    }
  });
});
