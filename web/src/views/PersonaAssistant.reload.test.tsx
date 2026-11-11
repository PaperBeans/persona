// @vitest-environment jsdom
// Another assistant's conversation after a reload, in a new tab or from a link (follow-up to #3493): what the tab noted at
// the hand-off is gone, and a hub with the paged sessions list (`sessions_list`) pushes no rows to find it in. Its
// address names the computer it runs on and its manager (lib/conversation-page.ts): the page opens it there, the exact
// lookup brings its row (so it can be sent to), and its plan and briefing buttons name its manager. An address without
// them opens as before.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setToken } from "../lib/api";
import { conversationPage } from "../lib/conversation-page";
import { conversationHeaders, startPlan, type TaskPlan } from "../lib/feed";
import { sessionCan } from "../lib/format";
import { conversationRoute } from "../lib/inbox";
import { linkRoute } from "../lib/links";
import { resetManagerChoice, setRosterDefault } from "../lib/manager-choice";
import { resetRoster, setRoster, type ManagerList } from "../lib/managers";
import { parse, routePath } from "../lib/router";
import { getSession, getState, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { PersonaAssistant } from "./PersonaAssistant";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
vi.hoisted(() => {
  Object.assign(globalThis, {
    matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
    ResizeObserver: class { observe() {} unobserve() {} disconnect() {} },
    IntersectionObserver: class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } },
  });
});
// the conversation page as far as this needs it: the exact lookup ChatView makes (the real one) and whether it can send
vi.mock("./chat/ChatView", async () => {
  const { useRuntimeSession } = await vi.importActual<typeof import("../lib/runtime-session")>("../lib/runtime-session");
  const { sessionCan: can } = await vi.importActual<typeof import("../lib/format")>("../lib/format");
  return {
    ChatView: (p: { mid: string; sid: string; offline?: string }) => {
      const { session, detail } = useRuntimeSession(p.mid, p.sid);
      return <div data-chat={`${p.mid}/${p.sid}`} data-can-send={session && !detail.missing && can(session, "send") ? "1" : "0"} data-offline={p.offline ?? ""} />;
    },
  };
});
vi.mock("./assistant/Readiness", () => ({
  useReadinessGate: () => ({ readiness: null }), ReadinessCard: () => null, ChatOnlyNote: () => null, FailureCard: () => null,
}));

const ROSTER: ManagerList = { default: "assistant", managers: [{ id: "assistant", name: "Persona", online: true }, { id: "m-work", name: "Work", online: true }] };
const WORK_CHAT: Session = { machine_id: "macbook", session_id: "work-chat", manager: "m-work", assistant: true, state: "idle", status: "idle", title: "Hand-off", cwd: "/" } as Session;

let host: HTMLDivElement;
let root: Root;
let calls: { url: string; method: string; header: string | null }[];
const page = (rows: Session[]) => ({ sessions: rows, remove: [], has_more: false, revision: "r1", total: rows.length });

beforeEach(() => {
  localStorage.clear();
  resetManagerChoice();
  reset();
  resetRoster();
  setToken("t");
  setRosterDefault("assistant");
  setRoster(ROSTER);
  calls = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, method: init?.method ?? "GET", header: new Headers(init?.headers).get("X-Persona-Manager") });
    const q = new URL(url, "http://hub.test").searchParams;
    // the exact lookup: the hub has the row on its own computer only
    const body = url.startsWith("/api/v1/sessions?") ? page(q.get("machine_id") === "macbook" && q.get("session_id") === "work-chat" ? [WORK_CHAT] : [])
      : url === "/api/v1/assistant" ? { machine_id: new Headers(init?.headers).get("X-Persona-Manager") === "m-work" ? "macbook" : "hub", conversations: [] }
      : url.includes("/task-plans/") ? { plan: { id: "p1", conversation: "work-chat", tasks: [], state: "started" } }
      : url.startsWith("/api/v1/todos") ? { todos: [] } : {};
    return { ok: true, status: 200, text: async () => JSON.stringify(body), headers: new Headers() } as Response;
  }));
  // a hub with the paged list and the exact lookup: no `sessions` pushes, nothing listed until a page asks
  Object.assign(getState(), { conn: "online", features: ["sessions_list", "sessions_lookup"], sessions: {}, assistant: { machine_id: "hub", main: "main-a", conversations: [] } });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  resetRoster();
  resetManagerChoice();
  vi.unstubAllGlobals();
});

