import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readlinkSync, rmSync, rmdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { installCurrent, PACKAGES, packageHash, readShipped, verifyPackage } from '../../runtime-sdk/lib/package.js'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { installAntigravityPackage, PACKAGE_NAME } from '../install.mjs'
import { createAgyRuntime } from '../lib/runtime.js'

const INTEGRATIONS_ROOT = join(dirname(fileURLToPath(import.meta.url)), '../..')

const FAKE_AGY_SOURCE = `
import { existsSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { createInterface } from 'node:readline'

const stateDir = process.argv[2]
if (process.argv.includes('models')) {
  console.log('test-model\\tTest Model')
  process.exit(0)
}

const send = (obj) => process.stdout.write(JSON.stringify(obj) + '\\n')
let pendingPermId = null
let pendingPromptId = null

function maybeFinishPrompt() {
  if (pendingPromptId === null || pendingPermId !== null) return
  if (!existsSync(join(stateDir, 'release-turn'))) {
    setTimeout(maybeFinishPrompt, 15).unref?.()
    return
  }
  const id = pendingPromptId
  pendingPromptId = null
  send({ jsonrpc: '2.0', id, result: { stopReason: 'end_turn' } })
}

const rl = createInterface({ input: process.stdin, crlfDelay: Infinity })
rl.on('line', (line) => {
  if (!line.trim()) return
  const msg = JSON.parse(line)
  if (msg.id !== undefined && !msg.method) {
    if (msg.id === pendingPermId) {
      pendingPermId = null
      writeFileSync(join(stateDir, 'perm-answered'), '1')
      maybeFinishPrompt()
    }
    return
  }
  if (msg.method === 'session/cancel') {
    if (pendingPromptId !== null) {
      const id = pendingPromptId
      pendingPromptId = null
      pendingPermId = null
      send({ jsonrpc: '2.0', id, result: { stopReason: 'cancelled' } })
    }
    return
  }
  switch (msg.method) {
    case 'initialize':
      send({ jsonrpc: '2.0', id: msg.id, result: { protocolVersion: 1, agentInfo: { name: 'fake-agy', version: '1.0.0' }, agentCapabilities: {} } })
      break
    case 'session/new':
      send({ jsonrpc: '2.0', id: msg.id, result: { sessionId: '22222222-3333-4444-8555-666666666666' } })
      break
    case 'session/resume':
    case 'session/set_model':
    case 'session/set_mode':
    case 'session/set_config_option':
    case 'session/close':
      send({ jsonrpc: '2.0', id: msg.id, result: {} })
      break
    case 'session/prompt': {
      const text = msg.params?.prompt?.find((b) => b.type === 'text')?.text?.trim() || ''
      if (text === 'hold-with-card') {
        pendingPromptId = msg.id
        pendingPermId = 'perm-1'
        send({
          jsonrpc: '2.0',
          id: pendingPermId,
          method: 'session/request_permission',
          params: {
            sessionId: msg.params.sessionId,
            toolCall: { toolCallId: 'call-1', title: 'run_command', rawInput: { command: 'echo ok' } },
            options: [
              { optionId: 'allow', name: 'Allow', kind: 'allow_once' },
              { optionId: 'reject', name: 'Reject', kind: 'reject_once' },
            ],
          },
        })
      } else {
        send({ jsonrpc: '2.0', id: msg.id, result: { stopReason: 'end_turn' } })
      }
      break
    }
    default:
      send({ jsonrpc: '2.0', id: msg.id, error: { code: -32601, message: 'Method not found' } })
  }
})
rl.on('close', () => process.exit(0))
`

function makeEnv(t) {
  const dir = mkdtempSync(join(tmpdir(), 'agy-update-'))
  const fakeScript = join(dir, 'fake-agy.mjs')
  const binary = join(dir, 'agy')
  const agyDir = join(dir, 'agy-config')
  const store = join(dir, 'store')
  const socketPath = join(dir, 'runtime.sock')
  mkdirSync(agyDir, { recursive: true })
  writeFileSync(fakeScript, FAKE_AGY_SOURCE)
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${fakeScript}' '${dir}' "$@"\n`)
  chmodSync(binary, 0o700)
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  return { dir, binary, agyDir, store, socketPath }
}

