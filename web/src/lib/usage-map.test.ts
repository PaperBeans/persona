import { describe, expect, it, vi } from "vitest";
import {
  ageStep,
  cloudTree,
  layoutBoxes,
  layoutTop,
  linkIndex,
  nearestBox,
  outermost,
  parentPath,
  pickForGoal,
  pollUsage,
  reclaimCandidates,
  removePaths,
  scaleChip,
  spliceNode,
  splitScale,
  squarify,
  suggestReclaim,
  trailTo,
  whatIf,
  withFree,
  type OffloadItem,
  type OffloadProvider,
  type UsageNode,
  type UsageReport,
} from "./usage-map";

const GB = 1024 ** 3;
const NOW = 1_800_000_000_000;
const days = (d: number) => NOW / 1000 - d * 86_400;

const leaf = (path: string, bytes: number, extra: Partial<UsageNode> = {}): UsageNode => ({
  id: path,
  name: path.split("/").pop()!,
  path,
  kind: "dir",
  category: "personal",
  bytes,
  has_children: false,
  ...extra,
});

const overlaps = (a: { x: number; y: number; w: number; h: number }, b: { x: number; y: number; w: number; h: number }) =>
  a.x < b.x + b.w - 1e-6 && b.x < a.x + a.w - 1e-6 && a.y < b.y + b.h - 1e-6 && b.y < a.y + a.h - 1e-6;

describe("squarify", () => {
  const kids = [{ bytes: 6 }, { bytes: 6 }, { bytes: 4 }, { bytes: 3 }, { bytes: 2 }, { bytes: 2 }, { bytes: 1 }];

  it("gives every child an area proportional to its bytes", () => {
    const cells = squarify(kids, 0, 0, 600, 400);
    const total = kids.reduce((s, k) => s + k.bytes, 0);
    for (const c of cells) expect(c.w * c.h).toBeCloseTo((600 * 400 * c.child.bytes) / total, 6);
    expect(cells.reduce((s, c) => s + c.w * c.h, 0)).toBeCloseTo(600 * 400, 6);
  });

  it("lays nothing over anything else and stays inside the rectangle", () => {
    const cells = squarify(kids, 10, 20, 300, 200);
    for (const c of cells) {
      expect(c.x).toBeGreaterThanOrEqual(10 - 1e-6);
      expect(c.y).toBeGreaterThanOrEqual(20 - 1e-6);
      expect(c.x + c.w).toBeLessThanOrEqual(310 + 1e-6);
      expect(c.y + c.h).toBeLessThanOrEqual(220 + 1e-6);
    }
    for (let i = 0; i < cells.length; i++) for (let j = i + 1; j < cells.length; j++) expect(overlaps(cells[i], cells[j])).toBe(false);
  });

  it("orders largest first, keeps equal sizes in their order and skips empty children", () => {
    const cells = squarify([{ bytes: 1 }, { bytes: 0 }, { bytes: 5 }, { bytes: 5 }], 0, 0, 100, 100);
    expect(cells.map((c) => c.index)).toEqual([2, 3, 0]);
    expect(squarify([], 0, 0, 100, 100)).toEqual([]);
    expect(squarify([{ bytes: 3 }], 0, 0, 0, 100)).toEqual([]);
  });

  it("puts the free space in a strip along the right edge, or the bottom when narrow", () => {
    const kids = [{ bytes: 70 }, { bytes: 30, key: "free" }];
    const wide = layoutTop(kids, 1000, 500, false);
    const free = wide.find((c) => c.child.key === "free")!;
    expect(free).toMatchObject({ x: 700, y: 0, w: 300, h: 500 });
    const narrow = layoutTop(kids, 400, 500, true);
    expect(narrow.find((c) => c.child.key === "free")).toMatchObject({ x: 0, y: 350, w: 400, h: 150 });
  });
});

describe("splitScale", () => {
  it("shares one scale: the cloud's width is its share of volume plus cloud", () => {
    const s = splitScale(300, 100, 800, 400);
    expect(s.cloud.w).toBeCloseTo(200);
    expect(s.local.w).toBeCloseTo(600);
    expect(s.ratio).toBe(1);
    // same bytes per pixel on both sides
    expect((s.local.w * s.local.h) / 300).toBeCloseTo((s.cloud.w * s.cloud.h) / 100);
  });

  it("keeps a small cloud side at its minimum width and makes it shorter so the area stays true", () => {
    const s = splitScale(1000, 10, 800, 400, 120);
    expect(s.cloud.w).toBe(120);
    expect(s.ratio).toBe(1);
    expect((s.cloud.w * s.cloud.h) / 10).toBeCloseTo((s.local.w * s.local.h) / 1000);
    expect(s.cloud.h).toBeLessThan(400);
  });

  it("compresses a cloud side over 45 % and says by how much", () => {
    const s = splitScale(100, 900, 1000, 400);
    expect(s.cloud.w).toBeCloseTo(450);
    expect(s.ratio).toBeGreaterThan(1);
    expect(scaleChip(s.ratio)).toBe(Math.round((900 * 550) / (100 * 450)));
    expect(scaleChip(1)).toBeUndefined();
  });

  it("gives everything to the local side with nothing offloaded", () => {
    expect(splitScale(100, 0, 800, 400)).toEqual({ local: { x: 0, y: 0, w: 800, h: 400 }, cloud: { x: 800, y: 0, w: 0, h: 0 }, ratio: 1 });
  });
});

