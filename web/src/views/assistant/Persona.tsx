// Persona's look in the page (avatar, docs/apple/persona.md, docs/extensions/extensions.md "Avatar"; the app's PersonaStore / PersonaViews):
// the looks the hub offers (GET /looks: the built-in ones, its packages and the extensions'), the one chosen — the user's, kept on the hub
// so every device shows it — and its face (a still avatar) and dot (the state's APNG, which the browser animates by itself). The default
// dumpling (MochiFace) is drawn locally and also shows while a chosen package is not there. An older hub without /looks gets the packages
// from /personas and the choice stays this browser's. The picker UI lives in PersonaPicker.tsx so it stays off the cold first screen.
import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { api, ApiError, errorText, getToken } from "../../lib/api";
import {
  avatarFile, BUILTIN_PERSONA, dotFile, isBuiltinPersona, localized, lookPackages, normalizePersonaChoice, personaFileURL,
  personaFiles, usablePersonas, withChosenReady, type LookCurrent, type LookView, type PersonaInfo, type PersonaState,
} from "../../lib/persona";
import { isResumed } from "../../lib/reconnect";
import { onHubEvent } from "../../lib/store";
import { cx } from "../../ui/atoms";
import { MochiFace } from "./MochiFace";
import { useReducedMotion } from "./face-motion";
import { personaAccent, setPersonaAccent } from "../../ui/look";
import { currentLanguage, t } from "../../i18n";
import { setLookIcon } from "../../lib/look-icon";

const CHOSEN_KEY = "persona.persona";

export interface PersonaStoreState {
  list: PersonaInfo[];
  /** GET /looks's list; null on a hub without it. */
  looks: LookView[] | null;
  loaded: boolean;
  error: string;
  chosen: string;
}

let ps: PersonaStoreState = { list: [], looks: null, loaded: false, error: "", chosen: readChosen() };
const listeners = new Set<() => void>();
let loading: Promise<void> | null = null;
/** Whether the hub keeps the choice (it answered /looks): then choosing goes there. */
let synced = false;
/** Counts the choices made on this page and pushed from other devices: a list read that left before one keeps it. */
let choices = 0;
/** A choice made before the hub was known to keep it (no /looks answer yet): it is sent once one comes. */
let unsent = false;
/** Counts the looks uploaded, edited and deleted from this page: a list read that left before one is older than what it shows. */
let edits = 0;

function readChosen() {
  try {
    const saved = localStorage.getItem(CHOSEN_KEY) || BUILTIN_PERSONA;
    const chosen = normalizePersonaChoice(saved);
    if (saved !== chosen) saveChosen(chosen);
    return chosen;
  } catch {
    return BUILTIN_PERSONA;
  }
}

/** Whether this browser chose a look itself before the hub kept the choice (what moves up to the hub). */
function choseHere(): string | null {
  try {
    return localStorage.getItem(CHOSEN_KEY);
  } catch {
    return null;
  }
}

function saveChosen(id: string) {
  try {
    if (id === BUILTIN_PERSONA) localStorage.removeItem(CHOSEN_KEY);
    else localStorage.setItem(CHOSEN_KEY, id);
  } catch {
    // private mode: this page only
  }
}

function setPS(patch: Partial<PersonaStoreState>) {
  ps = { ...ps, ...patch };
  // Theme color "Match avatar" (ui/look.ts) keeps the chosen persona's accent, from the last list the hub gave; while there is no
  // list yet (or the last load failed) and the chosen one is not in it, the saved accent stays.
  const shown = ps.list.find((p) => p.id === ps.chosen);
  if (isBuiltinPersona(ps.chosen)) setPersonaAccent(null);
  else if (shown || (ps.loaded && !ps.error)) setPersonaAccent(personaAccent(shown?.manifest.colors));
  setLookIcon(shown ?? null);
  listeners.forEach((l) => l());
}

/** The choice to keep given the hub's: the hub's once it has one; else this browser's moves up (once, if unset there). */
async function settleChoice(cur: LookCurrent): Promise<string> {
  if (cur.id) return normalizePersonaChoice(cur.id);
  // one chosen on this page before this answer is sent as it is once the answer is in (refreshPersonas)
  if (unsent) return BUILTIN_PERSONA;
  const mine = choseHere();
  if (!mine) return BUILTIN_PERSONA;
  try {
    const r = await api<LookCurrent>("/looks/current", { method: "PUT", json: { id: mine, if_unset: true } });
    return normalizePersonaChoice(r.id ?? BUILTIN_PERSONA);
  } catch {
    // the hub does not know it (the page's old mark, a package gone): the default
    return BUILTIN_PERSONA;
  }
}

