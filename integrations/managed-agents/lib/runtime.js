// The Managed Agents runtime (docs/integrations/managed-agents.md): Managed Agents sessions from one or more
// profiles (an Ark cloud endpoint, a self-hosted server) as Persona sessions. It lists them, pages their event logs
// as items, sends messages and interrupts, starts new sessions on a configured agent, streams live text while a
// session is watched, and asks the phone what the agent waits on a person for.

import { readFile, writeFile, mkdir, rename, stat } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { createRuntime, interactionId, light, page, RpcError } from '../../runtime-sdk/index.js'
import { ApiError, createClient } from './client.js'
import { defaultConfigPath, loadConfig, RUNTIME_ID, RUNTIME_NAME } from './config.js'
import { answerOf, cardOf, deltaText, eventItems, lastText, logStatus, pendingAsks } from './map.js'
import { createSecrets } from './secrets.js'

const LIST_EVERY_MS = 30_000
const CONFIG_EVERY_MS = 10_000
const KEEP_STARTED = 200
const SYNC_CONCURRENCY = 4

/**
 * @param {object} o
 * @param {string} [o.configPath] the config file (default $PERSONA_HOME/managed-agents.json or ~/.persona/managed-agents.json)
 * @param {object} [o.config] a parsed config instead of the file (tests)
 * @param {string} [o.statePath] where the ids of sessions started from Persona are kept (no keys, no content)
 * @param {Function} [o.fetch] fetch for the REST calls (tests)
 * @param {object} [o.env] the environment key references read (env:, extension:)
 * @param {Function} [o.log]
 * @param {number} [o.syncConcurrency] max concurrent /events sync calls (default 4)
 * Other options go to createRuntime (socketPath, listen, retryMs, answerFirst…).
 */
