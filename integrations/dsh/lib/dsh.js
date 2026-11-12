import { mcpToolNames } from '../../runtime-sdk/lib/compat.js'
// The DSH runtime (docs/integrations/dsh.md): DSH's sessions in Persona through the runtime SDK (docs/integrations/runtimes.md). Only what is
// DSH's own lives here — listing and paging through sessionController, mapping its session events (map.js), and
// putting its approval / question waterfalls in front of the phone; the SDK does the connection, watching, batching,
// live text, asking and the who-answers-first policy. Everything DSH-specific comes in through `deps`, so the tests
// drive it with fakes; index.js wires the real Cordis services.

import { randomUUID } from 'node:crypto'
import { basename, isAbsolute, relative, resolve } from 'node:path'
import { isAssistantDir } from '../../runtime-sdk/lib/identity.js'
import { createRuntime, interactionId, RpcError, summaryOf } from '../../runtime-sdk/index.js'
import { attachmentOf, dshSeq, eventItems, itemsOf, personaSeq, lastText, streamText, toolInput, toolName } from './map.js'

export const RUNTIME_VERSION = '0.5.0'

// The DSH permission preset for each Persona permission mode of an assistant conversation (config presets overrides):
// Codex's mapping, in DSH's presets (docs/integrations/dsh.md "Assistant"). DSH has no auto mode: as the default.
const DEFAULT_PRESETS = { default: 'workspace-write', acceptEdits: 'workspace-write', auto: 'workspace-write',
  plan: 'read-only', bypassPermissions: 'danger-full-access' }

/** A regex for a gated command's shell words (its command, its skill's script by path), as a command word. */
function shellWordsRe(words) {
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const alts = words.filter((w) => typeof w === 'string' && w !== '')
    .map((w) => (w.includes('/') ? `[^\\s;&|]*${esc(w)}` : esc(w)))
  return alts.length ? new RegExp(`(^|[\\s;&|(\`$])(${alts.join('|')})(\\s|$)`) : null
}

/** cwd is the Persona assistant's directory (~/.persona/assistant, as the node and the hub tell its conversations). */
function assistantDir(cwd) {
  return isAssistantDir(cwd)
}


/**
 * @typedef {object} DshDeps
 * @property {object} controller - DSH sessionController: list / page / projections / prompt / cancel / create / rename / attachment.
 * @property {(event: string, listener: Function, options?: object) => () => void} on - ctx.on.
 * @property {{ answer(agent: object, callId: string, answer: object): boolean } | undefined} [userQuestions]
 * @property {{ archivedSessionIds: readonly string[], archiveSession(id: string, o?: {stopActivity?: boolean}): Promise<void>, resolveByPath?(path: string): Promise<{id: string, path: string} | undefined> } | undefined} [workspace]
 *   DSH's workspaceRegistry: resolve existing workspaces for creation; omit archived sessions and archive on close.
 * @property {(definition: object, options?: object) => (() => void) | undefined} [registerTool] - ctx.tools.register:
 *   the hub's tools (phone_*, mail_*; the assistant's own for its conversations) for DSH's agents. Absent: none.
 * @property {{ get(id: string): { session: object } | undefined } | undefined} [agents] - ctx.agents: a live session,
 *   to set an assistant conversation's permission preset on.
 * @property {{ set(session: object, name: string): void } | undefined} [permissionPresets] - ctx.permissionPresets.
 * @property {{ list(caller?: string): object[] } | undefined} [jobs] - ctx.jobs: a session's background jobs (workflows,
 *   shells run in the background) that started before the plugin saw them.
 * @property {() => boolean} [hasLiveClient] - whether a DSH Web GUI is connected.
 * @property {(msg: string) => void} [log]
 * @property {(path: string) => import('node:net').Socket} [connect] - tests.
 */

const TOOLS_EVERY = 5 * 60_000 // ms: look at the hub's tools this often (the phone registers its tools when it connects)
const MAX_SUBAGENTS = 20 // a session's subagents listed: every running one, then the latest finished ones
const LEARN_EVERY = 5 * 60_000 // ms: read a parent's subagent catalog again at most this often (labels, tool calls)
const STARTED_SUBAGENT = /^started subagent (\S+)/ // DSH's subagent tool result: the child it started

/**
 * A subagent's id as Persona sees it, "<parent>@<child>" (docs/integrations/runtimes.md "Sub-Agents and background tasks"), split; undefined for another id.
 * @param {string} id
 */
export function splitChild(id) {
  const at = typeof id === 'string' ? id.indexOf('@') : -1
  return at > 0 && at < id.length - 1 ? { parent: id.slice(0, at), child: id.slice(at + 1) } : undefined
}

/**
 * @param {DshDeps} deps
 * @param {{ runtime?: string, name?: string, launcher?: string, socketPath?: string,
 *   interactions?: 'auto'|'phone'|'browser', listDays?: number, maxSessions?: number, pushDelayMs?: number,
 *   retryMs?: number, assistant?: boolean, presets?: Record<string, string>, listen?: { port: number, host?: string,
 *   path?: string, token: string | (() => string) }, package?: { name: string, hash: string },
 *   onUpdate?: Function }} [options] - runtime / name / launcher:
 *   how this DSH shows in Persona (dsh / DSH / dsh). assistant: it may run the Persona assistant (docs/integrations/dsh.md "Assistant");
 *   presets: the DSH permission preset for each Persona permission mode of an assistant conversation. listen: be a
 *   daemon the node dials (docs/integrations/daemons.md) instead of connecting to runtime.sock: DSH running as another user, in a
 *   container, on another machine (the node's daemons.json names it then, and a daemon does not run the assistant).
 *   package / onUpdate: from the loader (lib/plugin.js), passed on to createRuntime (docs/integrations/runtimes.md "Auto-update").
 */
