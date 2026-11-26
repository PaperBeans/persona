import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { Peer, SUBPROTOCOL } from '../../runtime-sdk/index.js'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { parseConfig } from '../lib/config.js'
import { createManagedAgentsRuntime } from '../lib/runtime.js'
import { startFakeMA } from '../testing/fake-ma.mjs'

const MODEL_KEY = 'model-secret-abcdef'

async function fixture(t, { ma: maOpts = {}, profile = {}, node: nodeOpts = {} } = {}) {
  const ma = await startFakeMA(maOpts)
  t.after(() => ma.close())
  const node = await startFakeNode(t, { strict: true, ...nodeOpts })
  const config = parseConfig({ profiles: [{ id: 'cloud', name: 'Ark', mode: 'self', base_url: ma.url, api_key: 'env:MA_KEY',
    headers: { 'X-Ark-Beta': 'agentic-2026-06-01' }, model_headers: { Authorization: 'env:MODEL_AUTH' },
    start: { agent: 'agent-a', environment_id: 'env-1', label: 'Helper' }, poll_seconds: 1, ...profile }] })
  assert.deepEqual(config.problems, [])
  const logs = []
  const dir = await mkdtemp(join(tmpdir(), 'ma-rt-'))
  const rt = createManagedAgentsRuntime({ config, statePath: join(dir, 'state.json'), env: { MA_KEY: ma.key, MODEL_AUTH: `Bearer ${MODEL_KEY}` },
    log: (m) => logs.push(m), pollScale: 0.05, runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10, answerFirst: 'phone' } })
  await rt.start()
  await node.ready
  t.after(() => rt.stop())
  const rows = () => node.last('runtime.sessions')?.sessions ?? []
  const items = (sid) => node.all('session.items').filter((b) => b.session_id === sid).flatMap((b) => b.items ?? [])
  return { ma, node, rt, logs, rows, items }
}

async function launcherProfiles(t) {
  const mas = await Promise.all([startFakeMA({ list: false }), startFakeMA({ list: false })])
  for (const ma of mas) t.after(() => ma.close())
  assert.notEqual(mas[0].url, mas[1].url)
  const config = parseConfig({ profiles: mas.map((ma, i) => ({
    id: `profile-${i + 1}`, mode: 'self', base_url: ma.url, api_key: `env:MA_${i + 1}_KEY`,
    start: { agent: `agent-${i + 1}`, environment_id: `env-${i + 1}` }, poll_seconds: 1,
  })) })
  assert.deepEqual(config.problems, [])
  const env = Object.fromEntries(mas.map((ma, i) => [`MA_${i + 1}_KEY`, ma.key]))
  const statePath = join(await mkdtemp(join(tmpdir(), 'ma-launcher-')), 'state.json')
  return { mas, config, env, statePath }
}

// The node's end of a daemon connection, with its launcher rewritten by daemons.json.
function dialDaemonNode(url, token) {
  const ws = new WebSocket(url, { protocols: [SUBPROTOCOL], headers: { authorization: `Bearer ${token}` } })
  const handlers = { data: [], close: [], error: [] }
  const socket = {
    setEncoding() {},
    on(ev, fn) { (handlers[ev] ??= []).push(fn) },
    write(s) { ws.send(s.endsWith('\n') ? s.slice(0, -1) : s) },
    destroy() { ws.close() },
  }
  const node = { ws, hello: undefined }
  ws.onmessage = (e) => handlers.data.forEach((fn) => fn(String(e.data) + '\n'))
  ws.onclose = () => handlers.close.forEach((fn) => fn())
  ws.onerror = () => {}
  node.peer = new Peer(socket, {
    prefix: 'n',
    onRequest: async (method, params) => {
      if (method === 'runtime.hello') {
        node.hello = params
        return { protocol: 1, node_version: 'test', hub: true, features: ['prefer_running'] }
      }
      throw new Error(`unexpected ${method}`)
    },
    onEvent: () => {},
  })
  return node
}

