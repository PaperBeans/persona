/// <reference types="node" />
// The sidebar's By-to-do tree (Sidebar.tsx, on every first screen) needs only consumeBoardSidebarTree. Importing it from
// board-consumer.ts put the Todos page's status and direction consumers (a lazy page's code) there too.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import * as consumer from "./board-consumer";
import * as tree from "./board-sidebar-tree";

const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");

describe("board-sidebar-tree", () => {
  it("keeps the Todos page's board consumers off the first screen", () => {
    expect(read("../../views/Sidebar.tsx")).not.toMatch(/^import\s+(?!type\b)[^;]*from\s+["'](\.\.\/)+features\/todos\/board-consumer["']/m);
    expect(read("./board-sidebar-tree.ts")).not.toMatch(/^import\s+(?!type\b)[^;]*from\s+["']\.\/board-consumer["']/m);
  });

  it("is the one tree board-consumer.ts still exports", () => {
    expect(consumer.consumeBoardSidebarTree).toBe(tree.consumeBoardSidebarTree);
  });
});