describe("ageStep", () => {
  it("steps from recent to over a year", () => {
    expect(ageStep(undefined, NOW)).toBeUndefined();
    expect(ageStep(days(3), NOW)).toBe("recent");
    expect(ageStep(days(60), NOW)).toBe("months");
    expect(ageStep(days(200), NOW)).toBe("half_year");
    expect(ageStep(days(400), NOW)).toBe("year");
  });
});

const providers: OffloadProvider[] = [{ extension: "example", id: "cloud", name: "Example Cloud", kinds: ["file", "dir"], restore: true }];

function sampleTree(): UsageNode {
  return {
    id: "group:root",
    name: "",
    kind: "root",
    category: "other",
    bytes: 200 * GB,
    has_children: true,
    children: [
      {
        id: "group:home",
        name: "",
        key: "home",
        kind: "group",
        category: "personal",
        bytes: 120 * GB,
        has_children: true,
        children: [
          leaf("/home/me/Movies/old", 40 * GB, { can_offload: true, can_trash: true, used_at: days(400) }),
          leaf("/home/me/Projects", 30 * GB, { can_offload: true, can_trash: true, used_at: days(2), children: [leaf("/home/me/Projects/stale", 20 * GB, { can_offload: true, can_trash: true, used_at: days(500) })] }),
          leaf("/home/me/.cache/pip", 8 * GB, { category: "cache", can_trash: true, cache_id: "pip-cache", used_at: days(1) }),
          leaf("/home/me/.cache/thing", 6 * GB, { category: "cache", can_trash: true, used_at: days(10) }),
          leaf("/home/me/.cache/go-build", 9 * GB, { category: "cache", can_trash: true, cache_id: "go-build", cache_in_use: true }),
          leaf("/home/me/Documents", 5 * GB, { refusal: "standard_folder" }),
        ],
      },
      { id: "group:free", name: "", key: "free", kind: "free", category: "free", bytes: 80 * GB, has_children: false },
    ],
  };
}

describe("suggestReclaim and pickForGoal", () => {
  it("suggests unused catalog caches, trashable caches and cold offloadable items, largest first, at most five", () => {
    const s = suggestReclaim(sampleTree(), providers, NOW);
    expect(s.map((c) => [c.node.path, c.action])).toEqual([
      ["/home/me/Movies/old", "offload"],
      ["/home/me/Projects/stale", "offload"],
      ["/home/me/.cache/pip", "clean"],
      ["/home/me/.cache/thing", "trash"],
    ]);
  });

  it("offers no offload without a provider", () => {
    expect(suggestReclaim(sampleTree(), [], NOW).map((c) => c.action)).toEqual(["clean", "trash"]);
  });

  it("picks caches first, then size × age, until the goal is met, never a folder and its child both", () => {
    const all = reclaimCandidates(sampleTree(), providers, NOW);
    expect(all[0].action).toBe("clean");
    const picked = pickForGoal(all, 45 * GB);
    expect(picked.map((c) => c.node.path)).toEqual(["/home/me/.cache/pip", "/home/me/Movies/old"]);
    const many = pickForGoal(all, 1000 * GB);
    const paths = many.map((c) => c.node.path!);
    expect(paths).toContain("/home/me/Projects/stale");
    expect(paths).not.toContain("/home/me/Projects");
  });
});

describe("whatIf", () => {
  it("adds the selection to the free space and counts a folder and its child once", () => {
    const tree = sampleTree();
    const projects = tree.children![0].children![1];
    const r = whatIf({ mount: "/", total_bytes: 200 * GB, used_bytes: 120 * GB, free_bytes: 80 * GB }, [projects, projects.children![0], tree.children![0].children![0]]);
    expect(r.count).toBe(2);
    expect(r.freed).toBe(70 * GB);
    expect(r.freeAfter).toBe(150 * GB);
    expect(r.pctBefore).toBe(60);
    expect(r.pctAfter).toBe(25);
  });
  it("outermost drops nested and repeated picks", () => {
    expect(outermost([leaf("/a/b", 1), leaf("/a", 2), leaf("/a", 2), leaf("/ab", 3)]).map((n) => n.path)).toEqual(["/a", "/ab"]);
  });
});

