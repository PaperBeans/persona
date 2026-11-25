// Reads a hub hint wakes, shared by every view in this page that follows the same path (#15910). A busy fleet sends a
// `sessions_list` hint for every agent's update; a counts read that answered each one made a request per hint per open
// list, so the browser's requests rose with the socket's frames. Here one path has one reader: the first hint after a
// quiet stretch reads at once, the hints of the next window share one read at its end, and a hint that lands while a
// read is out gets one trailing read after it, so the last change of a burst is always read. Every follower of the
// path gets each answer.
import { api } from "./api";
import { onShownHint } from "./shown-hints";
import { documentPage } from "./visible-poll";

type Page = Parameters<typeof onShownHint>[2];

interface Follower { value: (v: unknown) => void; error: (e: unknown) => void }

interface Reader {
  followers: Set<Follower>;
  controller: AbortController;
  /** When the last read came back (0 before the first): the window counts from there, so a slow hub still gets a
   *  quiet stretch between a burst's reads. */
  doneAt: number;
  reading: boolean;
  /** A hint came while the read was out: read once more after it. */
  dirty: boolean;
  timer?: ReturnType<typeof setTimeout>;
  last?: { value: unknown } | { error: unknown };
  off: () => void;
}

/** The hints of one window share one read: the same window as a SessionList's (`invalidate` in lib/session-list.ts). */
export const HINT_WINDOW_MS = 2_000;

const readers = new Map<string, Reader>();

function keyOf(event: string, path: string): string { return `${event}\u0000${path}`; }

function read(key: string, reader: Reader, path: string): void {
  clearTimeout(reader.timer);
  reader.timer = undefined;
  reader.reading = true;
  reader.dirty = false;
  const settle = (last: Reader["last"]) => {
    if (readers.get(key) !== reader) return;
    reader.reading = false;
    reader.doneAt = Date.now();
    reader.last = last;
    for (const follower of [...reader.followers]) {
      if ("value" in last!) follower.value(last.value);
      else follower.error(last!.error);
    }
    if (reader.dirty) hint(key, reader, path);
  };
  void api<unknown>(path, { signal: reader.controller.signal }).then((value) => settle({ value }), (error: unknown) => {
    if (reader.controller.signal.aborted) return;
    settle({ error });
  });
}

function hint(key: string, reader: Reader, path: string): void {
  if (reader.reading) { reader.dirty = true; return; }
  if (reader.timer !== undefined) return; // the window's read is set already: this hint shares it
  const wait = reader.doneAt + HINT_WINDOW_MS - Date.now();
  if (wait <= 0) read(key, reader, path);
  else reader.timer = setTimeout(() => read(key, reader, path), wait);
}

/** Follow `path`'s answer, read now (or, when another view already follows it, its last answer at once and a read
 *  within the window) and after each `event` hint, shared as above. Hints while the page is hidden wait for it to be
 *  shown (`onShownHint`). Returns the unfollow; the last one to leave aborts the read out. */
export function followHinted<T>(event: string, path: string, value: (v: T) => void, error: (e: unknown) => void, page: Page = documentPage): () => void {
  const key = keyOf(event, path);
  const follower: Follower = { value: value as (v: unknown) => void, error };
  let reader = readers.get(key);
  if (!reader) {
    const created: Reader = { followers: new Set(), controller: new AbortController(), doneAt: 0, reading: false, dirty: false, off: () => {} };
    readers.set(key, created);
    created.off = onShownHint(event, () => hint(key, created, path), page);
    created.followers.add(follower);
    read(key, created, path);
    reader = created;
  } else {
    reader.followers.add(follower);
    const last = reader.last;
    if (last) {
      if ("value" in last) follower.value(last.value);
      else follower.error(last.error);
    }
    // a read out now answers this follower too. Otherwise a failed last read (a Retry) is read again at once, and a
    // last answer that may be a window old gets one more read, shared with the others and inside the window.
    if (!reader.reading) {
      if (last && "error" in last) read(key, reader, path);
      else hint(key, reader, path);
    }
  }
  const held = reader;
  return () => {
    if (!held.followers.delete(follower) || held.followers.size) return;
    if (readers.get(key) === held) readers.delete(key);
    clearTimeout(held.timer);
    held.controller.abort();
    held.off();
  };
}
