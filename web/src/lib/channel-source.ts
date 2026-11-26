// The channels as the conversations need them: the list (fetched once per hub) and which channel a message or a
// conversation came from. Kept apart from channels.ts, whose settings code (bind, test, state words) the first screen
// does not need: SegmentView draws a message's "From WeChat" mark, so this part is on the first screen
// (FIRST_SCREEN_BUDGET, docs/web/web.md) and the rest loads with Settings and History.
import { useEffect, useState, useSyncExternalStore } from "react";
import { api } from "./api";
import type { Channel } from "./channels";
import { itemHeaders } from "./manager-choice";
import { useStore } from "./store";

/** `manager`: whose channels (another manager's, docs/assistant/managers.md "Clients"); none: the default's. */
export async function listChannels(manager?: string): Promise<Channel[]> {
  const r = await api<{ channels?: Channel[] }>("/channels", { headers: itemHeaders(manager) });
  return r.channels ?? [];
}

// ── where a message came from ────────────────────────────

/** The channel a message came from, by its client_msg_id's prefix (`wechat-…`), when it is one there is. */
export function channelOf(clientMsgId: string | undefined, list: Pick<Channel, "id" | "name" | "kind">[]): Pick<Channel, "id" | "name" | "kind"> | undefined {
  if (!clientMsgId) return undefined;
  const dash = clientMsgId.indexOf("-");
  if (dash <= 0) return undefined;
  const head = clientMsgId.slice(0, dash);
  return list.find((c) => c.id === head && c.kind !== "app");
}

/** The name to show for a conversation's latest channel (Session.channels), "" when it has none known. */
export function conversationChannel(ids: string[] | undefined, list: Pick<Channel, "id" | "name">[]): Pick<Channel, "id" | "name"> | undefined {
  const id = ids?.[0];
  return id ? list.find((c) => c.id === id) ?? { id, name: id } : undefined;
}

// The list, fetched once per hub (it changes when an extension comes or goes: the settings page reloads it), for the
// source marks in conversations. forgetChannels() starts a new generation: a read already out when it was called is
// not cached, and the hooks already mounted read again. A read is shared by every hook asking under the same
// generation and features key, and cached under the key it started with.
let cached: { key: string; list: Channel[] } | null = null;
let inflight: { gen: number; key: string; promise: Promise<Channel[]> } | null = null;
let generation = 0;
const listeners = new Set<() => void>();

function subscribeGeneration(on: () => void) {
  listeners.add(on);
  return () => void listeners.delete(on);
}

const currentGeneration = () => generation;

/** The shared read for this generation and features key, started when there is none. */
function readChannels(key: string) {
  if (inflight && inflight.gen === generation && inflight.key === key) return inflight;
  const run = { gen: generation, key, promise: listChannels() };
  inflight = run;
  run.promise.then((l) => {
    if (run.gen === generation) cached = { key: run.key, list: l };
  }, () => {}).finally(() => {
    if (inflight === run) inflight = null;
  });
  return run;
}

/** The channels as the conversations need them (names for the source marks); [] on a hub without them. */
export function useChannels(): Channel[] {
  const has = useStore((s) => s.features.includes("channels"));
  const key = useStore((s) => s.features.join(","));
  // The third argument is the server snapshot: renderToString (used by the bubble tests) needs one.
  const gen = useSyncExternalStore(subscribeGeneration, currentGeneration, currentGeneration);
  const [list, setList] = useState<Channel[]>(() => (cached && cached.key === key ? cached.list : []));
  useEffect(() => {
    if (!has) return;
    if (cached?.key === key) {
      setList(cached.list);
      return;
    }
    let live = true;
    readChannels(key).promise.then((l) => {
      if (live) setList(l);
    }, () => {});
    return () => void (live = false);
  }, [has, key, gen]);
  return has ? list : [];
}

/** Forget the list (the settings page changed something): drop the read in flight and have mounted hooks read again. */
export function forgetChannels() {
  cached = null;
  inflight = null;
  generation++;
  for (const on of listeners) on();
}
