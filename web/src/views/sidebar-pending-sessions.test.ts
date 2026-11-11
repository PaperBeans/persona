import { describe, expect, it, vi } from "vitest";
import type { Session } from "../lib/types";
import { sidebarPending } from "./sidebar-pending";
import { readPendingWorkSessions } from "./sidebar-pending-sessions";

const session = (id: string, state: Session["state"], extra: Partial<Session> = {}): Session => ({
  machine_id: "machine", session_id: id, title: id, cwd: "/work/example", status: "idle", state, ...extra,
});

describe("pending sessions transport", () => {
  it("selects the manager's complete pending work set instead of the newest general page", async () => {
    const idle = Array.from({ length: 50 }, (_, i) => session(`idle-${i}`, "idle"));
    const recentPending = Array.from({ length: 50 }, (_, i) => session(`pending-${i}`, "needs_you"));
    const older = session("older work", "needs_you", { assistant: true });
    const firstGeneralPage = [...idle, ...recentPending, older].slice(0, 50);
    expect(sidebarPending(firstGeneralPage, [], {})).toEqual([]);

    const read = vi.fn(async (path: string) => {
      const { searchParams: q } = new URL(path, "https://example.com");
      expect(q.get("scope")).toBe("work");
      expect(q.has("agents_only")).toBe(false);
      expect(q.get("state")).toBe("needs_you");
      expect(q.get("manager")).toBe("manager");
      expect(q.get("summary")).toBe("1");
      const matches = [...idle, ...recentPending, older].filter((s) => s.state === q.get("state"));
      const start = Number(q.get("cursor") || 0);
      const limit = Number(q.get("limit"));
      const end = start + limit;
      return { sessions: matches.slice(start, end), has_more: end < matches.length, next_cursor: String(end), revision: "1", total: matches.length, remove: [] };
    });
    const { sessions } = await readPendingWorkSessions("manager", new AbortController().signal, read);
    expect(read).toHaveBeenCalledTimes(2);
    const rows = sidebarPending(sessions, [{ id: "card", machine_id: "machine", session_id: older.session_id, kind: "permission" }], {});
    expect(rows.filter((r) => r.session?.session_id === older.session_id)).toHaveLength(1);
    expect(sidebarPending(sessions, [], {}).find((r) => r.session?.session_id === older.session_id)?.cards).toEqual([]);
  });
});
