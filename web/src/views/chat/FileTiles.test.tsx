import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { ComposerFile } from "../../lib/composer-files";
import { FileTiles } from "./FileTilesBody";

// A file's ✕ on a phone (#11442): its tap area is the ✕'s column of the tile (touch-hit-corner reaching 8 px left and
// 24 px down). The tile clips the target at its edges. See scripts/test/web_mobile_input_e2e.py (tile_hits).
const file = (state: ComposerFile["state"]): ComposerFile => ({ id: state, name: `${state}.txt`, size: 12, sent: 0, state });

describe("FileTiles", () => {
  it("gives a file's ✕ its column for a tap area", () => {
    const html = renderToStaticMarkup(<FileTiles draftKey="k" files={[file("ready"), file("failed")]} />);
    const removes = [...html.matchAll(/<button[^>]*data-remove[^>]*class="([^"]*)"/g)].map((m) => m[1].split(" "));
    expect(removes).toHaveLength(2);
    for (const c of removes) {
      expect(c).toEqual(expect.arrayContaining(["touch-hit-corner", "[--hit-in-x:8px]", "[--hit-in-y:24px]"]));
      expect(c).not.toContain("touch-hit");
    }
    // the tiles keep clipping it
    expect(html.match(/class="group\/att relative [^"]*overflow-hidden/g)).toHaveLength(2);
  });

  it("exposes progress, size and a named recovery action for each state", () => {
    const files = [file("waiting"), { ...file("uploading"), sent: 3 }, file("ready"), { ...file("failed"), error: "Storage full" }];
    const html = renderToStaticMarkup(<FileTiles draftKey="k" files={files} />);
    expect(html.match(/role="progressbar"/g)).toHaveLength(2);
    expect(html).toContain('aria-label="uploading.txt" aria-valuenow="25" aria-valuetext="3 B / 12 B"');
    expect(html).toContain('style="width:25%"');
    expect(html).toMatch(/data-state="ready".*?>12 B<\/span>/);
    expect(html).toContain('title="Storage full"');
    expect(html).toContain('aria-label="重试 failed.txt"');
    expect(html).toContain('aria-label="取消上传 uploading.txt"');
    expect(html).toContain('aria-label="移除 ready.txt"');
  });
});
