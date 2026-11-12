// End-to-end harness for a runtime (docs/integrations/runtimes.md "Acceptance"): starts a real `personad node --stdio` in an isolated HOME
// and plays the hub on its stdio, so a check can drive the runtime the way the phone does and watch what reaches the
// hub. The runtime under test connects to `runtimeSocket`. Nothing outside the temp directory is touched.

import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { Peer } from '../lib/wire.js'
import { until } from './fake-node.js'

/**
 * @param {{ personad: string, keep?: boolean, home?: string, env?: Record<string, string>, signal?: AbortSignal }} opts - home: an existing
 *   HOME to use (a resident node's, startResident): `personad node --stdio` then relays to the resident node, as the
 *   hub's ssh does. Left in place. env: more of the node's environment (HERDR_SOCKET_PATH for a herdr of the run's own).
 */
export async function startNode({ personad, keep = false, home: given, env = {}, signal }) {
  const root = given ? undefined : mkdtempSync(join('/tmp', 'jrt-e2e-'))
  const home = given ?? join(root, 'home')
  let child
  try {
    mkdirSync(home, { recursive: true })
    child = spawn(personad, ['node', '--stdio'], { env: { PATH: '/usr/bin:/bin', ...env, HOME: home }, stdio: ['pipe', 'pipe', 'pipe'] })
  } catch (error) {
    if (!keep && root) rmSync(root, { recursive: true, force: true })
    throw error
  }
  const log = []
  child.stderr.on('data', (d) => log.push(String(d)))
  const stdio = {
    setEncoding: (e) => child.stdout.setEncoding(e),
    on: (ev, fn) => (ev === 'data' ? child.stdout.on('data', fn) : ev === 'close' ? child.on('exit', () => fn()) : child.on(ev, fn)),
    write: (s) => child.stdin.write(s),
    destroy: () => child.kill(),
  }
  const h = { root, home, runtimeSocket: join(home, '.persona', 'runtime.sock'), events: [], asked: [], log }
  h.resolve = () => ({ dismiss: true }) // how the "phone" answers interaction cards
  h.tools = [] // the hub's tools (mcp.tools), and how a call of one is answered (mcp.call)
  h.assistantTools = [] // listed besides h.tools when the node asks for the assistant's
  h.assistantContext = () => '' // the live snapshot the hub gives an assistant conversation's next message
  h.lark = {} // the assistant's Feishu gate settings: rules, lark-cli, command gates (assistant.lark)
  h.toolCalls = []
  h.callTool = (params) => ({ content: [{ type: 'text', text: `${params.name} ran` }], is_error: false })
  h.hub = new Peer(stdio, {
    prefix: 'h',
    onEvent: (event, params) => h.events.push({ event, params, at: Date.now() }),
    onRequest: async (method, params) => {
      switch (method) {
        case 'state.load':
          return { sessions: {} }
        case 'interaction.create':
          h.asked.push(params.interaction)
          return h.resolve(params.interaction)
        case 'mcp.tools':
          return { tools: params?.assistant ? [...h.tools, ...h.assistantTools] : h.tools }
        case 'assistant.context':
          return { text: h.assistantContext(params?.session_id) }
        case 'assistant.lark':
          return h.lark
        case 'mcp.call':
          h.toolCalls.push(params)
          return h.callTool(params)
        default:
          return {}
      }
    },
  })
  /** Call the node as the hub does. */
  h.call = (method, params = {}, timeoutMs = 30_000) => h.hub.call(method, params, { timeoutMs })
  /** Items pushed for a session so far, in order. */
  h.items = (sid) => h.events.filter((e) => e.event === 'session.items' && e.params.session_id === sid).flatMap((e) => e.params.items)
  /** The pushed batches of a session. */
  h.batches = (sid) => h.events.filter((e) => e.event === 'session.items' && e.params.session_id === sid).map((e) => e.params)
  /** Batches that do not start where the one before ended (should be none). */
  h.gaps = (sid) => {
    const b = h.batches(sid)
    return b.slice(1).filter((x, i) => x.from_seq !== b[i].to_seq)
  }
  /** A session's status in the last pushed list. */
  h.status = (sid) => h.events.filter((e) => e.event === 'sessions.changed').at(-1)?.params.sessions.find((s) => s.session_id === sid)?.status
  /** The last pushed list. */
  h.sessions = () => h.events.filter((e) => e.event === 'sessions.changed').at(-1)?.params.sessions ?? []
  h.deltas = (sid) => h.events.filter((e) => e.event === 'session.delta' && e.params.session_id === sid).map((e) => e.params.text)
  // Own the child before the first RPC: a failed handshake must not leave an unreturned process behind.
  const exited = new Promise((resolve) => {
    child.once('exit', resolve)
    child.once('error', resolve) // a failed spawn has no PID and emits no exit
  })
  const waitForExit = async (ms) => {
    let timer
    try {
      return await Promise.race([exited.then(() => true), new Promise((resolve) => { timer = setTimeout(() => resolve(false), ms) })])
    } finally { clearTimeout(timer) }
  }
  let stopping
  // Preserve the synchronous cleanup contract used by older standalone E2Es before process.exit().
  h.stop = () => {
    try { child.kill('SIGTERM') } catch {}
    if (!keep && root) rmSync(root, { recursive: true, force: true })
  }
  /** Stop and reap this child before removing its temporary home. Safe to await more than once. */
  h.stopAndWait = () => stopping ??= (async () => {
    if (child.pid && child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM')
      if (!await waitForExit(5000)) {
        child.kill('SIGKILL')
        if (!await waitForExit(5000)) throw new Error('node did not exit after SIGKILL')
      }
    }
    h.hub.close()
    child.stdin.destroy()
    child.stdout.destroy()
    child.stderr.destroy()
    if (!keep && root) rmSync(root, { recursive: true, force: true })
  })()
  child.stdin.on('error', (error) => h.hub.close(error))
  try {
    h.info = await h.hub.call('node.info', {}, { timeoutMs: 40_000, signal })
  } catch (error) {
    await h.stopAndWait()
    throw error
  }
  return h
}