function offerOf(marker) {
  const files = readShipped(INTEGRATIONS_ROOT, PACKAGES[PACKAGE_NAME]).map((f) =>
    f.path === 'antigravity/companion.mjs'
      ? { path: f.path, data: Buffer.concat([f.data, Buffer.from(`\n// ${marker}\n`)]) }
      : f,
  )
  return {
    name: PACKAGE_NAME,
    hash: packageHash(files),
    version: 'node-next',
    files: files.map((f) => ({ path: f.path, data: f.data.toString('base64') })),
  }
}

test('a non-store launch sends no package hash and ignores node update offers', async (t) => {
  const { dir, binary, agyDir, socketPath } = makeEnv(t)
  const node = await startFakeNode(t, { path: socketPath })
  node.offer = offerOf('v2')
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    log: () => {},
  })
  t.after(() => runtime.stop())
  runtime.start()
  await node.ready
  assert.equal(node.hello.package, undefined)
  assert.equal(node.packageCalls.length, 0)
  assert.equal(runtime.idle(), true)
})

test('an idle store runtime installs the offered package as current and exits for its supervisor', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const exits = []
  const node = await startFakeNode(t, { path: socketPath })
  const v2 = offerOf('v2')
  node.offer = v2
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    log: () => {},
    update: { name: PACKAGE_NAME, hash: 'running-v1', store, exit: (h) => exits.push(h) },
  })
  t.after(() => runtime.stop())
  runtime.start()
  await until('exit called', () => exits.length === 1)
  assert.deepEqual(node.hello.package, { name: PACKAGE_NAME, hash: 'running-v1' })
  assert.equal(exits[0], v2.hash)
  assert.equal(readlinkSync(join(store, 'current')), v2.hash)
})

test('store lock contention retries without blacklisting the offer or blocking timers', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const node = await startFakeNode(t, { path: socketPath })
  const [running, offered] = ['lock-A', 'lock-B'].map((marker) => offerOf(marker))
  const current = verifyPackage(running, PACKAGE_NAME)
  installCurrent(store, current.hash, current.files)
  node.offer = offered
  const lock = join(store, '.install.lock')
  const owner = join(lock, `${process.pid}-0000000000000000`)
  mkdirSync(lock)
  writeFileSync(owner, '')
  const exits = []
  const logs = []
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    log: (msg) => logs.push(msg),
    update: { name: PACKAGE_NAME, hash: running.hash, store, exit: (hash) => exits.push(hash) },
  })
  t.after(() => {
    rmSync(lock, { recursive: true, force: true })
    return runtime.stop()
  })
  runtime.start()
  await until('first package fetch', () => node.packageCalls.length === 1)
  await until('asynchronous lock retries', () => logs.filter((msg) => msg.includes('store is busy')).length >= 2)
  assert.equal(node.packageCalls.length, 1, 'a lock retry reuses the verified package instead of fetching it again')
  assert.equal(readlinkSync(join(store, 'current')), running.hash)
  assert.deepEqual(exits, [])
  rmSync(owner)
  rmdirSync(lock)
  await until('install succeeds after the lock is released', () => readlinkSync(join(store, 'current')) === offered.hash)
  await until('the accepted update exits for its supervisor', () => exits.length === 1)
  assert.equal(exits[0], offered.hash)
})

test('package fetch failures retry without blacklisting the offer', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const node = await startFakeNode(t, { path: socketPath })
  const [running, offered] = ['fetch-A', 'fetch-B'].map((marker) => offerOf(marker))
  const current = verifyPackage(running, PACKAGE_NAME)
  installCurrent(store, current.hash, current.files)
  node.offer = offered
  const push = node.packageCalls.push.bind(node.packageCalls)
  let failFetch = true
  node.packageCalls.push = (...args) => {
    const result = push(...args)
    if (failFetch) {
      node.offer = undefined // The hello offered it, but this first fetch hits a transient node failure.
      failFetch = false
    }
    return result
  }
  const exits = []
  const logs = []
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    log: (msg) => logs.push(msg),
    update: { name: PACKAGE_NAME, hash: running.hash, store, exit: (hash) => exits.push(hash) },
  })
  t.after(() => runtime.stop())
  runtime.start()
  await until('transient package fetch failure', () => logs.some((msg) => msg.includes('package fetch failed')))
  node.offer = offered
  await until('the same offer succeeds after the node recovers', () => readlinkSync(join(store, 'current')) === offered.hash)
  await until('the accepted update exits for its supervisor', () => exits.length === 1)
  assert.ok(node.packageCalls.length >= 2)
  assert.equal(exits[0], offered.hash)
})

