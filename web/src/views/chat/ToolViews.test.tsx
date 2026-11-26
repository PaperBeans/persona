// @vitest-environment jsdom
import { renderToStaticMarkup } from "react-dom/server";
import { beforeAll, describe, expect, it } from "vitest";
import { readView } from "../../lib/extension-ui";
import type { TurnToolView } from "../../lib/turn-tool-views";
import { loadPages } from "../../lib/lazy-page";
import { ToolViews } from "./ToolViews";

beforeAll(() => loadPages(), 60_000);

describe("ToolViews", () => {
  it("renders tool views inside owner scopes in compact mode", () => {
    const view = readView({
      title: "Morning Commute",
      sections: [{
        items: [{
          type: "map",
          key: "commute-map",
          alt: "Driving route map",
          markers: [
            { id: "o", lat: 39.9, lon: 116.4, label: "Home", kind: "origin" },
            { id: "d", lat: 39.98, lon: 116.32, label: "Office", kind: "destination" },
          ],
          routes: [{
            id: "r1",
            mode: "driving",
            distance_meters: 12400,
            duration_seconds: 1380,
            selected: true,
            points: [{ lat: 39.9, lon: 116.4 }, { lat: 39.98, lon: 116.32 }],
          }],
        }],
      }],
    });
    const list: TurnToolView[] = [{
      id: "i:call-1#0",
      tool: "Amap · Maps direction driving",
      owner: "chat-tool:m1/s1/i:call-1#0",
      view,
    }];
    const html = renderToStaticMarkup(<ToolViews views={list} />);
    expect(html).toContain("data-tool-views=\"1\"");
    expect(html).toContain("Morning Commute");
    expect(html).toContain("Amap · Maps direction driving");
  });

  it("renders nothing when views list is empty", () => {
    expect(renderToStaticMarkup(<ToolViews views={[]} />)).toBe("");
  });
});