/**
 * A resident node (`personad node --attach`, what launchd keeps running on a Mac) in a fresh HOME; hub connections
 * reach it through startNode({ home }). stop ends it (the HOME stays until removeHome).
 * @param {{ personad: string, home?: string }} opts
 */
export async function startResident({ personad, home: given }) {
  const root = given ? undefined : mkdtempSync(join('/tmp', 'jrt-res-'))
  const home = given ?? join(root, 'home')
  mkdirSync(join(home, '.persona'), { recursive: true })
  const child = spawn(personad, ['node', '--attach'], { env: { PATH: '/usr/bin:/bin', HOME: home }, stdio: ['ignore', 'ignore', 'pipe'] })
  const log = []
  child.stderr.on('data', (d) => log.push(String(d)))
  const r = { home, runtimeSocket: join(home, '.persona', 'runtime.sock'), log, child }
  await until('the resident node listening', () => existsSync(join(home, '.persona', 'node-attach.sock')) && existsSync(r.runtimeSocket), 20_000)
  r.stop = async () => {
    if (child.exitCode !== null) return
    const exited = new Promise((res) => child.once('exit', res))
    child.kill('SIGTERM')
    await Promise.race([exited, new Promise((res) => setTimeout(res, 10_000))])
  }
  r.removeHome = () => {
    if (root) rmSync(root, { recursive: true, force: true })
  }
  return r
}

/** A minimal check reporter: PASS / FAIL lines and a summary; exit code 1 when any failed. */
export function checks() {
  const results = []
  return {
    check(name, ok, detail = '') {
      results.push({ name, ok: !!ok })
      console.log(`${ok ? 'PASS' : 'FAIL'} ${name}${detail ? ` — ${detail}` : ''}`)
      return !!ok
    },
    summary() {
      const failed = results.filter((r) => !r.ok).length
      console.log(`${results.length - failed}/${results.length} checks passed`)
      return failed
    },
  }
}

export { until } from './fake-node.js'