test('a newer offer invalidates an older lock retry', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const node = await startFakeNode(t, { path: socketPath })
  const [running, older, newer] = ['retry-A', 'retry-B', 'retry-C'].map((marker) => offerOf(marker))
  const current = verifyPackage(running, PACKAGE_NAME)
  installCurrent(store, current.hash, current.files)
  node.offer = older
  const lock = join(store, '.install.lock')
  mkdirSync(lock)
  writeFileSync(join(lock, `${process.pid}-0000000000000000`), '')
  const logs = []
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 100,
    log: (msg) => logs.push(msg),
    update: { name: PACKAGE_NAME, hash: running.hash, store },
  })
  t.after(() => {
    rmSync(lock, { recursive: true, force: true })
    return runtime.stop()
  })
  runtime.start()
  await until('the older offer is waiting on the lock', () => logs.some((msg) => msg.includes('store is busy')))
  node.offer = newer
  rmSync(lock, { recursive: true, force: true })
  node.peer.close(new Error('a newer node offer arrives'))
  await until('the newer offer installs', () => readlinkSync(join(store, 'current')) === newer.hash)
  await new Promise((resolve) => setTimeout(resolve, 160))
  assert.equal(readlinkSync(join(store, 'current')), newer.hash, 'the older timed retry cannot roll current back')
  assert.equal(node.packageCalls.length, 2, 'the older retry does not need another package fetch')
})

test('a completed hello without an offer cancels the stale lock retry', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const node = await startFakeNode(t, { path: socketPath })
  const [running, offered] = ['hello-A', 'hello-B'].map((marker) => offerOf(marker))
  const current = verifyPackage(running, PACKAGE_NAME)
  installCurrent(store, current.hash, current.files)
  node.offer = offered
  const lock = join(store, '.install.lock')
  mkdirSync(lock)
  writeFileSync(join(lock, `${process.pid}-0000000000000000`), '')
  const logs = []
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 100,
    log: (msg) => logs.push(msg),
    update: { name: PACKAGE_NAME, hash: running.hash, store },
  })
  t.after(() => {
    rmSync(lock, { recursive: true, force: true })
    return runtime.stop()
  })
  runtime.start()
  await until('the stale offer is waiting on the lock', () => logs.some((msg) => msg.includes('store is busy')))
  const hellos = node.hellos
  let answerHello
  node.holdHello = new Promise((resolve) => { answerHello = resolve })
  t.after(() => { answerHello(); node.holdHello = undefined })
  node.offer = running // The replacement node rolled back to A, but its hello is still negotiating.
  rmSync(lock, { recursive: true, force: true })
  node.peer.close(new Error('node rollback'))
  await until('the rollback hello is negotiating', () => node.hellos > hellos)
  await new Promise((resolve) => setTimeout(resolve, 160))
  assert.equal(readlinkSync(join(store, 'current')), running.hash, 'a stale retry cannot publish B while disconnected or negotiating hello')
  node.hello = undefined
  answerHello()
  node.holdHello = undefined
  await until('the rollback hello completed', () => node.hello?.package?.hash === running.hash)
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(readlinkSync(join(store, 'current')), running.hash, 'the completed hello invalidates B instead of publishing it later')
  assert.equal(node.packageCalls.length, 1)
})