test('hello names the runtime and its launcher; sessions are listed grouped by agent', async (t) => {
  const { ma, node, rt, rows } = await fixture(t)
  assert.equal(node.hello.runtime, 'managed-agents')
  assert.equal(node.hello.name, 'Managed Agents')
  assert.deepEqual(node.hello.caps, { ...node.hello.caps, start: true, send: true, interrupt: true })
  assert.deepEqual(node.hello.launchers, [{ name: 'ma-cloud', display_name: 'Helper', agent: 'managed-agents', model: '' }])
  const s = ma.create({ title: 'Quarterly report', agent: 'agent-a' })
  ma.emit(s.id, { type: 'user.message', content: [{ type: 'text', text: 'hello' }] })
  ma.emit(s.id, { type: 'agent.message', content: [{ type: 'text', text: 'Hi there' }] })
  ma.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
  await rt.refresh()
  const row = await until('listed', () => rows().find((r) => r.session_id === `cloud.${s.id}`))
  assert.equal(row.title, 'Quarterly report')
  assert.equal(row.space_id, 'cloud:agent-a')
  assert.equal(row.space_name, 'Helper')
  assert.equal(row.model, 'model-x')
})

test('reads a conversation page by page, with the key on every request and the model key nowhere but sends', async (t) => {
  const { ma, node, rt } = await fixture(t, { ma: { pageSize: 2 } })
  const s = ma.create({ title: 'Read me' })
  ma.emit(s.id, { type: 'user.message', content: [{ type: 'text', text: 'list files' }] })
  ma.emit(s.id, { type: 'session.status_running' })
  ma.emit(s.id, { type: 'agent.tool_use', name: 'bash', input: { command: 'ls' }, evaluated_permission: 'allow' })
  ma.emit(s.id, { type: 'agent.tool_result', tool_use_id: s.events[2].id, content: [{ type: 'text', text: 'a.txt' }] })
  ma.emit(s.id, { type: 'agent.message', content: [{ type: 'text', text: 'One file: a.txt' }] })
  ma.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
  await rt.refresh()
  const sid = `cloud.${s.id}`
  const page = await node.peer.call('session.items', { session_id: sid })
  assert.deepEqual(page.items.map((i) => i.kind), ['text', 'tool_use', 'tool_result', 'text'])
  assert.equal(page.history, `ma:${sid}`)
  assert.equal(page.to_seq, 6)
  assert.equal(page.items[1].tool_name, 'Bash')
  const last = await node.peer.call('session.items', { session_id: sid, turns: 1, after_seq: 5 })
  assert.equal(last.to_seq, 6)
  // Three pages of two events each were followed.
  assert.ok(ma.requests.filter((r) => r.path.endsWith('/events') && r.method === 'GET').length >= 3)
  for (const r of ma.requests) {
    assert.equal(r.headers.authorization, `Bearer ${ma.key}`)
    assert.equal(r.headers['x-ark-beta'], 'agentic-2026-06-01')
    assert.equal(r.headers['x-user-model-header-authorization'], undefined, `${r.method} ${r.path} carried the model key`)
  }
})

test('sends a message once per client_msg_id and pushes the reply as it lands', async (t) => {
  const { ma, node, rt, rows, items } = await fixture(t)
  const s = ma.create({ title: 'Chat' })
  await rt.refresh()
  const sid = `cloud.${s.id}`
  await until('listed', () => rows().find((r) => r.session_id === sid))
  await node.peer.call('session.watch', { session_id: sid, since_seq: 0 })
  await node.peer.call('session.send', { session_id: sid, text: 'ping', client_msg_id: 'm1' })
  await node.peer.call('session.send', { session_id: sid, text: 'ping', client_msg_id: 'm1' }) // the app retries
  await until('the reply', () => items(sid).some((i) => i.text === 'echo: ping'), 5000)
  const posts = ma.requests.filter((r) => r.method === 'POST' && r.path.endsWith('/events'))
  assert.equal(posts.length, 1)
  assert.deepEqual(JSON.parse(posts[0].body), { events: [{ type: 'user.message', content: [{ type: 'text', text: 'ping' }] }] })
  // BYOK: the model credential rides on the send, under the configured prefix.
  assert.equal(posts[0].headers['x-user-model-header-authorization'], `Bearer ${MODEL_KEY}`)
  await until('done', () => rows().find((r) => r.session_id === sid)?.status === 'done', 5000)
  assert.equal(rows().find((r) => r.session_id === sid).last_text, 'echo: ping')
  // Pushed batches continue each other.
  const batches = node.all('session.items').filter((b) => b.session_id === sid)
  for (let i = 1; i < batches.length; i++) assert.equal(batches[i].from_seq, batches[i - 1].to_seq)
})

