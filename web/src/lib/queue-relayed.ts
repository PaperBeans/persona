// A Persona chat's queued line from another conversation, read apart for the queue panel (QueuePanel.tsx). Kept out of
// lib/queue.ts, which the first screen loads (ChatView), so only the panel's lazy chunk carries it.

/** Read the hub's queue label (queuedRelays in conversations.go) in either form:
 *  来自「<title>」：<text> or 来自「<title>」（不唤醒）：<text>. Other notices keep their own text. */
export function relayedFrom(raw: string): { from: string; text: string; noWake: boolean } | null {
  const m = /^来自「([^\n]*?)」(（不唤醒）)?：/.exec(raw);
  const from = m?.[1].trim();
  return m && from ? { from, text: raw.slice(m[0].length), noWake: m[2] !== undefined } : null;
}