test('a companion that stays on A keeps its running version through consecutive installs', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const node = await startFakeNode(t, { path: socketPath })
  const versions = ['running-A', 'B', 'C', 'D', 'E', 'F'].map((marker) => offerOf(`active-${marker}`))
  const running = verifyPackage(versions[0], PACKAGE_NAME)
  installCurrent(store, running.hash, running.files)
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    log: () => {},
    // No exit hook: this is the long-lived process that must keep using A.
    update: { name: PACKAGE_NAME, hash: running.hash, store },
  })
  t.after(() => runtime.stop())
  node.offer = versions[1]
  runtime.start()
  for (const [index, offered] of versions.slice(1).entries()) {
    if (index > 0) {
      node.offer = offered
      node.peer.close(new Error('next consecutive upgrade'))
    }
    await until(`installed upgrade ${index + 2}`, () => {
      try { return readlinkSync(join(store, 'current')) === offered.hash } catch { return false }
    })
  }
  assert.ok(existsSync(join(store, running.hash, 'antigravity', 'mcp-proxy.mjs')), 'the process still running A retains its proxy after A → B → C and later upgrades')
  assert.ok(existsSync(join(store, running.hash, 'antigravity', 'media-guard.mjs')), 'the same retained generation keeps the media guard usable')
})

test('waits for an open permission card and active ACP turn to settle before exiting onto the installed version', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const exits = []
  let answerCard
  const node = await startFakeNode(t, { path: socketPath })
  node.resolve = () => new Promise((r) => { answerCard = r })
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    idleCheckMs: 25,
    log: () => {},
    update: { name: PACKAGE_NAME, hash: 'running-v1', store, exit: (h) => exits.push(h) },
  })
  t.after(() => runtime.stop())
  runtime.start()
  await node.ready

  await node.peer.call('session.start', { cwd: dir, prompt: 'hold-with-card' })
  await until('permission card reaches phone', () => node.asked.length === 1 && typeof answerCard === 'function')
  assert.equal(runtime.idle(), false)

  // Node upgrades and reconnects while both the card and ACP prompt are in flight.
  const v2 = offerOf('v2-while-busy')
  node.offer = v2
  node.peer.close(new Error('node upgraded'))
  await until('new version installed in store', () => {
    try { return readlinkSync(join(store, 'current')) === v2.hash } catch { return false }
  })
  assert.equal(exits.length, 0, 'does not exit while permission card and turn are active')

  // Answering the card on the reconnected node leaves the ACP turn still running until release-turn is written.
  await until('card re-asked on upgraded node', () => node.asked.length >= 2)
  answerCard({ allow: true })
  await until('agy received permission answer', () => existsSync(join(dir, 'perm-answered')))
  assert.equal(runtime.idle(), false)
  assert.equal(exits.length, 0, 'does not exit while ACP turn is still executing')

  writeFileSync(join(dir, 'release-turn'), '1')
  await until('exits once turn finishes', () => exits.length === 1)
  assert.equal(exits[0], v2.hash)
})

test('a valid package whose hash differs from the hello offer is not installed', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const node = await startFakeNode(t, { path: socketPath })
  const [running, offered, replacement] = ['offer-A', 'offer-B', 'offer-C'].map((marker) => offerOf(marker))
  const current = verifyPackage(running, PACKAGE_NAME)
  installCurrent(store, current.hash, current.files)
  node.offer = offered
  const push = node.packageCalls.push.bind(node.packageCalls)
  node.packageCalls.push = (...args) => {
    const result = push(...args)
    node.offer = replacement // A different valid package answers the fetch for B.
    return result
  }
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    log: () => {},
    update: { name: PACKAGE_NAME, hash: running.hash, store },
  })
  t.after(() => runtime.stop())
  runtime.start()
  await until('the replacement package answered', () => node.packageCalls.length === 1)
  await new Promise((resolve) => setTimeout(resolve, 80))
  assert.equal(readlinkSync(join(store, 'current')), running.hash, 'C is not cached or installed for the B offer')
  assert.equal(node.packageCalls.length, 1, 'the mismatched response does not start a retry loop')
})

test('rejects a tampered package without touching current or retrying the bad hash', async (t) => {
  const { dir, binary, agyDir, store, socketPath } = makeEnv(t)
  const exits = []
  const node = await startFakeNode(t, { path: socketPath })
  const v2 = offerOf('tampered')
  node.offer = {
    ...v2,
    files: v2.files.map((f) =>
      f.path === 'antigravity/companion.mjs' ? { ...f, data: Buffer.from('corrupted').toString('base64') } : f,
    ),
  }
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir,
    home: dir,
    socketPath: node.path,
    retryMs: 20,
    log: () => {},
    update: { name: PACKAGE_NAME, hash: 'running-v1', store, exit: (h) => exits.push(h) },
  })
  t.after(() => runtime.stop())
  runtime.start()
  await until('package fetched once', () => node.packageCalls.length === 1)
  const hellosBefore = node.hellos
  node.peer.close(new Error('reconnect'))
  await until('reconnected', () => node.hellos > hellosBefore)
  assert.equal(node.packageCalls.length, 1, 'bad hash is not fetched again')
  assert.throws(() => readlinkSync(join(store, 'current')))
  assert.equal(exits.length, 0)
})