export function createDshRuntime(deps, options = {}) {
  const runtimeID = options.runtime ?? 'dsh'
  const label = options.name ?? 'DSH'
  const listDays = options.listDays ?? 7
  const maxSessions = options.maxSessions ?? 80
  const log = deps.log ?? (() => {})
  const known = new Map() // session id -> { lastSeq (DSH), lastText, running }
  const calls = new Map() // session id -> Map(callId -> { name, args }): recent tool calls, for approval cards
  const texts = new Map() // session id -> the assistant text being generated
  // Subagents (DSH's child sessions: subagent tool, workflows) and background jobs (docs/integrations/dsh.md "Subagents and background tasks").
  const parents = new Map() // child session id -> its parent session id
  const children = new Map() // child session id -> { label, callId, createdAt }
  const learned = new Map() // parent session id -> when its catalog was last read
  const jobs = new Map() // owner session id -> Map(jobId -> { kind, label, startedAt, callId })
  const openCalls = new Map() // session id -> Set(callId): tool calls still waiting for their result
  const disposers = []
  const tools = { key: '', disposers: [], refreshing: false, timer: undefined }
  // The Persona assistant's conversations (their directory is the assistant's): id -> { memoryDir, mode }.
  const assistants = new Map()
  const presets = { ...DEFAULT_PRESETS, ...(options.presets ?? {}) }
  const isAssistant = (id) => assistants.has(id)

  const state = (id) => {
    let s = known.get(id)
    if (!s) {
      s = { lastSeq: -1, lastText: '', running: false }
      known.set(id, s)
    }
    return s
  }
  const history = (id) => `dsh:${id}`
  const withTimeout = async (ms, fn) => {
    const ac = new AbortController()
    const timer = setTimeout(() => ac.abort(), ms)
    try {
      return await fn(ac.signal)
    } finally {
      clearTimeout(timer)
    }
  }

  // ---- the driver ----

  async function listSessions() {
    const { items } = await withTimeout(20_000, (signal) => deps.controller.list({}, signal))
    const archived = archivedIDs()
    const rows = []
    const kids = new Map() // parent id -> its children's summaries
    for (const s of items ?? []) {
      const id = String(s.sessionId)
      if (s.origin === 'subagent' || s.parentSessionId) { // a subagent's or a workflow's: its parent shows it
        if (s.parentSessionId) {
          const parent = String(s.parentSessionId)
          parents.set(id, parent)
          if (!kids.has(parent)) kids.set(parent, [])
          kids.get(parent).push(s)
        }
        continue
      }
      if (archived.has(id)) continue // archived in DSH (or closed from the phone): DSH hides it too
      if (options.assistant && assistantDir(s.cwd) && !assistants.has(id)) assistants.set(id, {})
      const st = known.get(id)
      const running = s.running === true || st?.running === true
      const waiting = activeQuestions(s) > 0 // asked in the Web GUI; cards on the phone the SDK marks itself
      if (s.blank && !rt.touched(id)) continue // before its first prompt, only one the phone started
      rows.push({ s, id, running, waiting })
    }
    for (const r of rows) {
      r.subagents = subagentsOf(r.id, kids.get(r.id) ?? [])
      r.background = backgroundOf(r.id)
      r.busy = r.subagents.some((a) => a.running) || r.background.length > 0
    }
    // Running and waiting ones always, those with work still running and those the phone touched always, the rest by
    // age and count (the SDK's rule).
    const chosen = rt.recent(rows, { days: listDays, max: maxSessions, pinned: (r) => r.running || r.waiting || r.busy,
      id: (r) => r.id, updatedAt: (r) => r.s.updatedAt ?? 0 })
    return chosen.map(({ s, id, running, waiting, subagents, background }) => {
      const st = known.get(id)
      const hints = s.projections
      const title = typeof hints?.values?.title === 'string' ? hints.values.title : ''
      const seq = Math.max(st?.lastSeq ?? -1, typeof hints?.asOfSeq === 'number' ? hints.asOfSeq : -1)
      const row = {
        session_id: id,
        title: title || (s.cwd ? basename(s.cwd) : ''),
        cwd: s.cwd ?? '',
        status: waiting ? 'blocked' : running ? 'working' : 'done',
        updated_at: new Date(s.updatedAt ?? Date.now()).toISOString(),
      }
      if (runtimeID !== 'dsh') row.agent_label = label // the node names only a runtime's own agent itself
      if (s.cwd) {
        row.space_id = s.cwd
        row.space_name = basename(s.cwd)
      }
      if (seq >= 0) row.rev = personaSeq(seq)
      if (st?.lastText) row.last_text = st.lastText
      if (subagents.length > 0) row.subagents = subagents
      if (background.length > 0) row.background = background
      return row
    })
  }

  /**
   * A session's subagents as Persona lists them: every running one, then the latest finished, MAX_SUBAGENTS in all.
   * Their labels and the tool calls that started them come from the parent's events (live) or its catalog (read once).
   */
  function subagentsOf(parent, summaries) {
    if (summaries.length === 0) return []
    const rows = summaries.map((s) => {
      const id = String(s.sessionId)
      const info = children.get(id) ?? {}
      const values = s.projections?.values ?? {}
      const running = s.running === true || known.get(id)?.running === true
      const since = values.subagentTiming?.active?.since
      const a = { id, description: info.label || values.subagent?.label || '子 Agent', type: 'subagent', running }
      if (info.callId) a.tool_use_id = info.callId
      const started = info.createdAt ?? (typeof since === 'number' ? since : undefined)
      if (typeof started === 'number') a.started_at = new Date(started).toISOString()
      if (typeof s.updatedAt === 'number') a.updated_at = new Date(s.updatedAt).toISOString()
      const last = known.get(id)?.lastText
      if (last) a.last_text = last
      return { a, at: s.updatedAt ?? 0 }
    })
    rows.sort((x, y) => Number(y.a.running) - Number(x.a.running) || y.at - x.at)
    const out = rows.slice(0, MAX_SUBAGENTS).map((r) => r.a)
    if (out.some((a) => a.running && (!a.tool_use_id || a.description === '子 Agent'))) void learnChildren(parent)
    return out
  }

  /**
   * Reads a parent's subagent catalog (labels, start times) and the tool results that started its children (which
   * call each one answers, so the chat opens it from the call): for children the plugin did not see start.
   */
  async function learnChildren(parent) {
    const at = learned.get(parent) ?? 0
    if (Date.now() - at < LEARN_EVERY) return
    learned.set(parent, Date.now())
    try {
      await withTimeout(20_000, async (signal) => {
        const base = await deps.controller.projections({ sessionId: parent }, signal)
        for (const e of Array.isArray(base?.values?.subagentCatalog) ? base.values.subagentCatalog : []) {
          if (e?.childId) noteChild(parent, String(e.childId), { label: e.label, createdAt: e.childCreatedAt })
        }
        if (typeof base?.asOfSeq !== 'number') return
        const { events } = await pageEvents(parent, base.asOfSeq, undefined, 400, undefined, signal)
        for (const e of events) noteStarted(parent, e)
      })
      rt.sessionsChanged()
    } catch (err) {
      log(`persona: subagents of ${parent}: ${err.message}`)
    }
  }

  function noteChild(parent, child, { label, createdAt, callId } = {}) {
    parents.set(child, parent)
    const info = children.get(child) ?? {}
    if (typeof label === 'string' && label) info.label = label
    if (typeof createdAt === 'number') info.createdAt = createdAt
    if (typeof callId === 'string' && callId) info.callId = callId
    children.set(child, info)
    while (children.size > 2000) children.delete(children.keys().next().value)
  }

  /** A parent's tool result saying which child its subagent call started. */
  function noteStarted(parent, event) {
    if (event?.type !== 'tool/result') return false
    const m = event.data?.message
    const text = (m?.content ?? []).find((b) => b?.type === 'text')?.text
    const found = typeof text === 'string' ? STARTED_SUBAGENT.exec(text) : null
    if (!found || !m?.toolCallId) return false
    noteChild(parent, found[1], { callId: String(m.toolCallId) })
    return true
  }

  /**
   * A session's background tasks: its DSH jobs still running (workflows, shells run in the background) — not one whose
   * tool call is still waiting for its result (that call is in the foreground). From the job events the plugin saw,
   * and DSH's job registry for those that started before it.
   */
  function backgroundOf(id) {
    const open = openCalls.get(id) ?? new Set()
    const tracked = jobs.get(id) ?? new Map()
    const out = new Map()
    for (const [jobId, j] of tracked) {
      if (j.callId && open.has(j.callId)) continue
      out.set(jobId, task(jobId, j))
    }
    try {
      for (const j of typeof deps.jobs?.list === 'function' ? deps.jobs.list(id) ?? [] : []) {
        if (String(j.owner ?? '') !== id || !['running', 'stopping'].includes(j.status) || out.has(String(j.id))) continue
        const t = tracked.get(String(j.id))
        if (t?.callId && open.has(t.callId)) continue
        out.set(String(j.id), task(String(j.id), { kind: j.kind, label: j.label, startedAt: j.startedAt }))
      }
    } catch (err) {
      log(`persona: jobs of ${id}: ${err.message}`)
    }
    return [...out.values()]
  }

  function task(id, j) {
    const t = { id, kind: j.kind === 'bash' ? 'shell' : 'agent', description: String(j.label ?? j.kind ?? '') }
    if (j.kind === 'bash' && j.label) t.command = String(j.label)
    if (typeof j.startedAt === 'number') t.started_at = new Date(j.startedAt).toISOString()
    return t
  }

  function activeQuestions(summary) {
    const q = summary?.projections?.values?.userQuestions
    return Array.isArray(q?.active) ? q.active.length : 0
  }

  /** DSH's archived sessions (none when it has no workspace registry, or it is not loaded yet). */
  function archivedIDs() {
    try {
      const ids = deps.workspace?.archivedSessionIds
      return new Set(Array.isArray(ids) ? ids.map(String) : [])
    } catch {
      return new Set() // the registry's state is not loaded yet: nothing is known to be archived
    }
  }

  /** Closing from the phone archives the session, as DSH's own close does, stopping what still runs in it. */
  async function close(params) {
    await deps.workspace.archiveSession(params.session_id, { stopActivity: true })
    known.delete(params.session_id)
  }

  // ---- the hub's tools for DSH's agents (phone_*, mail_*: docs/integrations/dsh.md "Phone tools") ----

  /** The DSH tool for one of the hub's tools: called through the node (mcp.call), its text as the result. */
  function toolDefinition(t, assistantOnly, name) {
    const schema = t.input_schema && typeof t.input_schema === 'object' && !Array.isArray(t.input_schema)
      ? t.input_schema : { type: 'object', properties: {} }
    return {
      name,
      description: typeof t.description === 'string' ? t.description : t.name,
      parameters: schema,
      output: {
        schema: { type: 'object', properties: { content: { type: 'array', items: {} } }, required: ['content'], additionalProperties: false },
        render: (_args, value) => [{ type: 'text', text: resultText(value?.content) }],
      },
      async execute(args, exec) {
        const agent = exec?.agent
        const sessionId = String(agent?.session?.id ?? agent?.id ?? '')
        const call = { name: t.name, args: args && typeof args === 'object' ? args : {}, session_id: sessionId }
        if (isAssistant(sessionId)) call.assistant = true
        else if (assistantOnly) throw new Error(`${t.name} is the Persona assistant's`)
        const res = await rt.callTool(call, { signal: exec?.signal })
        const text = resultText(res?.content)
        if (res?.is_error) throw new Error(text || `${t.name} failed`)
        return { content: [{ type: 'text', text }] }
      },
    }
  }

  /** Registers the hub's tools when they changed; the last ones stay while the hub is away (a call says so). */
  async function refreshTools(connected) {
    if (typeof deps.registerTool !== 'function' || !connected || tools.refreshing) return
    tools.refreshing = true
    try {
      const list = await rt.tools()
      // The assistant's own tools (ask_user, schedule_followup…) only for its conversations.
      const plain = new Set(list.map((t) => t.name))
      const all = options.assistant ? await rt.tools({ assistant: true }) : list
      // A gated command's tool: its words in an assistant conversation's shell are refused (onPreExecute).
      tools.shell = all.filter((t) => Array.isArray(t.shell)).map((t) => ({ name: t.name, command: t.shell[0], re: shellWordsRe(t.shell) }))
        .filter((g) => g.re)
      const key = JSON.stringify(all.map((t) => [t.name, t.description, t.input_schema, plain.has(t.name)]))
      if (key === tools.key) return
      dropTools()
      tools.key = key
      for (const t of all) {
        const assistantOnly = !plain.has(t.name)
        try {
          const when = assistantOnly ? (c) => isAssistant(String(c?.agent?.session?.id ?? c?.agent?.id ?? '')) : undefined
          for (const name of mcpToolNames(t.name)) {
            const dispose = deps.registerTool(toolDefinition(t, assistantOnly, name), when ? { when } : undefined)
            if (typeof dispose === 'function') tools.disposers.push(dispose)
          }
        } catch (err) {
          log(`persona: tool ${t.name}: ${err.message}`)
        }
      }
      log(`persona: ${tools.disposers.length} Persona tools for DSH's agents`)
    } catch (err) {
      log(`persona: the hub's tools: ${err.message}`)
    } finally {
      tools.refreshing = false
    }
  }

  function dropTools() {
    for (const d of tools.disposers.splice(0)) {
      try {
        d()
      } catch {
        // already disposed with the plugin's scope
      }
    }
    tools.key = ''
  }

  /** DSH's own session id for a Persona id (a subagent's "<parent>@<child>" is the child's). */
  function dshID(id) {
    return splitChild(id)?.child ?? id
  }

  /** The address DSH pages a session by: a subagent's through its parent (DSH refuses a child's plain address). */
  function addressOf(id) {
    const sub = splitChild(id)
    return sub ? { kind: 'subagent', parentSessionId: sub.parent, childSessionId: sub.child, mode: 'unknown' } : { kind: 'session', sessionId: id }
  }

  async function cursorOf(sessionId, signal) {
    const base = await deps.controller.projections({ sessionId: dshID(sessionId) }, signal)
    if (base === null || base === undefined) throw new RpcError(`DSH 里没有会话 ${sessionId}`, 'not_found')
    return base.asOfSeq
  }

  /** Events of a backwards page (oldest first), with has_more. */
  async function pageEvents(sessionId, throughSeq, beforeSeq, maxMessages, turnWindow, signal) {
    if (throughSeq < 0) return { events: [], hasMore: false }
    const req = { address: addressOf(sessionId), throughSeq, maxMessages }
    if (beforeSeq !== undefined) req.beforeSeq = beforeSeq
    if (turnWindow) req.turnWindow = turnWindow
    const page = await deps.controller.page(req, signal)
    const events = (page.records ?? []).filter((r) => r?.type === 'event').map((r) => r.event)
    return { events, hasMore: page.hasMore === true }
  }

  /** Events with DSH seq > afterSeq up to throughSeq, oldest first; reached false when they go back too far. */
  async function eventsAfter(sessionId, afterSeq, throughSeq, signal) {
    let events = []
    let before
    for (let i = 0; i < 10; i++) {
      const page = await pageEvents(sessionId, throughSeq, before, 200, undefined, signal)
      events = page.events.concat(events)
      if (!page.hasMore || page.events.length === 0 || page.events[0].seq <= afterSeq + 1) {
        return { events: events.filter((e) => e.seq > afterSeq), reached: true }
      }
      before = page.events[0].seq
    }
    return { events: events.filter((e) => e.seq > afterSeq), reached: false }
  }

  function items(params) {
    const sessionId = params.session_id
    return withTimeout(25_000, async (signal) => {
      const cursor = await cursorOf(sessionId, signal)
      const top = personaSeq(cursor)
      if (typeof params.through_seq === 'number' && params.through_seq > 0) {
        // A fold being opened: the events of [from_seq, through_seq].
        const from = dshSeq(params.from_seq ?? 1)
        const through = Math.min(dshSeq(params.through_seq), cursor)
        const { events } = await eventsAfter(sessionId, from - 1, through, signal)
        return { items: itemsOf(events.filter((e) => e.seq <= through)), more: false }
      }
      if (params.after_seq !== undefined && params.after_seq !== null) {
        const after = dshSeq(params.after_seq)
        if (after >= cursor) return { items: [], has_more: false, more_after: false, history: history(sessionId), from_seq: params.after_seq, to_seq: Math.max(top, params.after_seq) }
        const { events, reached } = await eventsAfter(sessionId, after, cursor, signal)
        const out = []
        let to = params.after_seq
        let cut = false
        for (const e of events) {
          const its = eventItems(e)
          if (out.length > 0 && out.length + its.length > 500) {
            cut = true
            break
          }
          out.push(...its)
          to = personaSeq(e.seq)
        }
        if (!cut) to = top
        const from = reached ? params.after_seq : personaSeq(events[0]?.seq ?? cursor) - 1
        return { items: out, has_more: false, more_after: cut, history: history(sessionId), from_seq: from, to_seq: to }
      }
      const before = params.before_seq > 0 ? dshSeq(params.before_seq) : undefined
      const turns = params.turns > 0 ? Math.min(params.turns, 10) : 0
      const limit = params.limit > 0 ? Math.min(params.limit, 500) : 80
      const window = turns > 0 ? { minMessages: 1, minTurns: turns } : undefined
      const { events, hasMore } = await pageEvents(sessionId, cursor, before, turns > 0 ? 400 : limit, window, signal)
      const last = events.at(-1)
      return { items: itemsOf(events), has_more: hasMore, history: history(sessionId), to_seq: before === undefined ? top : last ? personaSeq(last.seq) : 0 }
    })
  }

  async function eventAt(sessionId, seq, signal) {
    const page = await pageEvents(sessionId, await cursorOf(sessionId, signal), seq + 1, 1, undefined, signal)
    return page.events.find((e) => e.seq === seq)
  }

  function splitItemID(itemID) {
    const [seqText, idxText] = String(itemID).split('#')
    const seq = Number(seqText)
    const idx = Number(idxText)
    if (!Number.isSafeInteger(seq) || !Number.isSafeInteger(idx) || seq < 1) throw new RpcError(`bad item id ${itemID}`, 'bad_request')
    return { seq: dshSeq(seq), idx }
  }

  function image(params) {
    const { seq, idx } = splitItemID(params.item_id)
    return withTimeout(25_000, async (signal) => {
      const event = await eventAt(params.session_id, seq, signal)
      // A tool result's images sit after its text item: block i is item i + 1.
      const attachmentId = attachmentOf(event, event?.type === 'tool/result' ? idx - 1 : idx)
      if (!attachmentId) throw new RpcError(`no image ${params.item_id}`, 'not_found')
      const res = await deps.controller.attachment({ sessionId: dshID(params.session_id), attachmentId })
      return { mime: res.attachment?.mediaType ?? 'image/png', data: res.data }
    })
  }

  function item(params) {
    const { seq } = splitItemID(params.item_id)
    return withTimeout(25_000, async (signal) => {
      const event = await eventAt(params.session_id, seq, signal)
      const it = eventItems(event, { full: true }).find((x) => x.id === params.item_id)
      if (!it) throw new RpcError(`no item ${params.item_id}`, 'not_found')
      return it
    })
  }

  async function send(params) {
    if (splitChild(params.session_id)) throw new RpcError('unsupported: 子 Agent 由它的父会话管理，请在父会话里说', 'unsupported')
    const content = []
    const assistant = options.assistant === true && params.assistant === true // the node says so only for its assistant conversations
    if (assistant) {
      const a = assistants.get(params.session_id) ?? {}
      if (typeof params.memory_dir === 'string' && params.memory_dir !== '') a.memoryDir = params.memory_dir
      if (typeof params.mode === 'string' && params.mode !== '') a.mode = params.mode
      assistants.set(params.session_id, a)
      // The live snapshot goes in front of what was typed, as for Claude and Codex (the chat does not show it: map.js).
      if (typeof params.context === 'string' && params.context.trim() !== '') content.push({ type: 'text', text: params.context })
    }
    if (typeof params.text === 'string' && params.text.trim() !== '') content.push({ type: 'text', text: params.text })
    for (const img of params.images ?? []) {
      if (img?.data) content.push({ type: 'image', mediaType: img.mime || 'image/jpeg', data: img.data, name: 'phone' })
    }
    if (content.length === 0) throw new RpcError('消息是空的', 'bad_request')
    // Queued while idle (a new turn), steered while working (into this turn, like Claude's queued input).
    const running = known.get(params.session_id)?.running === true
    await deps.controller.prompt({
      requestId: params.client_msg_id || randomUUID(), // DSH dedupes on it: a resend is not a second prompt
      sessionId: params.session_id,
      mode: running ? 'steer' : 'queue',
      content,
    }, AbortSignal.timeout(25_000))
    if (assistant) applyPreset(params.session_id)
  }

  /** The DSH permission preset for an assistant conversation's Persona mode, set once its agent is live. */
  function applyPreset(id) {
    const a = assistants.get(id)
    const name = presets[a?.mode || 'default'] ?? presets.default
    if (!name || a?.preset === name || !deps.permissionPresets || !deps.agents) return
    const agent = deps.agents.get(id)
    if (!agent?.session) return
    try {
      deps.permissionPresets.set(agent.session, name)
      a.preset = name
    } catch (err) {
      log(`persona: preset ${name} for ${id}: ${err.message}`)
    }
  }

  async function start(params) {
    if (!params.cwd) throw new RpcError('要选一个目录', 'bad_request')
    let workspace
    try {
      workspace = await deps.workspace?.resolveByPath?.(params.cwd)
    } catch (err) {
      if (err?.code !== 'ENOENT') throw err // DSH's cwd-only creation also creates missing directories.
    }
    const res = await deps.controller.create(workspace ? { workspaceId: workspace.id } : { cwd: params.cwd })
    const id = String(res.sessionId)
    if (options.assistant && params.assistant === true) assistants.set(id, {})
    if (typeof params.prompt === 'string' && params.prompt.trim() !== '') await send({ session_id: id, text: params.prompt })
    const session = { session_id: id, cwd: params.cwd, title: basename(params.cwd), status: 'idle',
      space_id: params.cwd, space_name: basename(params.cwd), updated_at: new Date().toISOString() }
    if (runtimeID !== 'dsh') session.agent_label = label
    return { session }
  }

  const rt = createRuntime({
    listSessions,
    items,
    cursor: (sessionId) => withTimeout(25_000, async (signal) => personaSeq(await cursorOf(sessionId, signal))),
    image,
    item,
    send,
    interrupt: (params) => deps.controller.cancel({ sessionId: dshID(params.session_id) }),
    start,
    rename: (params) => {
      if (splitChild(params.session_id)) throw new RpcError('unsupported: 子 Agent 不能改名', 'unsupported')
      return deps.controller.rename({ sessionId: params.session_id, title: params.title })
    },
    close: typeof deps.workspace?.archiveSession === 'function' ? close : undefined,
    hasLocalClient: typeof deps.hasLiveClient === 'function' ? deps.hasLiveClient : undefined,
  }, {
    runtime: runtimeID,
    name: options.name ?? 'DSH',
    agent: runtimeID === 'dsh' ? undefined : 'dsh', // a renamed DSH still runs DSH's agent (its cards are DSH's)
    version: RUNTIME_VERSION,
    keepAlways: true, // DSH answers allowed-once only: the hub keeps "Always allow in this session" (#3974)
    launcher: options.launcher ?? 'dsh',
    launcherModel: 'DeepSeek Harness',
    socketPath: options.socketPath,
    answerFirst: options.interactions === 'browser' ? 'local' : options.interactions ?? 'auto',
    log,
    connect: deps.connect,
    pushDelayMs: options.pushDelayMs,
    retryMs: options.retryMs,
    onHub: (connected) => void refreshTools(connected),
    assistant: options.assistant === true,
    listen: options.listen,
    package: options.package,
    onUpdate: options.onUpdate,
  })

  // ---- DSH's events ----

  /** A DSH session's parent, when it is a subagent (known from the list, the parent's events, or its header). */
  function parentOf(id, session) {
    const header = session?.header
    if (header?.parentSession && (header.origin === 'subagent' || !parents.has(id))) parents.set(id, String(header.parentSession))
    return parents.get(id)
  }

  /** The id Persona knows a DSH session by: a subagent's is "<parent>@<child>". */
  function personaID(id, session) {
    const parent = parentOf(id, session)
    return parent ? `${parent}@${id}` : id
  }

  function onSessionEvent(session, event) {
    const id = String(session?.id ?? '')
    if (!id || typeof event?.seq !== 'number') return
    const st = state(id)
    if (event.seq > st.lastSeq) st.lastSeq = event.seq
    const d = event.data ?? {}
    switch (event.type) {
      case 'assistant/message': {
        const t = lastText([event])
        if (t) st.lastText = t
        rememberCalls(id, event)
        break
      }
      case 'turn/start':
        st.running = true
        rt.sessionsChanged()
        break
      case 'turn/end':
        st.running = false
        rt.sessionsChanged()
        break
      case 'tool/call':
        if (d.callId) callsOpen(id).add(String(d.callId))
        break
      case 'tool/result':
        if (d.message?.toolCallId) callsOpen(id).delete(String(d.message.toolCallId))
        if (noteStarted(id, event)) rt.sessionsChanged()
        if (jobs.get(id)?.size) rt.sessionsChanged() // a background job's call answered: it shows now
        break
      case 'subagent/catalog':
        if (d.childId) noteChild(id, String(d.childId), { label: d.label, createdAt: d.childCreatedAt })
        rt.sessionsChanged()
        break
      case 'tool-workflow/agent-start': // a workflow's agent: its child session, named by the workflow's label
        if (d.childId) noteChild(id, String(d.childId), { label: d.label, createdAt: event.time })
        rt.sessionsChanged()
        break
      case 'tool-workflow/agent-end':
      case 'tool-workflow/run-end':
        rt.sessionsChanged()
        break
      case 'job/registered':
      case 'job/settled': {
        const owner = String(d.owner ?? id)
        let m = jobs.get(owner)
        if (!m) jobs.set(owner, (m = new Map()))
        if (event.type === 'job/registered' && d.jobId) {
          m.set(String(d.jobId), { kind: d.kind, label: d.label, startedAt: d.startedAt, callId: d.toolCall?.callId })
          while (m.size > 200) m.delete(m.keys().next().value)
        } else if (d.jobId) m.delete(String(d.jobId))
        rt.sessionsChanged()
        break
      }
    }
    // Every event moves the seq, even one the chat does not show. A subagent's go to "<parent>@<child>".
    const jid = personaID(id, session)
    rt.append(jid, { items: rt.watching(jid) ? eventItems(event) : [], fromSeq: event.seq, toSeq: personaSeq(event.seq), history: history(jid) })
  }

  function callsOpen(id) {
    let s = openCalls.get(id)
    if (!s) openCalls.set(id, (s = new Set()))
    while (s.size > 200) s.delete(s.values().next().value)
    return s
  }

  function rememberCalls(id, event) {
    let m = calls.get(id)
    if (!m) {
      m = new Map()
      calls.set(id, m)
    }
    for (const b of event.data?.message?.content ?? []) {
      if (b?.type === 'tool-call' && b.id) m.set(b.id, { name: b.name, args: b.arguments })
    }
    while (m.size > 64) m.delete(m.keys().next().value)
  }

  function onAssistantStream({ agent, frame }) {
    const own = String(agent?.session?.id ?? agent?.id ?? '')
    if (!own) return
    const id = personaID(own, agent?.session)
    if (frame?.type === 'start') {
      texts.set(id, '')
      return
    }
    if (frame?.type === 'end') {
      texts.delete(id)
      rt.streamEnd(id)
      return
    }
    const add = streamText(frame)
    if (!add) return
    // Kept whether or not the phone watches: session.delta is the whole text so far, so a watch that starts
    // mid-message needs what came before it.
    const text = (texts.get(id) ?? '') + add
    texts.set(id, text)
    if (rt.watching(id)) rt.stream(id, text)
  }

  /** A write or edit of a file in an assistant conversation's memory directory, outside plan mode. */
  function memoryWrite(sessionId, name, input) {
    const a = assistants.get(sessionId)
    const file = typeof input?.file_path === 'string' ? input.file_path : ''
    if (!a?.memoryDir || a.mode === 'plan' || !file || !['write', 'edit'].includes(name)) return false
    const rel = relative(a.memoryDir, resolve(a.memoryDir, file))
    return rel !== '' && !rel.startsWith('..') && !isAbsolute(rel)
  }

  /**
   * In an assistant conversation a gated command goes through its tool (the node runs the gate, confirms writes on the
   * phone and holds the credentials): the command run in the shell is stopped with that said.
   */
  function onPreExecute(exec, next) {
    const id = String(exec?.agent?.session?.id ?? exec?.agent?.id ?? '')
    const cmd = exec?.name === 'bash' && typeof exec?.arguments?.command === 'string' ? exec.arguments.command : ''
    const gate = id && cmd && isAssistant(id) ? (tools.shell ?? []).find((g) => g.re.test(cmd)) : undefined
    if (gate) {
      return { kind: 'deny', reason: `这里用 ${gate.name} 工具：参数就是 ${gate.command} 后面那些词；shell 里的 ${gate.command} 拿不到凭据。` }
    }
    return next()
  }

  /**
   * Where an agent's card goes: its own session, or for a subagent its parent's (the phone lists the parent; the card
   * says which subagent asks).
   */
  function askFor(agent) {
    const sessionId = String(agent?.session?.id ?? agent?.id ?? '')
    const parent = sessionId ? parentOf(sessionId, agent?.session) : undefined
    const label = parent ? children.get(sessionId)?.label || '子 Agent' : ''
    return { sessionId, on: parent ?? sessionId, child: parent ? label : '' }
  }

  async function onApproval(req, next) {
    const agent = req?.agent
    const { sessionId, on, child } = askFor(agent)
    if (!sessionId || !(isAssistant(on) ? rt.hubConnected : rt.phoneFirst(on))) return next()
    const call = req.callId ? calls.get(sessionId)?.get(req.callId) : undefined
    const name = call?.name ?? req.toolName
    const input = call ? toolInput(call.name, call.args) : undefined
    if (memoryWrite(sessionId, name, input)) return 'allowed-once' // the assistant's own memory: not asked, as for Codex
    const ix = {
      id: interactionId(runtimeID, sessionId, 'permission', req.callId ?? randomUUID()),
      kind: 'permission',
      session_id: on,
      tool_name: toolName(name),
      tool_use_id: req.callId,
      summary: summaryOf(name, input), // what it will run; DSH's own words on why go in reason, as on a Codex card
    }
    const why = typeof req.reason === 'string' ? req.reason.trim() : ''
    if (child) ix.reason = why ? `子 Agent「${child}」：${why}` : `子 Agent「${child}」`
    else if (why) ix.reason = req.reason
    if (input) ix.tool_input = input
    // "Always allow in this session" (#3974): DSH keeps no session rule, so the hub does — the node names the grant (this session, this
    // tool) and answers the next card for it itself. DSH still hears allowed-once each time. A subagent's card is on
    // its parent's session: the same tool there is the same grant for the main agent and its subagents, which the
    // label says on both.
    ix.always_label = `本会话（主 Agent 和子 Agent）不再问 ${ix.tool_name}`
    let res
    try {
      res = await rt.ask(ix, { signal: req.signal })
    } catch (err) {
      if (req.signal?.aborted) return 'cancelled'
      throw err
    }
    if (res?.dismiss || typeof res?.allow !== 'boolean') return next() // handed back: the Web GUI asks
    if (!res.allow && !child) steerReason(sessionId, ix.id, ix.tool_name, res.message) // a subagent takes no steering
    return res.allow ? 'allowed-once' : 'rejected'
  }

  /**
   * A refusal with words: DSH's approval carries no reason, so the words go into the running turn right after the
   * rejection (steer), as Persona does for Codex — the agent reads why, the way Claude reads a hook's deny message.
   */
  function steerReason(sessionId, ixId, tool, message) {
    const text = typeof message === 'string' ? message.trim() : ''
    if (!text) return
    setTimeout(() => {
      deps.controller.prompt({
        requestId: `${ixId}-reason`, // once, however often the card is answered
        sessionId,
        mode: 'steer',
        content: [{ type: 'text', text: `（在手机上拒绝了 ${tool || '这个工具调用'}）${text}` }],
      }, AbortSignal.timeout(25_000)).catch((err) => log(`persona: refusal reason for ${sessionId}: ${err.message}`))
    }, 0)
  }

  async function onQuestion(request, next) {
    const agent = request?.agent
    const { sessionId, on, child } = askFor(agent)
    const questions = request?.questions ?? []
    if (!sessionId || questions.length === 0 || !(isAssistant(on) ? rt.hubConnected : rt.phoneFirst(on))) return next()
    const callId = request.wait?.callId
    const plan = questions.length === 1 && questions[0].intent?.kind === 'plan-review' ? questions[0] : undefined
    const ix = {
      id: interactionId(runtimeID, sessionId, plan ? 'plan' : 'question', callId ?? questions.map((q) => q.id).join(',')),
      kind: plan ? 'plan' : 'question',
      session_id: on,
    }
    if (callId) ix.tool_use_id = callId
    if (plan) {
      ix.plan = plan.detail ?? ''
      ix.tool_name = 'ExitPlanMode'
    } else {
      ix.tool_name = 'AskUserQuestion'
      ix.questions = questions.map((q) => ({
        question: q.question,
        header: child ? `子 Agent「${child}」${q.header ? ` · ${q.header}` : ''}` : q.header ?? '',
        multi_select: q.multiSelect === true,
        options: (q.options ?? []).map((o) => ({ label: o.label, description: o.description ?? '' })),
      }))
    }
    // The phone keeps the card past DSH's timed wait: a late answer goes in through userQuestions.answer.
    const phoneAnswer = new AbortController()
    const asked = rt.ask(ix, { signal: phoneAnswer.signal })
    const aborted = new Promise((resolve) => {
      if (!request.signal) return
      if (request.signal.aborted) resolve()
      else request.signal.addEventListener('abort', () => resolve(), { once: true })
    })
    const first = await Promise.race([asked.then((res) => ({ res }), (err) => ({ err })), aborted.then(() => ({ aborted: true }))])
    if (first.err) throw first.err
    if (!first.aborted) {
      if (first.res?.dismiss) return next()
      return { answers: answerOf(questions, plan, first.res) }
    }
    const late = request.wait?.timed === true && callId && deps.userQuestions
    if (!late) {
      phoneAnswer.abort() // takes the card off the phone
      asked.catch(() => {})
      throw request.signal.reason ?? new Error('aborted')
    }
    asked.then((res) => {
      if (res?.dismiss) return
      try {
        deps.userQuestions.answer(agent, callId, { answers: answerOf(questions, plan, res) })
      } catch (err) {
        log(`persona: late answer for ${callId}: ${err.message}`)
      }
    }, () => {})
    throw request.signal.reason ?? new Error('aborted')
  }

  return {
    start() {
      const listen = (event, fn, opts = { global: true }) => {
        const dispose = deps.on(event, fn, opts)
        if (typeof dispose === 'function') disposers.push(dispose)
      }
      listen('session/event', onSessionEvent)
      listen('agent/assistant-stream', onAssistantStream)
      listen('api-session/added', () => rt.sessionsChanged())
      listen('api-session/removed', () => rt.sessionsChanged())
      listen('api-session/activity', () => rt.sessionsChanged())
      listen('api-session/status', (sessionId, running) => {
        state(String(sessionId)).running = running === true
        rt.sessionsChanged()
      })
      // First on DSH's answerer chains (the Web GUI registers when a browser connects): see docs/integrations/dsh.md.
      listen('approval/request', onApproval, { global: true, prepend: true })
      listen('user-questions/request', onQuestion, { global: true, prepend: true })
      if (options.assistant) listen('tools/pre-execute', onPreExecute, { global: true, prepend: true })
      rt.start().then((addr) => {
        if (addr) log(`persona: daemon endpoint on ${addr.address}:${addr.port}`)
      }, (err) => log(`persona: daemon endpoint: ${err.message}`)) // a port in use must not take DSH down
      if (typeof deps.registerTool === 'function') {
        tools.timer = setInterval(() => void refreshTools(rt.hubConnected), TOOLS_EVERY)
        tools.timer.unref?.()
      }
    },
    stop() {
      for (const d of disposers.splice(0)) {
        try {
          d()
        } catch {
          // already gone
        }
      }
      clearInterval(tools.timer)
      dropTools()
      rt.stop()
    },
    get connected() {
      return rt.connected
    },
    /** A card of this runtime is out on the phone (the loader switches versions only when none is). */
    busy: () => rt.busy(),
    /** The node's package (the loader's update). */
    fetchPackage: (name) => rt.fetchPackage(name),
    /** Look at the hub's tools now (tests). */
    refreshTools: () => refreshTools(rt.hubConnected),
    pushNow: rt.pushNow,
  }
}