/** What the hub offers now (each time the picker opens, once per page load, and when the hub says the list changed). */
export function refreshPersonas(): Promise<void> {
  if (loading) return loading;
  // the hub's `current` is its choice when the request left: one made since (here, or another device's push) stays
  const asked = choices;
  // a look uploaded, edited or deleted here while this read is on its way: what it lists is older, and another read follows
  const edited = edits;
  let again = false;
  loading = api<{ looks: LookView[]; current: LookCurrent }>("/looks")
    .then(async (r) => {
      synced = true;
      return { list: usablePersonas(lookPackages(r.looks)), looks: r.looks, chosen: await settleChoice(r.current) };
    })
    .catch(async (e) => {
      if (!(e instanceof ApiError && e.status === 404)) throw e;
      synced = false; // an older hub: its packages, this browser's choice
      const r = await api<{ personas: PersonaInfo[] }>("/personas");
      return { list: usablePersonas(r.personas), looks: null, chosen: ps.chosen };
    })
    .then(async ({ list, looks, chosen: answered }) => {
      const pick = () => (choices === asked && !unsent ? answered : ps.chosen);
      const chosen = pick();
      const next = list.find((p) => p.id === chosen);
      const shown = ps.list.find((p) => p.id === chosen);
      const ready = !next || !shown || next.rev === shown.rev || (await preload(next));
      const now = pick(); // chosen again while the files loaded: that one was not waited for, it shows as it comes
      if (edits !== edited) {
        again = true;
        return;
      }
      if (now === answered) saveChosen(now);
      setPS({ list: withChosenReady(list, ps.list, now, now === chosen ? ready : true), looks, chosen: now, loaded: true, error: "" });
      // chosen before the hub was known to keep the choice: it goes now
      if (unsent) {
        unsent = false;
        if (synced) void api("/looks/current", { method: "PUT", json: { id: now } }).catch(() => undefined);
      }
    })
    .catch((e) => setPS({ loaded: true, error: e instanceof ApiError && e.status === 404 ? t("assistant-persona.hub_unsupported") : t("assistant-persona.load_failed", { error: errorText(e) }) }))
    .finally(() => {
      loading = null;
      if (again) void refreshPersonas();
    });
  return loading;
}

/** A read that leaves after now: one already on its way left before, so another follows it. */
export function readPersonasAgain() {
  const again = () => void refreshPersonas();
  if (loading) loading.then(again, again);
  else again();
}

/** Every file of the package in the browser's cache before it is shown; false when one did not come. */
function preload(p: PersonaInfo): Promise<boolean> {
  const token = getToken();
  return Promise.all(
    personaFiles(p.manifest).map(
      (f) =>
        new Promise<boolean>((done) => {
          const img = new Image();
          img.onload = () => done(true);
          img.onerror = () => done(false);
          img.src = personaFileURL(p, f, token);
        }),
    ),
  ).then((all) => all.every(Boolean));
}

// The list changed (a tiered look moved, an extension with looks came or went): list again, the new files loaded first.
onHubEvent("personas", () => void refreshPersonas());
// A reconnect the hub could not resume: a `personas` or `look` push missed meanwhile is not replayed (#4954). The read
// takes the hub's choice as a `look` push would; a page that has not read the looks yet reads them when it shows one.
// A read already on its way left before the drop: another one follows it.
onHubEvent("hello", (msg) => {
  if (isResumed(msg) || !ps.loaded) return;
  readPersonasAgain();
});
// Another device chose a look (or the hub went back to the default when its extension went).
onHubEvent("look", (m) => {
  const pushed = typeof m.id === "string" ? m.id : "";
  if (!pushed) return;
  const id = normalizePersonaChoice(pushed);
  choices++; // the hub's choice now, even when this page already shows it: a list read that left before is older
  if (id === ps.chosen) return;
  saveChosen(id);
  if (!isBuiltinPersona(id) && !ps.list.some((p) => p.id === id)) void refreshPersonas();
  setPS({ chosen: id });
});

/** Choosing one keeps it when its package leaves the hub (the built-in shows meanwhile), as on the phone; on a hub that
 *  keeps the choice, every device follows. */
export function choosePersona(choice: string) {
  const id = normalizePersonaChoice(choice);
  choices++;
  saveChosen(id);
  setPS({ chosen: id });
  if (synced) void api("/looks/current", { method: "PUT", json: { id } }).catch(() => undefined);
  else unsent = true; // the list being read says whether this hub keeps it
}

/** Store hooks for PersonaPicker.tsx (kept lazy off the first screen). */
export function personaSnapshot(): PersonaStoreState {
  return ps;
}
export function patchPersonaStore(patch: Partial<PersonaStoreState>) {
  setPS(patch);
}
export function recordPersonaEdit() {
  edits++;
}
/** Where the choices stand: taken as a request that makes the hub choose (an upload, a delete) leaves. */
export function personaChoiceMark(): number {
  return choices;
}
/**
 * The hub's choice in the answer to an upload or a delete that left at `since` (personaChoiceMark): the newest, as a
 * `look` push would be, unless another choice came meanwhile (another device's push, or one made here) — the hub pushes
 * its own before it answers, so which of the two it kept last only the hub can say. Then the one on screen stays and a
 * fresh read settles it.
 */
