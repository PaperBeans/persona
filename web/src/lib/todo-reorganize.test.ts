import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DirectionTree } from "./direction-tree";
import type { Todo } from "./todos";

const api = vi.fn(async (_path: string, _init?: unknown): Promise<unknown> => ({ updated: [] }));
vi.mock("./api", async (orig) => ({
  ...(await orig<typeof import("./api")>()),
  api: (path: string, init?: unknown) => api(path, init),
}));

const {
  MAX_REORGANIZE_IDS,
  buildReorganizeRequest,
  canTransferSupervision,
  eligibleParentTargets,
  reorganizeDirectionOptions,
  reorganizeTodos,
} = await import("./todo-reorganize");

const makeTodo = (id: string, title: string, extra: Partial<Todo> = {}): Todo => ({
  id,
  title,
  created_at: "2026-11-24T10:00:00.000Z",
  updated_at: "2026-11-24T11:00:00.000Z",
  ...extra,
});

describe("todo-reorganize helpers (#14758)", () => {
  beforeEach(() => {
    api.mockReset();
    api.mockResolvedValue({ updated: [] });
  });

  describe("canTransferSupervision", () => {
    it("allows transfer only for non-empty active direction targets", () => {
      const active = new Set(["dir-active"]);
      expect(canTransferSupervision({ mode: "keep" }, active)).toBe(false);
      expect(canTransferSupervision({ mode: "clear" }, active)).toBe(false);
      expect(canTransferSupervision({ mode: "set", id: "" }, active)).toBe(false);
      expect(canTransferSupervision({ mode: "set", id: "dir-inactive" }, active)).toBe(false);
      expect(canTransferSupervision({ mode: "set", id: "dir-active" }, undefined)).toBe(false);
      expect(canTransferSupervision({ mode: "set", id: "dir-active" }, active)).toBe(true);
    });
  });

  describe("buildReorganizeRequest", () => {
    it("returns null when ids are empty, exceed limit, or no target change is requested", () => {
      expect(
        buildReorganizeRequest({
          ids: [],
          partOf: { mode: "clear" },
          direction: { mode: "keep" },
        }),
      ).toBeNull();

      expect(
        buildReorganizeRequest({
          ids: ["t1"],
          partOf: { mode: "keep" },
          direction: { mode: "keep" },
        }),
      ).toBeNull();

      expect(
        buildReorganizeRequest({
          ids: ["t1"],
          partOf: { mode: "set", id: "   " },
          direction: { mode: "keep" },
        }),
      ).toBeNull();

      const tooMany = Array.from({ length: MAX_REORGANIZE_IDS + 1 }, (_, i) => `t${i}`);
      expect(
        buildReorganizeRequest({
          ids: tooMany,
          partOf: { mode: "clear" },
          direction: { mode: "keep" },
        }),
      ).toBeNull();
    });

    it("distinguishes keep, clear, and set states with safe defaults", () => {
      expect(
        buildReorganizeRequest({
          ids: [" t1 ", "t2", "t1"],
          partOf: { mode: "set", id: " goal-1 " },
          direction: { mode: "keep" },
        }),
      ).toEqual({
        ids: ["t1", "t2"],
        part_of: "goal-1",
        include_children: false,
        transfer_supervision: false,
      });

      expect(
        buildReorganizeRequest({
          ids: ["t1"],
          partOf: { mode: "clear" },
          direction: { mode: "clear" },
          includeChildren: true,
          transferSupervision: true,
          activeDirectionIds: new Set(["dir-1"]),
        }),
      ).toEqual({
        ids: ["t1"],
        part_of: "",
        direction_id: "",
        include_children: true,
        transfer_supervision: false,
      });
    });

    it("enables transfer_supervision only when targeting an active direction", () => {
      const active = new Set(["dir-active"]);

      expect(
        buildReorganizeRequest({
          ids: ["t1"],
          partOf: { mode: "keep" },
          direction: { mode: "set", id: "dir-active" },
          transferSupervision: true,
          activeDirectionIds: active,
        }),
      ).toEqual({
        ids: ["t1"],
        direction_id: "dir-active",
        include_children: false,
        transfer_supervision: true,
      });

      expect(
        buildReorganizeRequest({
          ids: ["t1"],
          partOf: { mode: "keep" },
          direction: { mode: "set", id: "dir-external" },
          transferSupervision: true,
          activeDirectionIds: active,
        }),
      ).toEqual({
        ids: ["t1"],
        direction_id: "dir-external",
        include_children: false,
        transfer_supervision: false,
      });
    });
  });

  describe("eligibleParentTargets", () => {
    it("excludes selected todos and all their multi-hop descendants while keeping ancestors and siblings", () => {
      const todos: Todo[] = [
        makeTodo("root", "Root Goal"),
        makeTodo("mid", "Mid Task", { part_of: "root" }),
        makeTodo("leaf", "Leaf Task", { part_of: "mid" }),
        makeTodo("grandleaf", "Grandleaf Task", { part_of: "leaf" }),
        makeTodo("sibling", "Sibling Task", { part_of: "root" }),
        makeTodo("other", "Other Goal"),
      ];

      expect(eligibleParentTargets(todos, ["mid"]).map((t) => t.id)).toEqual(["root", "sibling", "other"]);
      expect(eligibleParentTargets(todos, new Set(["mid", "other"])).map((t) => t.id)).toEqual(["root", "sibling"]);
    });

    it("terminates safely when existing data already contains a part_of cycle", () => {
      const cyclic: Todo[] = [
        makeTodo("a", "A", { part_of: "b" }),
        makeTodo("b", "B", { part_of: "a" }),
        makeTodo("c", "C"),
      ];
      expect(eligibleParentTargets(cyclic, ["a"]).map((t) => t.id)).toEqual(["c"]);
    });
  });

  describe("reorganizeDirectionOptions", () => {
    it("combines direction tree leaders and assistant leader IDs, excluding secretary and marking external inactive", () => {
      const tree: DirectionTree = {
        directions: [
          {
            id: "dir-1",
            name: "Core Platform",
            counts: { working: 1, needs_you: 0, stuck: 0, review: 0 },
            running: true,
          },
          {
            id: "dir-ext",
            name: "External Bot",
            external: true,
            counts: { working: 0, needs_you: 0, stuck: 0, review: 0 },
            running: false,
          },
          {
            id: "main",
            name: "",
            secretary: true,
            counts: { working: 0, needs_you: 0, stuck: 0, review: 0 },
            running: false,
          },
        ],
      };

      const opts = reorganizeDirectionOptions(tree, undefined, ["dir-1", "dir-2"]);
      expect(opts).toEqual([
        { id: "dir-1", label: "Core Platform", active: true },
        { id: "dir-ext", label: "External Bot", active: false },
        { id: "dir-2", label: "dir-2", active: true },
      ]);
    });
  });

  describe("reorganizeTodos", () => {
    it("POSTs to /todos/reorganize with json body and headers", async () => {
      const updated = [makeTodo("t1", "Task 1", { part_of: "goal-1" })];
      api.mockResolvedValueOnce({ updated, transferred_ids: ["t1"] });

      const req = {
        ids: ["t1"],
        part_of: "goal-1",
        include_children: false,
        transfer_supervision: false,
      };
      const res = await reorganizeTodos(req);

      expect(api).toHaveBeenCalledWith(
        "/todos/reorganize",
        expect.objectContaining({
          method: "POST",
          json: req,
        }),
      );
      expect(res).toEqual({ updated, transferred_ids: ["t1"] });
    });
  });
});