test('streams live text while watched', async (t) => {
  let release
  const { ma, node, rt, rows } = await fixture(t, { ma: { agent: async (s, text, api) => {
    api.emit(s.id, { type: 'session.status_running' })
    await new Promise((r) => { release = r })
    api.delta(s.id, 'evt-live', 'Hel')
    api.delta(s.id, 'evt-live', 'lo')
    await new Promise((r) => setTimeout(r, 300))
    api.emit(s.id, { id: 'evt-live', type: 'agent.message', content: [{ type: 'text', text: 'Hello' }] })
    api.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
  } } })
  const s = ma.create()
  await rt.refresh()
  const sid = `cloud.${s.id}`
  await until('listed', () => rows().find((r) => r.session_id === sid))
  await node.peer.call('session.watch', { session_id: sid, since_seq: 0 })
  await until('the stream open', () => ma.streamCount() > 0, 3000)
  await node.peer.call('session.send', { session_id: sid, text: 'hi', client_msg_id: 'm' })
  await until('the agent runs', () => release, 3000)
  release()
  await until('live text', () => node.all('session.delta').some((d) => d.session_id === sid && d.text === 'Hello'), 3000)
  await until('it ends', () => node.all('session.delta').some((d) => d.session_id === sid && d.text === ''), 3000)
})

test('a tool confirmation becomes a permission card; allow and deny go back as tool confirmations', async (t) => {
  let answered = []
  const { ma, node, rt, rows, items } = await fixture(t, { ma: {
    agent: async (s, text, api) => {
      api.emit(s.id, { type: 'session.status_running' })
      const call = api.emit(s.id, { type: 'agent.tool_use', name: 'bash', input: { command: `rm ${text}` }, evaluated_permission: 'ask' })
      api.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'requires_action', event_ids: [call.id] } })
    },
    onAnswer: async (s, ev, api) => {
      answered.push(ev)
      api.emit(s.id, { type: 'session.status_running' })
      api.emit(s.id, { type: 'agent.tool_result', tool_use_id: ev.tool_use_id, content: [{ type: 'text', text: ev.result === 'allow' ? 'removed' : 'denied' }], is_error: ev.result !== 'allow' })
      api.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
    },
  } })
  node.resolve = (ix) => (ix.tool_input.command === 'rm a' ? { allow: true } : { allow: false, message: 'keep it' })
  const s = ma.create()
  await rt.refresh()
  const sid = `cloud.${s.id}`
  await until('listed', () => rows().find((r) => r.session_id === sid))
  await node.peer.call('session.watch', { session_id: sid, since_seq: 0 })
  await node.peer.call('session.send', { session_id: sid, text: 'a', client_msg_id: 'a' })
  await until('allowed', () => answered.length === 1, 5000)
  const card = node.asked[0]
  assert.equal(card.kind, 'permission')
  assert.equal(card.session_id, sid)
  assert.equal(card.tool_name, 'Bash')
  assert.equal(card.summary, 'rm a')
  assert.deepEqual({ ...answered[0], session_thread_id: undefined }, { type: 'user.tool_confirmation', tool_use_id: card.tool_use_id, result: 'allow', session_thread_id: undefined })
  await until('done', () => rows().find((r) => r.session_id === sid)?.status === 'done', 5000)
  await node.peer.call('session.send', { session_id: sid, text: 'b', client_msg_id: 'b' })
  await until('denied', () => answered.length === 2, 5000)
  assert.equal(answered[1].result, 'deny')
  assert.equal(answered[1].deny_message, 'keep it')
  await until('the result', () => items(sid).some((i) => i.kind === 'tool_result' && i.text === 'denied'), 5000)
  assert.equal(node.asked.length, 2, 'each call is asked once')
})