export function createManagedAgentsRuntime(o = {}) {
  const env = o.env ?? process.env
  const rawLog = o.log ?? o.runtimeOptions?.log ?? (() => {})
  const secrets = createSecrets({ env, log: rawLog, run: o.run, platform: o.platform })
  const say = (m) => rawLog(secrets.redact(m))
  const configPath = o.configPath ?? (o.config ? undefined : defaultConfigPath(undefined, env))
  const statePath = o.statePath ?? join(dirname(configPath ?? defaultConfigPath(undefined, env)), 'managed-agents-state.json')
  const pollScale = o.pollScale ?? 1
  const syncConcurrency = Math.max(1, o.syncConcurrency ?? SYNC_CONCURRENCY)

  let config = {
    runtime: o.config?.runtime || RUNTIME_ID,
    name: o.config?.name || RUNTIME_NAME,
    profiles: o.config?.profiles ?? [],
    problems: o.config?.problems ?? [],
  }
  let configStamp = ''
  const profiles = new Map() // id -> { profile, client, listUnsupported, rows: Map(maId -> session), polling }
  const sessions = new Map() // key -> session state
  let started = [] // [{profile, id}] started from Persona: listed even where the server has no list route
  const warned = new Set()
  const timers = new Set()
  const pollTimers = new Map() // pid -> { ms, timer }
  let stopped = false
  let loopsRunning = false
  let rtStarted = false
  let rt

  const warnOnce = (key, msg) => {
    if (warned.has(key)) return
    warned.add(key)
    say(msg)
  }
  const keyOf = (pid, maId) => `${pid}.${maId}`
  const history = (s) => `ma:${s.key}`

  const sdkOptions = {
    runtime: o.runtimeOptions?.runtime ?? config.runtime,
    name: o.runtimeOptions?.name ?? config.name,
    launchers: [],
    launcher: undefined,
  }

  function launchers() {
    return config.profiles.filter((p) => p.start).map((p) => ({ name: p.start.launcher, display_name: p.start.label ?? p.name, agent: config.runtime, model: '' }))
  }

  function makeRuntime() {
    return createRuntime(driver, {
      ...o.runtimeOptions,
      runtime: sdkOptions.runtime,
      name: sdkOptions.name,
      version: o.runtimeOptions?.version ?? '0.1.0',
      launchers: sdkOptions.launchers,
      // A daemon's link passes one launcher on, named by daemons.json: the first profile that starts sessions.
      launcher: sdkOptions.launcher,
      log: say,
      onHub: (connected) => {
        if (rt?.connected) for (const s of sessions.values()) if (s.loaded) reconcileAsks(s)
        o.runtimeOptions?.onHub?.(connected)
      },
    })
  }

  function applyConfig(c) {
    const prevRuntime = sdkOptions.runtime
    const prevName = sdkOptions.name
    config = {
      runtime: c?.runtime || RUNTIME_ID,
      name: c?.name || RUNTIME_NAME,
      profiles: c?.profiles ?? [],
      problems: c?.problems ?? [],
    }
    for (const p of config.problems) warnOnce(`problem:${p}`, `config: ${p}`)
    const want = new Set(config.profiles.map((p) => p.id))
    for (const id of profiles.keys()) {
      if (!want.has(id)) {
        profiles.delete(id)
        for (const [k, s] of sessions) if (s.pid === id) dropSession(k)
      }
    }
    for (const p of config.profiles) {
      const had = profiles.get(p.id)
      if (had && JSON.stringify(had.profile) === JSON.stringify(p)) continue
      profiles.set(p.id, { profile: p, client: createClient(p, secrets, { fetch: o.fetch }), listUnsupported: false, rows: had?.rows ?? new Map(), polling: false })
      for (const s of sessions.values()) if (s.pid === p.id) s.client = profiles.get(p.id).client
    }
    const nextRuntime = o.runtimeOptions?.runtime ?? config.runtime
    const nextName = o.runtimeOptions?.name ?? config.name
    const identityChanged = nextRuntime !== prevRuntime || nextName !== prevName
    sdkOptions.runtime = nextRuntime
    sdkOptions.name = nextName
    sdkOptions.launchers = launchers()
    sdkOptions.launcher = sdkOptions.launchers[0]?.name
    syncPollTimers()
    if (!rtStarted) {
      rt = makeRuntime()
    } else if (identityChanged) {
      for (const s of sessions.values()) {
        for (const ctl of s.asking.values()) ctl.abort()
        s.asking.clear()
      }
      rt.stop()
      rt = makeRuntime()
      if (!stopped) void rt.start().catch((err) => say(`reconnect: ${err.message}`))
    } else {
      rt?.launchersChanged(sdkOptions.launchers)
      rt?.sessionsChanged(0)
    }
  }

  async function reloadConfig() {
    if (o.config) return
    let st
    try {
      st = await stat(configPath, { bigint: true })
    } catch {
      st = undefined
    }
    const stamp = st ? `${st.ino}:${st.mtimeNs}:${st.size}` : 'none'
    if (stamp === configStamp) return
    configStamp = stamp
    try {
      applyConfig(await loadConfig(configPath, { log: say }))
      say(`config: ${config.profiles.length} profile(s) from ${configPath}`)
      if (loopsRunning && !stopped) void refreshList().catch((err) => say(err.message))
    } catch (err) {
      say(`config: ${err.message}`)
    }
  }

  async function loadState() {
    try {
      const raw = JSON.parse(await readFile(statePath, 'utf8'))
      if (Array.isArray(raw.started)) started = raw.started.filter((x) => typeof x?.profile === 'string' && typeof x?.id === 'string').slice(-KEEP_STARTED)
    } catch {}
  }

  async function saveState() {
    try {
      await mkdir(dirname(statePath), { recursive: true, mode: 0o700 })
      const tmp = `${statePath}.${process.pid}.tmp`
      await writeFile(tmp, JSON.stringify({ started }), { mode: 0o600 })
      await rename(tmp, statePath)
    } catch (err) {
      say(`state: ${err.message}`)
    }
  }

  // --- sessions ------------------------------------------------------------------------------------------------

  function sessionFor(pid, maId) {
    const key = keyOf(pid, maId)
    let s = sessions.get(key)
    if (!s) {
      const pr = profiles.get(pid)
      if (!pr) return undefined
      s = { key, pid, maId, client: pr.client, meta: undefined, events: [], ids: new Set(), items: [], images: new Map(), loaded: false,
        chain: Promise.resolve(), asking: new Map(), dismissed: new Set(), watched: false, stream: undefined, deltas: new Map(), active: 0, failed: '' }
      sessions.set(key, s)
    }
    return s
  }

  function dropSession(key) {
    const s = sessions.get(key)
    if (!s) return
    s.stream?.abort()
    for (const a of s.asking.values()) a.abort()
    sessions.delete(key)
  }

  function sessionByKey(key) {
    const s = sessions.get(key)
    if (s) return s
    const dot = key.indexOf('.')
    if (dot > 0 && profiles.has(key.slice(0, dot))) return sessionFor(key.slice(0, dot), key.slice(dot + 1))
    throw new RpcError(`no Managed Agents session ${key}`, 'not_found')
  }

  /** Reads the session's new events, pushes their items, and asks what is pending. Serialized per session. */
  function sync(s) {
    const run = s.chain.then(() => syncNow(s))
    s.chain = run.catch(() => {})
    return run
  }

  async function syncNow(s) {
    if (stopped) return
    const after = s.events.at(-1)?.id
    let res
    try {
      res = await s.client.listEvents(s.maId, { after })
    } catch (err) {
      s.failed = err.message
      if (err instanceof ApiError && err.status === 404) {
        if (!s.loaded) sessions.delete(s.key) // asked about a session that is not ours (the node asks every runtime)
        throw new RpcError(err.message, 'not_found')
      }
      throw err
    }
    s.failed = ''
    const fresh = res.events.filter((e) => e && typeof e.id === 'string' && !s.ids.has(e.id) && typeof e.type === 'string')
    const from = s.events.length
    const added = []
    for (const ev of fresh) {
      s.ids.add(ev.id)
      s.events.push(ev)
      const items = eventItems(ev, s.events.length, s.images)
      s.items.push(...items)
      added.push(...items)
      if (ev.type === 'agent.message' || ev.type === 'agent.thinking') {
        if (s.deltas.delete(ev.id) && s.deltas.size === 0) rt.streamEnd(s.key)
      }
    }
    const wasLoaded = s.loaded
    s.loaded = true
    if (fresh.length > 0 || !wasLoaded) {
      rt?.append(s.key, { items: added.map(light), fromSeq: from, toSeq: s.events.length, history: history(s) })
      if (wasLoaded && fresh.some((e) => e.type.startsWith('session.status') || e.type === 'agent.message' || e.type === 'user.message')) s.active = Date.now()
      rt?.sessionsChanged(fresh.length > 0 ? 0 : 300)
    }
    if (logStatus(s.events) !== 'working' && s.deltas.size > 0) {
      s.deltas.clear()
      rt?.streamEnd(s.key)
    }
    reconcileAsks(s)
  }

  function reconcileAsks(s) {
    const p = profiles.get(s.pid)?.profile
    if (!p) return
    const pending = pendingAsks(s.events, p)
    const open = new Set(pending.map((a) => a.event.id))
    for (const [id, ctl] of s.asking) {
      if (!open.has(id)) {
        ctl.abort() // answered elsewhere (the Managed Agents console, another client): the card is withdrawn
        s.asking.delete(id)
      }
    }
    if (!rt?.connected) return
    for (const ask of pending) {
      const id = ask.event.id
      if (s.asking.has(id) || s.dismissed.has(id)) continue
      const ctl = new AbortController()
      s.asking.set(id, ctl)
      const card = { ...cardOf(ask), id: interactionId(config.runtime, s.key, ask.kind, id), session_id: s.key }
      rt.ask(card, { signal: ctl.signal })
        .then(async (resolution) => {
          if (ctl.signal.aborted) return
          const answer = answerOf(ask, resolution)
          if (!answer) {
            s.dismissed.add(id) // handed back: answered in the Managed Agents console, not asked again here
            return
          }
          await s.client.sendEvents(s.maId, [answer])
          s.active = Date.now()
          await sync(s)
        })
        .catch((err) => {
          if (!ctl.signal.aborted) say(`${s.key}: answering ${ask.kind}: ${err.message}`)
        })
        .finally(() => {
          if (s.asking.get(id) === ctl) s.asking.delete(id)
          rt.sessionsChanged(50)
        })
    }
  }

  function statusOf(s, meta) {
    const p = profiles.get(s?.pid)?.profile
    const fromLog = s?.loaded ? logStatus(s.events) : 'unknown'
    const waiting = s?.loaded && p && pendingAsks(s.events, p).length > 0
    if (waiting || fromLog === 'waiting') return 'blocked'
    if (fromLog === 'working') return 'working'
    if (fromLog === 'terminated') return 'offline'
    if (fromLog === 'done') return 'done'
    const st = meta?.status ?? ''
    if (st === 'running' || st === 'rescheduling' || st === 'starting') return 'working'
    if (st === 'terminated' || st === 'deleted' || st === 'archived') return 'offline'
    return 'done'
  }

  function rowOf(pr, meta) {
    const s = sessions.get(keyOf(pr.profile.id, meta.id))
    const many = config.profiles.length > 1
    const agentName = meta.agentName || meta.agentId || 'Managed Agents'
    const lastEvent = s?.events.at(-1)
    const updated = lastEvent?.processed_at ?? lastEvent?.created_at ?? meta.updatedAt
    return {
      session_id: keyOf(pr.profile.id, meta.id),
      title: meta.title || agentName,
      status: statusOf(s, meta),
      updated_at: updated || new Date(0).toISOString(),
      started_at: meta.createdAt || undefined,
      rev: s?.loaded ? s.events.length : Math.floor((Date.parse(meta.updatedAt) || 0) / 1000),
      last_text: s?.loaded ? lastText(s.events) : '',
      space_id: `${pr.profile.id}:${meta.agentId || 'default'}`,
      space_name: many ? `${agentName} · ${pr.profile.name}` : agentName,
      model: meta.model || undefined,
      started_by_persona: started.some((x) => x.profile === pr.profile.id && x.id === meta.id) || undefined,
    }
  }

  async function refreshList() {
    for (const pr of profiles.values()) {
      const p = pr.profile
      const rows = new Map()
      if (p.listEnabled && !pr.listUnsupported) {
        try {
          for (const meta of await pr.client.listSessions()) rows.set(meta.id, meta)
        } catch (err) {
          if (err instanceof ApiError && (err.status === 404 || err.status === 405)) {
            pr.listUnsupported = true
            warnOnce(`list:${p.id}`, `${p.id}: the server has no session list at ${p.prefix}${p.listPath}; listing only the sessions in "sessions" and those started from Persona`)
          } else {
            warnOnce(`listerr:${p.id}:${err.code}`, `${p.id}: listing sessions: ${err.message}`)
            for (const [id, meta] of pr.rows) rows.set(id, meta) // keep the last list through a failure
          }
        }
      }
      const extra = [...p.sessions, ...started.filter((x) => x.profile === p.id).map((x) => x.id)].filter((id) => !rows.has(id))
      for (const id of extra) {
        try {
          const meta = await pr.client.getSession(id)
          if (meta) rows.set(meta.id, meta)
        } catch (err) {
          warnOnce(`get:${p.id}:${id}:${err.code}`, `${p.id}: session ${id}: ${err.message}`)
          const had = pr.rows.get(id)
          if (had) rows.set(id, had)
        }
      }
      for (const [id, meta] of rows) {
        if (meta.archived || (p.agents.length > 0 && meta.agentId && !p.agents.includes(meta.agentId))) rows.delete(id)
      }
      pr.rows = rows
    }
    rt.sessionsChanged(0)
  }

  /** Sessions of one profile whose log is followed now: watched, running, waiting on someone, or busy in the last minutes. */
  function followedFor(pr) {
    const out = []
    const seen = new Set()
    const now = Date.now()
    const recentMs = pr.profile.recentMinutes * 60_000
    for (const meta of pr.rows.values()) {
      const s = sessions.get(keyOf(pr.profile.id, meta.id))
      const st = statusOf(s, meta)
      const recent = now - (Date.parse(meta.updatedAt) || 0) < recentMs || (s && now - s.active < recentMs)
      if (s?.watched || st === 'working' || st === 'blocked' || recent) {
        const sess = s ?? sessionFor(pr.profile.id, meta.id)
        if (sess && !seen.has(sess.key)) {
          seen.add(sess.key)
          out.push(sess)
        }
      }
    }
    for (const s of sessions.values()) {
      if (s.pid === pr.profile.id && s.watched && !seen.has(s.key)) {
        seen.add(s.key)
        out.push(s)
      }
    }
    return out
  }

  function followed() {
    const out = []
    for (const pr of profiles.values()) out.push(...followedFor(pr))
    return out
  }

  function every(ms, fn) {
    const t = setInterval(() => void fn().catch((err) => say(err.message)), ms)
    t.unref?.()
    timers.add(t)
  }

  let activeSyncs = 0
  const syncQueue = []
  function runBounded(fn) {
    return new Promise((resolve, reject) => {
      const run = () => {
        activeSyncs++
        Promise.resolve().then(fn).then(resolve, reject).finally(() => {
          activeSyncs--
          if (syncQueue.length > 0) syncQueue.shift()()
        })
      }
      if (activeSyncs < syncConcurrency) run()
      else syncQueue.push(run)
    })
  }

  async function syncSessions(list) {
    await Promise.all(list.map((s) => runBounded(() => (stopped ? undefined : sync(s).catch((err) => warnOnce(`sync:${s.key}:${err.code ?? err.message}`, `${s.key}: ${err.message}`))))))
  }

  async function pollProfile(pr) {
    if (pr.polling || stopped) return
    pr.polling = true
    try {
      await syncSessions(followedFor(pr))
    } finally {
      pr.polling = false
    }
  }

  function syncPollTimers() {
    if (!loopsRunning || stopped) return
    for (const [pid, entry] of pollTimers) {
      const pr = profiles.get(pid)
      const wantMs = pr ? Math.max(20, Math.round(pr.profile.pollMs * pollScale)) : undefined
      if (!pr || entry.ms !== wantMs) {
        clearInterval(entry.timer)
        pollTimers.delete(pid)
      }
    }
    for (const [pid, pr] of profiles) {
      if (pollTimers.has(pid)) continue
      const ms = Math.max(20, Math.round(pr.profile.pollMs * pollScale))
      const timer = setInterval(() => {
        const cur = profiles.get(pid)
        if (cur) void pollProfile(cur).catch((err) => say(err.message))
      }, ms)
      timer.unref?.()
      pollTimers.set(pid, { ms, timer })
    }
  }

  async function poll() {
    if (stopped) return
    await syncSessions(followed())
  }

  // --- live text -------------------------------------------------------------------------------------------------

  function openStream(s) {
    const p = profiles.get(s.pid)?.profile
    if (!p?.stream || s.stream || stopped) return
    const ctl = new AbortController()
    s.stream = ctl
    let backoff = 1000
    const loop = async () => {
      while (s.watched && !ctl.signal.aborted && !stopped) {
        try {
          await s.client.stream(s.maId, (frame) => onFrame(s, frame), { signal: ctl.signal })
          backoff = 1000
        } catch (err) {
          if (err instanceof ApiError && (err.status === 404 || err.status === 405 || err.status === 400)) {
            warnOnce(`stream:${s.pid}`, `${s.pid}: no event stream (${err.status}); following by polling`)
            break
          }
          warnOnce(`streamerr:${s.key}`, `${s.key}: ${err.message}`)
          backoff = Math.min(backoff * 2, 30_000)
        }
        await new Promise((r) => setTimeout(r, backoff))
      }
      if (s.stream === ctl) s.stream = undefined
    }
    void loop()
  }

  function onFrame(s, frame) {
    if (frame?.type === 'event_start') return
    if (frame?.type === 'event_delta') {
      const id = frame.event_id ?? frame.id
      const text = (s.deltas.get(id) ?? '') + deltaText(frame)
      s.deltas.set(id, text)
      // Only the reply goes live: thinking deltas belong to their own item once it lands.
      if (frame.event_type !== 'agent.thinking' && frame.delta?.type !== 'thinking') rt.stream(s.key, text)
      return
    }
    if (typeof frame?.type === 'string' && typeof frame.id === 'string' && !s.ids.has(frame.id)) {
      s.active = Date.now()
      void sync(s).catch(() => {})
    }
  }

  // --- the driver ------------------------------------------------------------------------------------------------

  const driver = {
    async listSessions() {
      const rows = []
      for (const pr of profiles.values()) for (const meta of pr.rows.values()) rows.push(rowOf(pr, meta))
      return rt.recent(rows, { pinned: (r) => r.status === 'working' || r.status === 'blocked' })
    },
    async items(params) {
      const s = sessionByKey(params.session_id)
      if (!s.loaded) await sync(s)
      const res = page(s.items, params, { history: history(s), toSeq: s.events.length })
      return { ...res, items: res.items.map(light) }
    },
    async cursor(id) {
      const s = sessionByKey(id)
      if (!s.loaded) await sync(s)
      return { to_seq: s.events.length, history: history(s) }
    },
    async item(params) {
      const s = sessionByKey(params.session_id)
      if (!s.loaded) await sync(s)
      const it = s.items.find((i) => i.id === params.item_id)
      if (!it) throw new RpcError(`no item ${params.item_id}`, 'not_found')
      return it
    },
    async image(params) {
      const s = sessionByKey(params.session_id)
      const img = s.images.get(params.item_id)
      if (!img) throw new RpcError(`no image ${params.item_id}`, 'not_found')
      return img
    },
    async send(params) {
      const s = sessionByKey(params.session_id)
      const p = profiles.get(s.pid).profile
      if (s.loaded && logStatus(s.events) === 'terminated') throw new RpcError('this Managed Agents session has ended; start a new one', 'not_running')
      const content = []
      if (params.text) content.push({ type: 'text', text: params.text })
      for (const img of params.images ?? []) {
        if (p.images === 'none' || !img?.data) continue
        content.push({ type: 'image', source: { type: 'base64', media_type: img.mime ?? img.media_type ?? 'image/png', data: img.data } })
      }
      if (content.length === 0) throw new RpcError('nothing to send', 'bad_request')
      const busy = s.loaded && logStatus(s.events) === 'working'
      try {
        await s.client.sendEvents(s.maId, [{ type: 'user.message', content }])
      } catch (err) {
        throw new RpcError(err.message, err.status === 404 ? 'not_found' : 'send_failed', { delivered: err.status >= 400 && err.status < 500 ? 'none' : 'unknown' })
      }
      s.active = Date.now()
      void sync(s).catch(() => {})
      return busy ? { queued: true } : {}
    },
    async interrupt(params) {
      const s = sessionByKey(params.session_id)
      await s.client.sendEvents(s.maId, [{ type: 'user.interrupt' }])
      s.active = Date.now()
      const until = Date.now() + 10_000 * pollScale
      while (Date.now() < until && !stopped) {
        await sync(s).catch(() => {})
        const st = s.loaded ? logStatus(s.events) : 'unknown'
        if (st === 'done' || st === 'terminated') {
          rt.streamEnd(s.key)
          return { settled: true }
        }
        await new Promise((r) => setTimeout(r, 500 * pollScale))
      }
      return {}
    },
    async start(params) {
      const starters = [...profiles.values()].filter((x) => x.profile.start)
      // Only a daemon link may use its own daemons.json launcher name for the first profile.
      const pr = starters.find((x) => x.profile.start.launcher === params.launcher) ?? (o.runtimeOptions?.listen ? starters[0] : undefined)
      if (!pr) throw new RpcError(`no Managed Agents profile starts sessions as ${params.launcher ?? 'this launcher'}`, 'launcher_missing')
      const st = pr.profile.start
      const title = params.prompt ? params.prompt.split('\n')[0].slice(0, 60) : undefined
      const meta = await pr.client.createSession({ agent: st.agent, environmentId: st.environmentId, title, body: st.body })
      if (!meta) throw new RpcError('the server did not answer with a session', 'start_failed')
      started = [...started.filter((x) => !(x.profile === pr.profile.id && x.id === meta.id)), { profile: pr.profile.id, id: meta.id }].slice(-KEEP_STARTED)
      void saveState()
      pr.rows.set(meta.id, meta)
      const s = sessionFor(pr.profile.id, meta.id)
      s.active = Date.now()
      if (params.prompt || params.images?.length) {
        await driver.send({ session_id: s.key, text: params.prompt, images: params.images, client_msg_id: params.client_msg_id })
      }
      rt.sessionsChanged(0)
      return { session: rowOf(pr, meta) }
    },
    watchChanged(id, watching) {
      let s
      try {
        s = watching ? sessionByKey(id) : sessions.get(id)
      } catch {
        return
      }
      if (!s) return
      s.watched = watching
      if (watching) {
        s.active = Date.now()
        void sync(s).catch(() => {})
        openStream(s)
      } else {
        s.stream?.abort()
        s.stream = undefined
      }
    },
  }

  sdkOptions.launchers = launchers()
  sdkOptions.launcher = sdkOptions.launchers[0]?.name
  rt = makeRuntime()

  return {
    get runtime() { return rt },
    sessions,
    profiles,
    /** Reads the config and state, reconciles followed sessions, starts the loops, and connects (or listens, as a daemon). */
    async start() {
      stopped = false
      loopsRunning = false
      rtStarted = false
      await loadState()
      if (o.config) applyConfig(o.config)
      else await reloadConfig()
      await refreshList().catch((err) => say(err.message))
      await poll().catch((err) => say(err.message))
      if (stopped) return undefined
      loopsRunning = true
      if (!o.config) every(CONFIG_EVERY_MS * pollScale, reloadConfig)
      every(LIST_EVERY_MS * pollScale, refreshList)
      syncPollTimers()
      rtStarted = true
      return rt.start()
    },
    async stop() {
      stopped = true
      loopsRunning = false
      rtStarted = false
      for (const t of timers) clearInterval(t)
      timers.clear()
      for (const entry of pollTimers.values()) clearInterval(entry.timer)
      pollTimers.clear()
      for (const key of [...sessions.keys()]) dropSession(key)
      rt?.stop()
    },
    /** Lists again now (tests; a config change). */
    refresh: () => refreshList(),
    poll: () => poll(),
    reload: () => reloadConfig(),
    secrets,
  }
}