test('install.mjs seeds the store and companion.mjs self-updates unless --no-update is passed', async (t) => {
  const { binary, agyDir } = makeEnv(t)
  const socketDir = mkdtempSync(join(tmpdir(), 'jrt-agy-'))
  t.after(() => rmSync(socketDir, { recursive: true, force: true }))
  const socketPath = join(socketDir, 'runtime.sock')
  const node = await startFakeNode(t, { path: socketPath })
  const v2 = offerOf('v2-companion-e2e')
  node.offer = v2

  const { store, current, hash: v1Hash } = installAntigravityPackage({ socketPath })
  assert.equal(readlinkSync(current), v1Hash)
  const entry = join(current, 'antigravity', 'companion.mjs')

  // With --no-update, the store companion omits hello.package and keeps running.
  const noUpdateProc = spawn(process.execPath, [entry, '--socket', socketPath, '--agy-bin', binary, '--agy-dir', agyDir, '--no-update'], {
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  t.after(() => { try { noUpdateProc.kill('SIGKILL') } catch {} })
  await until('connected with --no-update', () => node.hellos === 1)
  assert.equal(node.hello.package, undefined)
  assert.equal(node.packageCalls.length, 0)
  assert.equal(readlinkSync(current), v1Hash)
  const stoppedNoUpdate = once(noUpdateProc, 'exit')
  noUpdateProc.kill('SIGTERM')
  const [noUpdateCode] = await stoppedNoUpdate
  assert.equal(noUpdateCode, 0)

  // Without --no-update, the store companion installs v2 as current and exits 0; restarting runs v2.
  const updateProc = spawn(process.execPath, [entry, '--socket', socketPath, '--agy-bin', binary, '--agy-dir', agyDir], {
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  t.after(() => { try { updateProc.kill('SIGKILL') } catch {} })
  const [updateCode] = await once(updateProc, 'exit')
  assert.equal(updateCode, 0)
  assert.deepEqual(node.hello.package, { name: PACKAGE_NAME, hash: v1Hash })
  assert.equal(node.packageCalls.length, 1)
  assert.equal(readlinkSync(current), v2.hash)
  assert.ok(existsSync(join(store, v1Hash)), 'previous version stays alongside current')

  const lock = join(store, '.install.lock')
  mkdirSync(lock)
  writeFileSync(join(lock, `${process.pid}-1111111111111111`), '')
  const restartedProc = spawn(process.execPath, [entry, '--socket', socketPath, '--agy-bin', binary, '--agy-dir', agyDir, '--parent-stdin'], {
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  t.after(() => { try { restartedProc.kill('SIGKILL') } catch {} })
  const active = join(store, '.active', v2.hash)
  mkdirSync(active, { recursive: true })
  const nodeReservation = join(active, `${process.pid}-0000000000000000`)
  writeFileSync(nodeReservation, '')
  rmSync(lock, { recursive: true, force: true })
  await until('restarted companion connected on v2', () => node.hellos >= 3 && node.hello?.package?.hash === v2.hash)
  assert.equal(node.packageCalls.length, 1, 'v2 matches node offer so no further package fetch occurs')
  assert.equal(existsSync(nodeReservation), false, 'companion registration releases the node startup reservation')
  const companionLeases = readdirSync(active).filter((name) => name.startsWith(`${restartedProc.pid}-`))
  assert.equal(companionLeases.length, 1, 'the companion keeps its own lease after the handoff')
  const stoppedRestarted = once(restartedProc, 'exit')
  restartedProc.kill('SIGTERM')
  const [restartedCode] = await stoppedRestarted
  assert.equal(restartedCode, 0)
})