const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
/** The page at an address, as App renders it. */
const open = async (address: string) => {
  const [path, search = ""] = address.split("?");
  const r = parse(path, search ? `?${search}` : "");
  if (r.name !== "persona") throw new Error(`not a conversation page: ${address}`);
  act(() => root.render(<PersonaAssistant sid={r.sid} routed={r.mid || r.manager ? { mid: r.mid, manager: r.manager } : undefined} onToggleSidebar={() => {}} />));
  await flush();
};
const chat = () => host.querySelector<HTMLElement>("[data-chat]");

describe("another assistant's conversation opened by its address alone", () => {
  it("opens on its own computer after a reload, can be sent to, and its plans and questions name its manager", async () => {
    await open(routePath(conversationPage("work-chat", "macbook", "m-work")));
    expect(chat()?.dataset.chat).toBe("macbook/work-chat");
    // the exact lookup asked its computer and put its row in the store
    expect(calls.some((c) => c.url.includes("machine_id=macbook") && c.url.includes("session_id=work-chat"))).toBe(true);
    expect(getSession("macbook", "work-chat")?.manager).toBe("m-work");
    expect(chat()?.dataset.canSend).toBe("1");
    expect(sessionCan(getSession("macbook", "work-chat"), "send")).toBe(true);
    expect(conversationHeaders("work-chat")).toEqual({ "X-Persona-Manager": "m-work" });
    const plan: TaskPlan = { id: "p1", conversation: "work-chat", tasks: [], state: "open" };
    await act(async () => { await startPlan(plan, false); });
    expect(calls.find((c) => c.url === "/api/v1/task-plans/p1/start")?.header).toBe("m-work");
  });

  it("does not put the current assistant's offline line on it", async () => {
    setRoster({ ...ROSTER, managers: [{ id: "assistant", name: "Persona", online: false, last_seen: new Date().toISOString() }, ROSTER.managers[1]] });
    Object.assign(getState(), { assistant: null, assistantError: "offline" });
    await open("/persona/work-chat?machine=macbook&manager=m-work");
    expect(chat()?.dataset.chat).toBe("macbook/work-chat");
    expect(chat()?.dataset.offline).toBe("");
  });

  it("reads the computer of a conversation its address names by manager only (a to-do's origin)", async () => {
    await open(routePath(conversationPage("work-chat", undefined, "m-work")));
    expect(calls.find((c) => c.url === "/api/v1/assistant")?.header).toBe("m-work");
    expect(chat()?.dataset.chat).toBe("macbook/work-chat");
    expect(conversationHeaders("work-chat")).toEqual({ "X-Persona-Manager": "m-work" });
  });

  it("names the address's manager on plan requests when the store has no row (wrong computer in the address)", async () => {
    await open("/persona/work-chat?machine=wrong&manager=m-work");
    expect(getSession("wrong", "work-chat")).toBeUndefined();
    expect(conversationHeaders("work-chat")).toEqual({ "X-Persona-Manager": "m-work" });
    await act(async () => { await startPlan({ id: "p1", conversation: "work-chat", tasks: [], state: "open" }, false); });
    expect(calls.find((c) => c.url === "/api/v1/task-plans/p1/start")?.header).toBe("m-work");
  });

  it("opens an address without them as before: on the current assistant's computer", async () => {
    await open("/persona/work-chat");
    expect(chat()?.dataset.chat).toBe("hub/work-chat");
  });
});

describe("the conversation page's address", () => {
  it("names the computer and another manager with several assistants, round trip", () => {
    const r = conversationPage("work-chat", "macbook", "m-work");
    expect(routePath(r)).toBe("/persona/work-chat?machine=macbook&manager=m-work");
    expect(parse("/persona/work-chat", "?machine=macbook&manager=m-work")).toEqual(r);
    // the default's names no manager; without a computer the manager is named, the default's too
    expect(routePath(conversationPage("main-a", "hub", "assistant"))).toBe("/persona/main-a?machine=hub");
    expect(routePath(conversationPage("c", undefined, undefined))).toBe("/persona/c?manager=assistant");
  });

  it("stays the plain address with one assistant", () => {
    setRoster({ default: "assistant", managers: [ROSTER.managers[0]] });
    expect(routePath(conversationPage("c", "hub", "assistant"))).toBe("/persona/c");
    expect(parse("/persona/c", "")).toEqual({ name: "persona", sid: "c" });
  });

  it("is what an inbox item's conversation and a link to one open", () => {
    const sessions = { macbook: [WORK_CHAT] };
    expect(conversationRoute("macbook", "work-chat", sessions, null)).toEqual({ name: "persona", sid: "work-chat", mid: "macbook", manager: "m-work" });
    expect(linkRoute("https://hub.example.com/persona/work-chat?machine=macbook&manager=m-work", false, "https://hub.example.com"))
      .toEqual({ kind: "conversation", sid: "work-chat", mid: "macbook", manager: "m-work" });
  });
});