test('AskQuestion and ExitPlanMode become question and plan cards answered with custom tool results', async (t) => {
  const results = []
  const { ma, node, rt, rows } = await fixture(t, { ma: {
    agent: async (s, text, api) => {
      api.emit(s.id, { type: 'session.status_running' })
      const call = text === 'q'
        ? api.emit(s.id, { type: 'agent.custom_tool_use', name: 'AskQuestion', input: { questions: [{ question: 'Which city?', header: 'City', options: [{ label: 'Paris' }, { label: 'Rome' }] }] } })
        : api.emit(s.id, { type: 'agent.custom_tool_use', name: 'ExitPlanMode', input: { title: 'Trip', plan: '1. Book\n2. Go' } })
      api.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'requires_action', event_ids: [call.id] } })
    },
    onAnswer: async (s, ev, api) => {
      results.push(ev)
      api.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
    },
  } })
  node.resolve = (ix) => (ix.kind === 'question' ? { answers: { 'Which city?': 'Rome' } } : { approve: true, mode: 'acceptEdits' })
  const s = ma.create()
  await rt.refresh()
  const sid = `cloud.${s.id}`
  await until('listed', () => rows().find((r) => r.session_id === sid))
  await node.peer.call('session.send', { session_id: sid, text: 'q', client_msg_id: 'q' })
  await until('answered', () => results.length === 1, 5000)
  assert.equal(node.asked[0].kind, 'question')
  assert.equal(node.asked[0].questions[0].question, 'Which city?')
  assert.deepEqual(JSON.parse(results[0].content[0].text), { answers: { 'Which city?': 'Rome' } })
  await until('done', () => rows().find((r) => r.session_id === sid)?.status === 'done', 5000)
  await node.peer.call('session.send', { session_id: sid, text: 'p', client_msg_id: 'p' })
  await until('approved', () => results.length === 2, 5000)
  assert.equal(node.asked[1].kind, 'plan')
  assert.match(node.asked[1].plan, /1\. Book/)
  assert.equal(JSON.parse(results[1].content[0].text).decision, 'approved')
})

test('a card answered elsewhere is withdrawn from the phone', async (t) => {
  const { ma, node, rt, rows } = await fixture(t)
  let asked
  node.resolve = () => new Promise((r) => { asked = r })
  const s = ma.create()
  const call = ma.emit(s.id, { type: 'agent.tool_use', name: 'bash', input: { command: 'deploy' }, evaluated_permission: 'ask' })
  ma.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'requires_action', event_ids: [call.id] } })
  await rt.refresh()
  const sid = `cloud.${s.id}`
  await until('blocked', () => rows().find((r) => r.session_id === sid)?.status === 'blocked', 5000)
  await until('asked', () => asked, 3000)
  ma.emit(s.id, { type: 'user.tool_confirmation', tool_use_id: call.id, result: 'allow' })
  ma.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
  await until('withdrawn', () => node.events.some((e) => e.event === 'interaction.cancel'), 5000)
  asked({ dismiss: true })
})

test('starts a session from the launcher, with its first message, and keeps listing it without a list route', async (t) => {
  const { ma, node, rt, rows, items } = await fixture(t, { ma: { list: false } })
  const { session } = await node.peer.call('session.start', { launcher: 'ma-cloud', cwd: '/tmp', prompt: 'plan a trip', client_msg_id: 'first' })
  assert.match(session.session_id, /^cloud\.sesn-/)
  const created = ma.requests.find((r) => r.method === 'POST' && r.path === '/api/v3/sessions')
  assert.deepEqual(JSON.parse(created.body), { agent: 'agent-a', environment_id: 'env-1', title: 'plan a trip' })
  await node.peer.call('session.watch', { session_id: session.session_id, since_seq: 0 })
  await until('the reply', () => items(session.session_id).some((i) => i.text === 'echo: plan a trip'), 5000)
  await rt.refresh()
  await until('still listed', () => rows().some((r) => r.session_id === session.session_id && r.started_by_persona), 3000)
})

test('an unknown launcher creates no session and sends no prompt to either endpoint', async (t) => {
  const { mas, config, env, statePath } = await launcherProfiles(t)
  const node = await startFakeNode(t, { strict: true })
  const rt = createManagedAgentsRuntime({ config, env, statePath, pollScale: 0.05,
    runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10 } })
  t.after(() => rt.stop())
  await rt.start()
  await node.ready
  const err = await node.peer.call('session.start', { launcher: 'stale-launcher', cwd: '/tmp', prompt: 'must not be routed', client_msg_id: 'unknown' })
    .then(() => undefined, (e) => e)
  assert.deepEqual(mas.map((ma) => ma.requests.filter((r) => r.method === 'POST').map((r) => r.path)), [[], []])
  assert.equal(err?.code, 'launcher_missing')
  for (const ma of mas) assert.equal(ma.sessions.size, 0)
})

