// Optional backend integration check for issue #5485. DSH_API_ROOT must point to
// an isolated installed/built package root with the DSH backend packages resolvable.
//   DSH_API_ROOT=<package root> node integrations/dsh/e2e/workspace.mjs
// Uses real DSH registry/controller/session/storage and Persona socket RPC. The
// Agent factory and node peer are fixtures; no personad, browser or model runs.

import assert from 'node:assert/strict'
import { mkdir, mkdtemp, realpath, symlink, readFile, rm, stat } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { createServer } from 'node:net'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

if (!process.env.DSH_API_ROOT) {
  console.error('set DSH_API_ROOT to an isolated installed/built DSH backend package root')
  process.exit(2)
}
const requireDsh = createRequire(join(resolve(process.env.DSH_API_ROOT), 'package.json'))
const loadDsh = name => import(pathToFileURL(requireDsh.resolve(`@deepseek-ai/${name}`)))
const keep = process.env.KEEP === '1'
const previousEnv = { HOME: process.env.HOME, DSH_HOME: process.env.DSH_HOME }
let stateRoot, ctx, domain, backend, runtime, server
const peers = new Set()
const failures = []
const checks = []
let factoryCalls = 0
function check(name, result) {
  assert.ok(result, name)
  checks.push(name)
  console.log(`PASS ${name}`)
}
async function deadline(operation, name) {
  let timer
  try {
    return await Promise.race([operation, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${name} timed out after 10 s`)), 10_000)
    })])
  } finally { clearTimeout(timer) }
}
try {
  // Short socket path on macOS; every writable fixture belongs to this directory.
  stateRoot = await mkdtemp('/tmp/persona-dsh-workspace-')
  console.log(`Fixture: ${stateRoot}`)
  process.env.HOME = join(stateRoot, 'home')
  process.env.DSH_HOME = join(stateRoot, 'dsh')
  for (const path of [process.env.HOME, process.env.DSH_HOME]) await mkdir(path)
  // DSH may capture home paths at module load time; import only after isolation.
  const [
    { Context }, { default: Storage }, { DomainFacility }, { JsonStorageBackend },
    { default: SessionStore }, { default: SessionProjectionRegistry },
    { default: TypertRegistry }, { default: WorkspaceRegistry }, { default: SessionController },
    { createDshRuntime }, { Peer },
  ] = await deadline(Promise.all([
    loadDsh('cordis'), loadDsh('dsh-storage'), loadDsh('dsh-storage-domain'), loadDsh('dsh-storage-json'),
    loadDsh('dsh-session'), loadDsh('dsh-session-projection'), loadDsh('dsh-typert-registry'),
    loadDsh('dsh-workspace'), loadDsh('dsh-api-session-controller'),
    import(new URL('../lib/dsh.js', import.meta.url)),
    import(new URL('../../runtime-sdk/lib/wire.js', import.meta.url)),
  ]), 'module loading')
  const root = join(stateRoot, 'project')
  const child = join(root, 'child')
  const other = join(stateRoot, 'other')
  const alias = join(stateRoot, 'alias')
  for (const path of [child, other]) await mkdir(path, { recursive: true })
  await symlink(root, alias)
  ctx = new Context()
  backend = new JsonStorageBackend(join(process.env.DSH_HOME, 'storages'))
  const agents = new Map()
  await deadline(ctx.plugin(Storage), 'DSH service activation')
  ctx.storage.backend.register('json', backend)
  domain = new DomainFacility(ctx, { backend: 'json', routes: {} })
  ctx.storage.mount('domain', domain)
  ctx.provide('storageDomain', domain)
  await deadline(ctx.plugin(SessionStore), 'DSH service activation')
  await deadline(ctx.plugin(SessionProjectionRegistry), 'DSH service activation')
  await deadline(ctx.plugin(TypertRegistry), 'DSH service activation')
  ctx.provide('sessionPersistence', { list: async () => [] })
  await deadline(ctx.plugin(WorkspaceRegistry), 'DSH service activation')
  // The Agent factory performs no model turns. Session creation, target
  // validation, canonical lookup, attachment and JSON persistence are real DSH.
  ctx.provide('agents', {
    get: id => agents.get(id), list: () => [...agents.values()],
    create: async ({ sessionId, meta }) => {
      factoryCalls++
      const session = ctx.sessions.create(sessionId, { meta })
      const agent = { id: sessionId, session, ctx, status: 'idle' }
      agents.set(sessionId, agent)
      return { agent }
    },
  })
  ctx.provide('agentDefaultModel', { currentSelection: () => ({ provider: 'fixture', model: 'never-called' }) })
  ctx.provide('attachments', { imageLimits: { maxImagesPerMessage: 1, maxImageBytes: 1024 } })
  ctx.provide('llm', {})
  ctx.provide('sessionQuery', { listSessions: async () => ctx.sessions.list().map(session => ({ header: session.header })) })
  await deadline(ctx.plugin(SessionController, {}), 'DSH service activation')
  const workspace = await ctx.workspaceRegistry.create(root, 'Registered project')
  check('canonical registry lookup matches exact root', (await ctx.workspaceRegistry.resolveByPath(root)).id === workspace.id)
  check('canonical registry lookup follows symlink', (await ctx.workspaceRegistry.resolveByPath(alias)).id === workspace.id)
  check('child directory does not match parent workspace', await ctx.workspaceRegistry.resolveByPath(child) === undefined)
  check('unregistered directory returns undefined', await ctx.workspaceRegistry.resolveByPath(other) === undefined)
  await assert.rejects(ctx.workspaceRegistry.resolveByPath(join(stateRoot, 'missing')), { code: 'ENOENT' })
  check('missing path rejects instead of inventing a workspace', true)
  const unattached = await ctx.sessionController.create({ cwd: root })
  check('original cwd-only create reproduces missing workspace membership', !workspace.sessionIds.includes(unattached.sessionId))
  const attached = await ctx.sessionController.create({ workspaceId: workspace.id })
  check('workspaceId-only create attaches actual session', workspace.sessionIds.includes(attached.sessionId))
  check('attached session records canonical workspace cwd', ctx.sessions.get(attached.sessionId).header.cwd === await realpath(root))
  await assert.rejects(ctx.sessionController.create({ cwd: root, workspaceId: workspace.id }), { code: 'gateway/bad-request' })
  check('controller rejects simultaneous workspaceId and cwd', true)
  const connected = Promise.withResolvers()
  server = createServer(socket => {
    const peer = new Peer(socket, {
      onRequest(method) {
        if (method === 'runtime.hello') {
          connected.resolve(peer)
          return { protocol: 1, node_version: 'fixture', hub: true, features: [] }
        }
        if (method === 'mcp.tools') return { tools: [] }
        throw new Error(`unexpected fixture request: ${method}`)
      },
    })
    peers.add(peer)
  })
  const socketPath = join(stateRoot, 'runtime.sock')
  await deadline(new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(socketPath, resolve)
  }), 'socket listen')
  runtime = createDshRuntime({
    controller: ctx.sessionController,
    workspace: ctx.workspaceRegistry,
    agents: ctx.agents,
    on: (...args) => ctx.on(...args),
    hasLiveClient: () => true,
  }, { socketPath, phoneTools: false, retryMs: 20 })
  runtime.start()
  const peer = await deadline(connected.promise, 'runtime hello')
  const call = params => deadline(peer.call('session.start', params), 'session.start')
  const rpcSessionIds = []
  for (const [name, cwd] of [['exact', root], ['symlink', alias], ['normalized', `${root}/child/..`], ['trailing slash', `${root}/`]]) {
    const { session } = await call({ cwd })
    rpcSessionIds.push(session.session_id)
    check(`Persona RPC ${name} session is attached to registered workspace`, workspace.sessionIds.includes(session.session_id))
    check(`Persona RPC ${name} keeps canonical persisted cwd`, ctx.sessions.get(session.session_id).header.cwd === await realpath(root))
  }
  for (const [name, cwd] of [['child', child], ['unmatched', other]]) {
    const { session } = await call({ cwd })
    check(`Persona RPC ${name} session stays outside parent workspace`, !workspace.sessionIds.includes(session.session_id))
    check(`Persona RPC ${name} preserves requested cwd`, ctx.sessions.get(session.session_id).header.cwd === cwd)
  }
  const newDirectory = join(stateRoot, 'new-project')
  await assert.rejects(stat(newDirectory), { code: 'ENOENT' })
  check('new directory is absent immediately before Persona RPC', true)
  const { session: createdDirectorySession } = await call({ cwd: newDirectory })
  check('Persona RPC preserves DSH new-directory creation', (await stat(newDirectory)).isDirectory())
  check('Persona RPC new-directory session stays ungrouped', !workspace.sessionIds.includes(createdDirectorySession.session_id))
  check('Persona RPC new-directory session preserves requested cwd', ctx.sessions.get(createdDirectorySession.session_id).header.cwd === newDirectory)
  const loop = join(stateRoot, 'symlink-loop')
  await symlink(loop, loop)
  await assert.rejects(ctx.workspaceRegistry.resolveByPath(loop), { code: 'ELOOP' })
  const before = factoryCalls
  await assert.rejects(call({ cwd: loop }), /ELOOP/)
  check('Persona RPC non-ENOENT lookup failure creates no partial session', factoryCalls === before)
  check('Persona RPC creates no new workspace registrations', ctx.workspaceRegistry.list().length === 1)
  const stored = JSON.parse(await readFile(join(process.env.DSH_HOME, 'storages/workspace.json'), 'utf8'))
  check('DSH JSON storage durably contains every Persona matched session', rpcSessionIds.every(id => stored.tables.workspaces[workspace.id].sessionIds.includes(id)))
  check('DSH JSON storage still excludes original cwd-only session', !stored.tables.workspaces[workspace.id].sessionIds.includes(unattached.sessionId))
} catch (error) {
  failures.push(error)
} finally {
  // Try every cleanup even if a preceding shutdown fails. Stop reconnects before
  // closing peers and wait for the server before deleting its socket directory.
  for (const [name, cleanup] of [
    ['runtime stop', () => runtime?.stop()],
    ['socket close', async () => {
      for (const peer of peers) peer.close()
      await Promise.all([...peers].map(peer => peer.done))
      if (server?.listening) await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
    }],
    ['context dispose', () => ctx?.fiber.dispose()],
    ['domain close', () => domain?.closeAll()],
    ['backend close', () => backend?.close()],
    ['fixture cleanup', async () => {
      if (stateRoot && !keep) await rm(stateRoot, { recursive: true, force: true })
    }],
  ]) {
    try { await deadline(Promise.resolve().then(cleanup), name) }
    catch (error) { failures.push(error) }
  }
  for (const [name, value] of Object.entries(previousEnv)) {
    if (value === undefined) delete process.env[name]
    else process.env[name] = value
  }
}
if (failures.length) {
  for (const error of failures) console.error(error)
  console.error(`FAIL: ${checks.length} checks passed; ${failures.length} failure(s)`)
  process.exitCode = 1
} else {
  console.log(`PASS: ${checks.length} checks; ${factoryCalls} fixture agents; ${keep ? 'fixture retained at ' + stateRoot : 'fixture removed'}`)
}