describe("linkIndex", () => {
  const items: OffloadItem[] = [
    { id: "1", name: "old", kind: "dir", bytes: 4 * GB, origin_path: "/home/me/Movies/old", restorable: true },
    { id: "2", name: "clip", kind: "file", bytes: GB, origin_path: "/home/me/Movies/old/clip.mov", restorable: true },
    { id: "3", name: "notes", kind: "dir", bytes: GB, origin_path: "/home/me/Notes", restorable: true },
    { id: "4", name: "loose", kind: "other", bytes: GB, restorable: false },
  ];
  it("counts the items a folder held and finds the deepest visible box for each", () => {
    const idx = linkIndex(items);
    expect(idx.under("/home/me/Movies").map((i) => i.id)).toEqual(["1", "2"]);
    expect(idx.under("/home/me/Mov")).toEqual([]);
    const boxes = [{ id: "home", path: "/home/me" }, { id: "movies", path: "/home/me/Movies" }, { id: "group:apps" }];
    expect(idx.anchor(items[1], boxes)).toBe("movies");
    expect(idx.anchor(items[2], boxes)).toBe("home");
    expect(idx.anchor(items[3], boxes)).toBeUndefined();
  });
  it("draws the inventory grouped, with what it did not list as one box", () => {
    const tree = cloudTree({ state: "ready", total_bytes: 8 * GB, items: items.map((i) => ({ ...i, group: i.kind === "file" ? "Files" : undefined })), more: { count: 3, bytes: 2 * GB } });
    expect(tree.children!.map((c) => c.id)).toEqual(["cloud-group:dir", "cloud-group:Files", "cloud-group:other", "cloud-group:more"]);
    expect(tree.bytes).toBe(9 * GB);
  });
});

describe("tree patches", () => {
  it("removes trashed paths and shrinks every ancestor by what left", () => {
    const next = removePaths(sampleTree(), ["/home/me/Projects/stale", "/home/me/.cache/thing"]);
    const home = next.children![0];
    expect(home.bytes).toBe(120 * GB - 26 * GB);
    expect(home.children!.find((c) => c.path === "/home/me/Projects")!.bytes).toBe(10 * GB);
    expect(next.bytes).toBe(200 * GB - 26 * GB);
  });
  it("splices a fresh scan in place and keeps the totals adding up", () => {
    const fresh = leaf("/home/me/Movies/old", 10 * GB, { children: [leaf("/home/me/Movies/old/a", 10 * GB)] });
    const next = spliceNode(sampleTree(), fresh);
    expect(trailTo(next, "/home/me/Movies/old/a")!.map((n) => n.id)).toEqual(["group:root", "group:home", "/home/me/Movies/old", "/home/me/Movies/old/a"]);
    expect(next.children![0].bytes).toBe(90 * GB);
  });
  it("reads the free box from the volume", () => {
    const next = withFree(sampleTree(), { mount: "/", total_bytes: 200 * GB, used_bytes: 100 * GB, free_bytes: 100 * GB });
    expect(next.children![1].bytes).toBe(100 * GB);
    expect(next.bytes).toBe(220 * GB);
  });
  it("names a path's folder", () => {
    expect(parentPath("/a/b/c")).toBe("/a/b");
    expect(parentPath("/a")).toBe("/");
    expect(parentPath("C:\\Users\\me\\x")).toBe("C:\\Users\\me");
  });
});

describe("layoutBoxes and nearestBox", () => {
  it("nests children under a title strip and snaps edges", () => {
    const boxes = layoutBoxes(sampleTree(), 900, 500, { maxDepth: 3, narrow: false, top: true, label: (n) => n.name || n.key || "", size: () => "1 GB" });
    for (const b of boxes) for (const v of [b.x, b.y, b.w, b.h]) expect(Number.isInteger(v)).toBe(true);
    const home = boxes.find((b) => b.node.id === "group:home")!;
    expect(home.nested).toBe(true);
    const inside = boxes.filter((b) => b.parent?.id === "group:home");
    expect(inside.length).toBeGreaterThan(0);
    for (const b of inside) expect(b.y).toBeGreaterThanOrEqual(home.y + 22);
  });
  it("moves to the nearest box that way", () => {
    const a = { x: 0, y: 0, w: 10, h: 10 };
    const b = { x: 20, y: 0, w: 10, h: 10 };
    const c = { x: 0, y: 20, w: 10, h: 10 };
    expect(nearestBox([a, b, c], a, "right")).toBe(b);
    expect(nearestBox([a, b, c], a, "down")).toBe(c);
    expect(nearestBox([a, b, c], a, "left")).toBeUndefined();
  });
});

describe("pollUsage", () => {
  it("asks again while the node scans, with rescan only on the first call", async () => {
    const answers: Partial<UsageReport>[] = [{ state: "scanning" }, { state: "scanning" }, { state: "ready" }];
    const fetcher = vi.fn(async (_mid: string, o: { rescan?: boolean } = {}) => ({ ...answers.shift()!, rescan: o.rescan }) as unknown as UsageReport);
    const seen: string[] = [];
    const r = await pollUsage("m", "/x", { rescan: true, intervalMs: 1, fetcher, onReport: (x) => seen.push(x.state) });
    expect(r.state).toBe("ready");
    expect(seen).toEqual(["scanning", "scanning", "ready"]);
    expect(fetcher.mock.calls.map((c) => c[1]?.rescan)).toEqual([true, false, false]);
  });
});