test('a daemon alias still starts the first profile and sends its prompt only there', async (t) => {
  const { mas, config, env, statePath } = await launcherProfiles(t)
  const token = 'test-daemon-token'
  const rt = createManagedAgentsRuntime({ config, env, statePath, pollScale: 0.05,
    runtimeOptions: { listen: { port: 0, token }, pushDelayMs: 10 } })
  t.after(() => rt.stop())
  const addr = await rt.start()
  const node = dialDaemonNode(`ws://127.0.0.1:${addr.port}/persona/runtime`, token)
  t.after(() => node.ws.close())
  await until('daemon hello', () => node.hello)
  const { session } = await node.peer.call('session.start', { launcher: 'daemon-alias', cwd: '/tmp', prompt: 'daemon prompt', client_msg_id: 'daemon' })
  assert.match(session.session_id, /^profile-1\.sesn-/)
  assert.equal(mas[0].sessions.size, 1)
  const created = mas[0].requests.find((r) => r.method === 'POST' && r.path === '/api/v3/sessions')
  assert.deepEqual(JSON.parse(created.body), { agent: 'agent-1', environment_id: 'env-1', title: 'daemon prompt' })
  const sent = mas[0].requests.find((r) => r.method === 'POST' && r.path.endsWith('/events'))
  assert.deepEqual(JSON.parse(sent.body), { events: [{ type: 'user.message', content: [{ type: 'text', text: 'daemon prompt' }] }] })
  assert.equal(mas[1].sessions.size, 0)
  assert.deepEqual(mas[1].requests.filter((r) => r.method === 'POST'), [])
})

test('interrupt sends user.interrupt and settles once the turn ended', async (t) => {
  const { ma, node, rt, rows } = await fixture(t, { ma: { agent: async (s, text, api) => api.emit(s.id, { type: 'session.status_running' }) } })
  const s = ma.create()
  await rt.refresh()
  const sid = `cloud.${s.id}`
  await until('listed', () => rows().find((r) => r.session_id === sid))
  await node.peer.call('session.send', { session_id: sid, text: 'long job', client_msg_id: 'x' })
  await until('working', () => rows().find((r) => r.session_id === sid)?.status === 'working', 5000)
  const res = await node.peer.call('session.interrupt', { session_id: sid })
  assert.deepEqual(res, { ok: true, settled: true })
  assert.ok(ma.requests.some((r) => r.method === 'POST' && JSON.parse(r.body || '{}').events?.[0]?.type === 'user.interrupt'))
})

test('a session the server does not have is not_found and leaves nothing behind', async (t) => {
  const { node, rt } = await fixture(t)
  const err = await node.peer.call('session.items', { session_id: 'cloud.sesn-missing' }).then(() => undefined, (e) => e)
  assert.equal(err?.code, 'not_found')
  assert.equal(rt.sessions.has('cloud.sesn-missing'), false)
})

test('a wrong key is reported without the key, and nothing is listed', async (t) => {
  const ma = await startFakeMA({ key: 'right-key-123456' })
  t.after(() => ma.close())
  const node = await startFakeNode(t)
  const config = parseConfig({ profiles: [{ id: 'cloud', mode: 'self', base_url: ma.url, api_key: 'env:MA_KEY' }] })
  const logs = []
  const rt = createManagedAgentsRuntime({ config, statePath: join(await mkdtemp(join(tmpdir(), 'ma-')), 's.json'), env: { MA_KEY: 'wrong-key-abcdef' },
    log: (m) => logs.push(m), pollScale: 0.05, runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10 } })
  ma.create()
  await rt.start()
  t.after(() => rt.stop())
  await node.ready
  await until('a warning', () => logs.some((l) => /401/.test(l)), 3000)
  assert.ok(logs.every((l) => !l.includes('wrong-key-abcdef') && !l.includes('right-key-123456')), logs.join('\n'))
  assert.deepEqual(node.last('runtime.sessions')?.sessions ?? [], [])
})