export function takePersonaChoice(id: string, since: number): string {
  if (choices !== since && id !== ps.chosen) {
    readPersonasAgain();
    return ps.chosen;
  }
  choices++;
  saveChosen(id);
  return id;
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

export function usePersonaStore(): PersonaStoreState {
  // The third arg is the server snapshot, so views rendered with renderToStaticMarkup work (the same shape as
  // useRoster); the module-level state is its stable value.
  const s = useSyncExternalStore(subscribe, () => ps, () => ps);
  useEffect(() => {
    if (!ps.loaded && !loading) void refreshPersonas();
  }, []);
  return s;
}

export interface CurrentPersona {
  /** The package on screen; null: a built-in. */
  info: PersonaInfo | null;
  /** The built-in look on screen (the chosen one, or the default while a chosen package is not there); null with a package. */
  builtin: string | null;
  /** Mochi is on screen. */
  mochi: boolean;
  name: string;
  chosen: string;
}

/** The look on screen: the chosen package once the hub listed it, else the built-in (named Persona). */
export function usePersona(): CurrentPersona {
  const s = usePersonaStore();
  const info = isBuiltinPersona(s.chosen) ? null : (s.list.find((p) => p.id === s.chosen) ?? null);
  const builtin = info ? null : isBuiltinPersona(s.chosen) ? s.chosen : BUILTIN_PERSONA;
  const name = info ? localized(info.manifest.name, info.manifest.names, currentLanguage()) : "Persona";
  return { info, builtin, mochi: builtin === BUILTIN_PERSONA, name, chosen: s.chosen };
}

/** Whether the page is dark now (the theme's class on <html>), for the dark avatar. */
export function useDarkPage(): boolean {
  const read = () => typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  const [dark, setDark] = useState(read);
  useEffect(() => {
    if (typeof MutationObserver === "undefined") return;
    const o = new MutationObserver(() => setDark(read()));
    o.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => o.disconnect();
  }, []);
  return dark;
}

/**
 * A still face in a circle: the package's avatar at the size it needs (its SVG, its dark one on a dark page), else the
 * built-in look on screen. With `persona` given (a card in the picker), that package's, or `fallback` when its file
 * does not load.
 */
export function PersonaAvatar({ size, persona, fallback, className }: { size: number; persona?: PersonaInfo | null; fallback?: React.ReactNode; className?: string }) {
  const current = usePersona();
  const dark = useDarkPage();
  const p = persona === undefined ? current.info : persona;
  const [broken, setBroken] = useState(false);
  const file = p ? avatarFile(p.manifest, size * Math.max(2, Math.ceil(typeof devicePixelRatio === "number" ? devicePixelRatio : 2)), dark) : undefined;
  useEffect(() => setBroken(false), [p?.id, p?.rev]);
  if (!p || !file || broken) return persona === undefined ? <MochiFace size={size} className={className} /> : <>{fallback}</>;
  return (
    <img
      src={personaFileURL(p, file, getToken())}
      alt=""
      aria-hidden
      draggable={false}
      onError={() => setBroken(true)}
      width={size}
      height={size}
      className={cx("shrink-0 rounded-full object-cover", className)}
      style={{ width: size, height: size }}
    />
  );
}

/**
 * The face moving for a state (the top bar's dot): the state's frames, looping. With reduced motion only the first frame,
 * which says the state by itself (a canvas draws an animated image's first frame). The built-in look drawn in that
 * state when the look on screen is one; `fallback` for a given package whose frames do not load.
 */
export function PersonaDot({ size, state, persona, fallback, className }: { size: number; state: PersonaState; persona?: PersonaInfo | null; fallback?: React.ReactNode; className?: string }) {
  const current = usePersona();
  const p = persona === undefined ? current.info : persona;
  const still = useReducedMotion();
  const [broken, setBroken] = useState(false);
  const file = p ? dotFile(p.manifest, state) : undefined;
  const url = p && file ? personaFileURL(p, file, getToken()) : "";
  useEffect(() => setBroken(false), [url]);
  if (!p || !file || broken) return persona === undefined ? <MochiFace size={size} state={state} className={className} /> : <>{fallback}</>;
  if (still) return <StillFrame url={url} size={size} className={className} onError={() => setBroken(true)} />;
  return (
    <img
      src={url}
      alt=""
      aria-hidden
      draggable={false}
      onError={() => setBroken(true)}
      width={size}
      height={size}
      className={cx("shrink-0 object-contain", className)}
      style={{ width: size, height: size }}
    />
  );
}

function StillFrame({ url, size, className, onError }: { url: string; size: number; className?: string; onError: () => void }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const img = new Image();
    let live = true;
    img.onload = () => {
      const c = ref.current;
      if (!live || !c) return;
      const k = Math.max(1, Math.ceil(typeof devicePixelRatio === "number" ? devicePixelRatio : 1));
      c.width = size * k;
      c.height = size * k;
      const g = c.getContext("2d");
      g?.clearRect(0, 0, c.width, c.height);
      g?.drawImage(img, 0, 0, c.width, c.height);
    };
    img.onerror = () => live && onError();
    img.src = url;
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url, size]);
  return <canvas ref={ref} aria-hidden className={cx("shrink-0", className)} style={{ width: size, height: size }} />;
}