/** The answer batch DSH wants, from the phone's resolution. */
export function answerOf(questions, plan, res) {
  if (plan) {
    const approve = plan.intent.approve
    if (res?.approve === true) return [{ id: plan.id, selected: [approve] }]
    const other = (plan.options ?? []).map((o) => o.label).find((l) => l !== approve)
    const item = { id: plan.id, selected: other ? [other] : [] }
    const feedback = typeof res?.feedback === 'string' ? res.feedback.trim() : ''
    if (feedback) item.custom = feedback
    return [item]
  }
  const answers = res?.answers ?? {}
  return questions.map((q) => {
    const value = typeof answers[q.question] === 'string' ? answers[q.question] : ''
    const labels = new Set((q.options ?? []).map((o) => o.label))
    const parts = q.multiSelect ? value.split(', ') : [value]
    const selected = parts.filter((p) => labels.has(p))
    const item = { id: q.id, selected }
    if (value && selected.length === 0) item.custom = value
    return item
  })
}

/** The text of a hub tool result's content (images are not passed on to DSH's agents: said in a line). */
export function resultText(content) {
  const parts = []
  for (const c of Array.isArray(content) ? content : []) {
    if (c?.type === 'text' && typeof c.text === 'string') parts.push(c.text)
    else if (c?.type === 'image') parts.push(`[图片 ${c.mime_type ?? ''}：DSH 里不显示]`.replace(' ：', '：'))
  }
  return parts.join('\n')
}