test('start reconciles warm-container idle sessions with bounded concurrency before first push and falls back to created_at', async (t) => {
  const ma = await startFakeMA()
  t.after(() => ma.close())
  const createdAt = '2026-11-23T09:15:00.000Z'
  const createdIds = []
  for (let i = 0; i < 8; i++) {
    const s = ma.create({ title: `Warm ${i}` })
    ma.emit(s.id, { type: 'user.message', content: [{ type: 'text', text: `hi ${i}` }] })
    ma.emit(s.id, { type: 'agent.message', content: [{ type: 'text', text: `done ${i}` }] })
    const idle = ma.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
    if (i === 0) {
      delete idle.processed_at
      idle.created_at = createdAt
      s.updated_at = '2026-11-20T00:00:00.000Z'
    }
    // Ark keeps status: "running" while the container stays warm after end_turn.
    s.status = 'running'
    createdIds.push(s.id)
  }

  let inFlight = 0
  let maxInFlight = 0
  const trackedFetch = async (url, init) => {
    const isEvents = String(url).includes('/events')
    if (isEvents) {
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      await new Promise((r) => setTimeout(r, 25))
    }
    try {
      return await fetch(url, init)
    } finally {
      if (isEvents) inFlight--
    }
  }

  const node = await startFakeNode(t, { strict: true })
  const config = parseConfig({ profiles: [{ id: 'ark', mode: 'self', base_url: ma.url, api_key: 'env:MA_KEY', poll_seconds: 60 }] })
  const rt = createManagedAgentsRuntime({
    config,
    statePath: join(await mkdtemp(join(tmpdir(), 'ma-warm-')), 'state.json'),
    env: { MA_KEY: ma.key },
    fetch: trackedFetch,
    pollScale: 0.05,
    runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10 },
  })
  await rt.start()
  t.after(() => rt.stop())
  await node.ready
  const firstPush = await until('first sessions push', () => node.all('runtime.sessions')[0])
  assert.equal(firstPush.sessions.length, 8)
  for (const id of createdIds) {
    const row = firstPush.sessions.find((r) => r.session_id === `ark.${id}`)
    assert.ok(row, `missing ${id}`)
    assert.equal(row.status, 'done', `warm-container session ${id} should be done on first push`)
  }
  const firstRow = firstPush.sessions.find((r) => r.session_id === `ark.${createdIds[0]}`)
  assert.equal(firstRow.updated_at, createdAt)
  assert.ok(maxInFlight >= 2 && maxInFlight <= 4, `expected bounded /events concurrency <= 4, got ${maxInFlight}`)
})

test('file-backed runtime and name reach runtime.hello and reconnect on reload', async (t) => {
  const ma = await startFakeMA()
  t.after(() => ma.close())
  const node = await startFakeNode(t, { strict: true })
  const dir = await mkdtemp(join(tmpdir(), 'ma-ident-'))
  const configPath = join(dir, 'managed-agents.json')
  const writeCfg = (runtime, name, launcher) => writeFile(configPath, JSON.stringify({
    runtime,
    name,
    profiles: [{ id: 'ark', mode: 'self', base_url: ma.url, api_key: 'env:MA_KEY', start: { agent: 'agent-a', launcher }, poll_seconds: 10 }],
  }), { mode: 0o600 })
  await writeCfg('ma-alpha', 'Alpha Agents', 'ma-alpha-start')

  const rt = createManagedAgentsRuntime({
    configPath,
    statePath: join(dir, 'state.json'),
    env: { MA_KEY: ma.key },
    pollScale: 0.01,
    runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10 },
  })
  await rt.start()
  t.after(() => rt.stop())
  await node.ready
  assert.equal(node.hello.runtime, 'ma-alpha')
  assert.equal(node.hello.name, 'Alpha Agents')
  assert.equal(node.hello.launcher, 'ma-alpha-start')
  assert.deepEqual(node.hello.launchers, [{ name: 'ma-alpha-start', display_name: 'ark', agent: 'ma-alpha', model: '' }])

  const hellosBefore = node.hellos
  await writeCfg('ma-beta', 'Beta Agents', 'ma-beta-start')
  await until('reconnect under new identity', () => node.hellos > hellosBefore && node.hello?.runtime === 'ma-beta', 3000)
  assert.equal(node.hello.name, 'Beta Agents')
  assert.equal(node.hello.launcher, 'ma-beta-start')
  assert.deepEqual(node.hello.launchers, [{ name: 'ma-beta-start', display_name: 'ark', agent: 'ma-beta', model: '' }])

  // A second companion with its own file-backed runtime and name connects alongside without colliding.
  const configPath2 = join(dir, 'managed-agents-2.json')
  await writeFile(configPath2, JSON.stringify({
    runtime: 'ma-second',
    name: 'Second Agents',
    profiles: [{ id: 'ark2', mode: 'self', base_url: ma.url, api_key: 'env:MA_KEY', poll_seconds: 10 }],
  }), { mode: 0o600 })
  const rt2 = createManagedAgentsRuntime({
    configPath: configPath2,
    statePath: join(dir, 'state-2.json'),
    env: { MA_KEY: ma.key },
    pollScale: 0.05,
    runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10 },
  })
  await rt2.start()
  t.after(() => rt2.stop())
  await until('second companion connects with distinct identity', () => node.hello?.runtime === 'ma-second', 3000)
  assert.equal(node.hello.name, 'Second Agents')
  assert.equal(node.peers.size, 2)
})

