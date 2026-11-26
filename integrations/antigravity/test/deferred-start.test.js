import assert from 'node:assert/strict'
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { toAgyId } from '../lib/map.js'
import { legacyEnvName } from '../../runtime-sdk/lib/compat.js'
import { createAgyRuntime } from '../lib/runtime.js'

const FAKE_ACP = fileURLToPath(new URL('./fake-acp-policy.mjs', import.meta.url))

async function fixture(t) {
  const home = mkdtempSync(join(tmpdir(), 'agy-deferred-start-'))
  const record = join(home, 'requests.jsonl')
  writeFileSync(record, '')
  const acpBin = join(home, 'agy')
  const personadBin = join(home, 'personad')
  writeFileSync(acpBin, `#!/bin/sh\nexec '${process.execPath}' '${FAKE_ACP}' --record '${record}' "$@"\n`)
  writeFileSync(personadBin, '#!/bin/sh\nexit 0\n')
  chmodSync(acpBin, 0o700)
  chmodSync(personadBin, 0o700)
  const node = await startFakeNode(t)
  const logs = []
  const runtime = createAgyRuntime({ home, agyBin: acpBin, personadBin, socketPath: node.path, log: (line) => logs.push(line) })
  t.after(async () => { await runtime.stop(); rmSync(home, { recursive: true, force: true }) })
  runtime.start()
  await node.ready
  const calls = () => readFileSync(record, 'utf8').split('\n').filter(Boolean).map(JSON.parse)
  const start = () => node.peer.call('session.start', {
    cwd: home, prompt: 'Lead the task.', client_msg_id: 'first-task-message',
    defer_mcp: true, model: 'test-model', permission_mode: 'plan', effort: 'high',
  })
  const send = (session) => node.peer.call('session.send', {
    session_id: session, text: 'Lead the task.', client_msg_id: 'first-task-message',
  })
  return { home, node, logs, calls, start, send }
}

function methods(calls) {
  return calls.map((call) => call.method).filter(Boolean)
}

function assertMcpIdentity(load, session) {
  const server = load.params.mcpServers.find(({ name }) => name === 'persona')
  assert.ok(server, 'the activation load carries the primary Persona MCP server')
  const env = new Map(server.env.map(({ name, value }) => [name, value]))
  assert.equal(env.get('PERSONA_SESSION_ID'), session)
  assert.equal(env.get(legacyEnvName('PERSONA_SESSION_ID')), session)
  assert.equal(env.has('PERSONA_ASSISTANT'), false)
}

test('a deferred task start creates its ACP identity before MCP and prompts only on activation (#15327)', { timeout: 15_000 }, async (t) => {
  const { node, calls, start, send } = await fixture(t)
  const started = await start()
  const session = started.session.session_id
  assert.match(session, /^agy-/)
  assert.equal(started.session.starting, true)
  assert.equal(calls().filter((call) => call.method === 'session/new').length, 1)
  assert.deepEqual(calls().find((call) => call.method === 'session/new').params.mcpServers, [])
  assert.equal(calls().some((call) => ['session/resume', 'session/prompt'].includes(call.method)), false,
    `start attached MCP or prompted before activation: ${methods(calls())}`)
  await until('the deferred row stays starting', () => node.last('runtime.sessions')?.sessions
    .some((row) => row.session_id === session && row.starting === true))

  await send(session)
  const raw = toAgyId(session)
  await until('the first prompt completes', () => calls().some((call) => call.ended === raw))
  const sequence = methods(calls())
  assert.ok(sequence.indexOf('session/new') < sequence.indexOf('session/resume'))
  assert.ok(sequence.indexOf('session/resume') < sequence.indexOf('session/prompt'))
  assert.equal(sequence.filter((method) => method === 'session/prompt').length, 1)
  assertMcpIdentity(calls().find((call) => call.method === 'session/resume' && call.params.sessionId === raw), session)
})

test('a deferred start resumes with MCP after its ACP worker restarts (#15327)', { timeout: 15_000 }, async (t) => {
  const { logs, calls, start, send } = await fixture(t)
  const session = (await start()).session.session_id
  const raw = toAgyId(session)
  const firstWorker = calls().find((call) => call.method === 'session/new').worker
  process.kill(firstWorker, 'SIGKILL')
  await until('the ACP exit is observed', () => logs.some((line) => line.includes('agy acp exited')))
  await send(session)
  await until('the replacement worker completes the first prompt', () => calls()
    .some((call) => call.ended === raw && call.worker !== firstWorker))
  const load = calls().find((call) => call.method === 'session/resume' && call.params.sessionId === raw && call.worker !== firstWorker)
  assert.ok(load, 'the replacement ACP worker resumes the deferred session')
  assertMcpIdentity(load, session)
  assert.equal(calls().filter((call) => call.method === 'session/prompt').length, 1)
})

test('closing a deferred start discards it without MCP or a prompt, while an old immediate start is unchanged (#15327)', { timeout: 15_000 }, async (t) => {
  const { node, home, calls, start, send } = await fixture(t)
  const session = (await start()).session.session_id
  await node.peer.call('session.close', { session_id: session })
  assert.equal(calls().filter((call) => call.method === 'session/close').length, 1)
  assert.equal(calls().some((call) => ['session/resume', 'session/prompt'].includes(call.method)), false)
  await assert.rejects(send(session), /closed/)

  const immediate = await node.peer.call('session.start', { cwd: home, prompt: 'Start now.', client_msg_id: 'old-companion' })
  assert.equal(immediate.session.starting, undefined)
  await until('the old immediate start attaches MCP and prompts', () => {
    const sequence = methods(calls())
    return sequence.filter((method) => method === 'session/resume').length === 1 && sequence.filter((method) => method === 'session/prompt').length === 1
  })
  assert.equal(calls().filter((call) => call.method === 'session/prompt').length, 1)
  assert.equal(calls().filter((call) => call.method === 'session/resume').length, 1, JSON.stringify(calls()))
})