test('interrupt does not settle while waiting on requires_action or when sync fails on an unloaded session', async (t) => {
  // Case 1: waiting on requires_action, server delays ending turn until released
  let endTurn
  const { ma, node, rt, rows } = await fixture(t, { ma: {
    onInterrupt: async (s, ev, api) => {
      await new Promise((r) => { endTurn = r })
      api.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'end_turn' } })
    },
  } })
  let resolveCard
  node.resolve = () => new Promise((r) => { resolveCard = r })
  const s = ma.create()
  const call = ma.emit(s.id, { type: 'agent.tool_use', name: 'bash', input: { command: 'rm -rf tmp' }, evaluated_permission: 'ask' })
  ma.emit(s.id, { type: 'session.status_idle', stop_reason: { type: 'requires_action', event_ids: [call.id] } })
  await rt.refresh()
  const sid = `cloud.${s.id}`
  await until('blocked', () => rows().find((r) => r.session_id === sid)?.status === 'blocked', 3000)
  await until('card open', () => resolveCard, 3000)

  // Interrupt while paused on requires_action: card withdraws once log records user.interrupt,
  // but interrupt must NOT settle until end_turn arrives.
  const interruptPromise = node.peer.call('session.interrupt', { session_id: sid })
  await until('interrupt reached server', () => endTurn, 3000)
  await until('card withdrawn by reconcileAsks', () => node.events.some((e) => e.event === 'interaction.cancel'), 3000)
  let settledEarly = false
  interruptPromise.then(() => { settledEarly = true })
  await new Promise((r) => setTimeout(r, 80))
  assert.equal(settledEarly, false, 'interrupt must not report settled while logStatus is still waiting')
  endTurn()
  const settledRes = await interruptPromise
  assert.deepEqual(settledRes, { ok: true, settled: true })
  resolveCard({ dismiss: true })

  // Case 2: waiting session where server never ends the turn -> returns { ok: true } without settled
  const { ma: ma2, node: node2, rt: rt2, rows: rows2 } = await fixture(t, { ma: {
    onInterrupt: async () => {},
  } })
  const s2 = ma2.create()
  const call2 = ma2.emit(s2.id, { type: 'agent.tool_use', name: 'bash', input: { command: 'ls' }, evaluated_permission: 'ask' })
  ma2.emit(s2.id, { type: 'session.status_idle', stop_reason: { type: 'requires_action', event_ids: [call2.id] } })
  await rt2.refresh()
  const sid2 = `cloud.${s2.id}`
  await until('blocked 2', () => rows2().find((r) => r.session_id === sid2)?.status === 'blocked', 3000)
  const unsettledWaiting = await node2.peer.call('session.interrupt', { session_id: sid2 })
  assert.deepEqual(unsettledWaiting, { ok: true })

  // Case 3: unloaded session whose GET /events fails -> must not settle on unknown
  const ma3 = await startFakeMA({ onInterrupt: async () => {} })
  t.after(() => ma3.close())
  const s3 = ma3.create()
  const node3 = await startFakeNode(t, { strict: true })
  const config3 = parseConfig({ profiles: [{ id: 'cloud', mode: 'self', base_url: ma3.url, api_key: 'env:MA_KEY', list: { enabled: false }, poll_seconds: 60 }] })
  const failingEventsFetch = async (url, init) => {
    if ((init?.method ?? 'GET') === 'GET' && String(url).includes('/events')) {
      return new Response(JSON.stringify({ error: { message: 'boom' } }), { status: 500, headers: { 'Content-Type': 'application/json' } })
    }
    return fetch(url, init)
  }
  const rt3 = createManagedAgentsRuntime({
    config: config3,
    statePath: join(await mkdtemp(join(tmpdir(), 'ma-unk-')), 'state.json'),
    env: { MA_KEY: ma3.key },
    fetch: failingEventsFetch,
    pollScale: 0.02,
    runtimeOptions: { socketPath: node3.path, retryMs: 50, pushDelayMs: 10 },
  })
  await rt3.start()
  t.after(() => rt3.stop())
  await node3.ready
  const unsettledUnknown = await node3.peer.call('session.interrupt', { session_id: `cloud.${s3.id}` })
  assert.deepEqual(unsettledUnknown, { ok: true })
})

test('each profile polls at its own poll_seconds without 3s clamp and re-arms on config reload', async (t) => {
  const fastMa = await startFakeMA()
  const slowMa = await startFakeMA()
  t.after(() => fastMa.close())
  t.after(() => slowMa.close())
  const fastS = fastMa.create({ title: 'Fast' })
  fastMa.emit(fastS.id, { type: 'session.status_running' })
  const slowS = slowMa.create({ title: 'Slow' })
  slowMa.emit(slowS.id, { type: 'session.status_running' })

  const node = await startFakeNode(t, { strict: true })
  const dir = await mkdtemp(join(tmpdir(), 'ma-poll-'))
  const configPath = join(dir, 'managed-agents.json')
  const writeCfg = (slowSeconds) => writeFile(configPath, JSON.stringify({
    profiles: [
      { id: 'fast', mode: 'self', base_url: fastMa.url, api_key: 'env:FAST_KEY', poll_seconds: 1 },
      { id: 'slow', mode: 'self', base_url: slowMa.url, api_key: 'env:SLOW_KEY', poll_seconds: slowSeconds },
    ],
  }), { mode: 0o600 })
  await writeCfg(20)

  const rt = createManagedAgentsRuntime({
    configPath,
    statePath: join(dir, 'state.json'),
    env: { FAST_KEY: fastMa.key, SLOW_KEY: slowMa.key },
    pollScale: 0.05,
    runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10 },
  })
  await rt.start()
  t.after(() => rt.stop())
  await node.ready

  const eventsCount = (ma) => ma.requests.filter((r) => r.method === 'GET' && r.path.endsWith('/events')).length
  const fastStart = eventsCount(fastMa)
  const slowStart = eventsCount(slowMa)

  // Wait 350ms: fast (50ms) polls several times; slow (20s * 0.05 = 1000ms) must NOT be clamped to 3s (150ms) or fast's rate.
  await new Promise((r) => setTimeout(r, 350))
  const fastDelta = eventsCount(fastMa) - fastStart
  const slowDelta = eventsCount(slowMa) - slowStart
  assert.ok(fastDelta >= 3, `expected fast profile to poll >= 3 times, got ${fastDelta}`)
  assert.equal(slowDelta, 0, `expected slow profile (20s) not to poll in 350ms, got ${slowDelta}`)

  // Reload config changing slow profile to 1s (50ms) and verify timer re-arms.
  const slowBeforeReload = eventsCount(slowMa)
  await writeCfg(1)
  await until('slow profile polls after reload', () => eventsCount(slowMa) - slowBeforeReload >= 2, 2000)
})

test('inline config without statePath falls back to default state path under PERSONA_HOME and keeps reload a no-op', async (t) => {
  const ma = await startFakeMA({ list: false })
  t.after(() => ma.close())
  const node = await startFakeNode(t, { strict: true })
  const personaHome = await mkdtemp(join(tmpdir(), 'ma-default-state-'))
  // Put a different config at $PERSONA_HOME/managed-agents.json to prove reload stays a no-op for inline o.config.
  await writeFile(join(personaHome, 'managed-agents.json'), JSON.stringify({
    runtime: 'from-file-ignored',
    profiles: [],
  }), { mode: 0o600 })

  const config = parseConfig({
    profiles: [{
      id: 'cloud',
      mode: 'self',
      base_url: ma.url,
      api_key: 'env:MA_KEY',
      start: { agent: 'agent-a', environment_id: 'env-1' },
      poll_seconds: 1,
    }],
  })
  assert.deepEqual(config.problems, [])

  // Constructing with inline config and no statePath (or env) must not throw TypeError from dirname(undefined).
  assert.doesNotThrow(() => createManagedAgentsRuntime({ config }))

  const rt = createManagedAgentsRuntime({
    config,
    env: { PERSONA_HOME: personaHome, MA_KEY: ma.key },
    pollScale: 0.05,
    runtimeOptions: { socketPath: node.path, retryMs: 50, pushDelayMs: 10 },
  })
  await rt.start()
  t.after(() => rt.stop())
  await node.ready

  await rt.reload()
  assert.equal(node.hello.runtime, 'managed-agents')
  assert.equal(rt.profiles.has('cloud'), true)

  const { session } = await node.peer.call('session.start', { launcher: 'ma-cloud', cwd: '/tmp', prompt: 'hello', client_msg_id: 'default-state' })
  const stateFile = join(personaHome, 'managed-agents-state.json')
  const saved = await until('default state file persisted', async () => {
    try {
      return JSON.parse(await readFile(stateFile, 'utf8'))
    } catch {
      return undefined
    }
  }, 3000)
  assert.deepEqual(saved, { started: [{ profile: 'cloud', id: session.session_id.slice('cloud.'.length) }] })
})
