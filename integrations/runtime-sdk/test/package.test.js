import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readdirSync, realpathSync, rmSync, rmdirSync, statSync, symlinkSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { currentHash, installCurrent, packageHash, PACKAGES, readShipped, registerActiveVersion, selfPackage } from '../lib/package.js'

const version = (text) => {
  const files = [{ path: 'pi/index.js', data: Buffer.from(text) }, { path: 'runtime-sdk/index.js', data: Buffer.from('export {}') },
    { path: 'pi/package.json', data: Buffer.from('{"type":"module"}') }]
  return { hash: packageHash(files), files }
}

test('installCurrent points current at the new version, keeps the previous and recent generations, and is idempotent', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-'))
  t.after(() => rmSync(store, { recursive: true, force: true }))
  const [v1, v2, v3] = [version('1'), version('2'), version('3')]
  installCurrent(store, v1.hash, v1.files)
  assert.equal(currentHash(store), v1.hash)
  installCurrent(store, v2.hash, v2.files)
  assert.equal(currentHash(store), v2.hash)
  assert.ok(existsSync(join(store, v1.hash)), 'the version current pointed at before stays (a process may still run it)')
  installCurrent(store, v3.hash, v3.files)
  assert.equal(currentHash(store), v3.hash)
  assert.deepEqual(readdirSync(store).filter((n) => n !== 'current').sort(), [v1.hash, v2.hash, v3.hash].sort())
  const cachedTime = new Date(1000)
  utimesSync(join(store, v3.hash), cachedTime, cachedTime)
  installCurrent(store, v3.hash, v3.files) // the companion and an extension both install it
  assert.ok(statSync(join(store, v3.hash)).mtimeMs > cachedTime.getTime(), 'reusing a cached generation makes it recent again')
  assert.equal(currentHash(store), v3.hash)
  assert.ok(existsSync(join(store, 'current', 'pi', 'index.js')))
})

test('installCurrent preserves fresh staging from another installer and reclaims day-old residue', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-staging-'))
  t.after(() => rmSync(store, { recursive: true, force: true }))
  const [v1, v2] = [version('1'), version('2')]
  installCurrent(store, v1.hash, v1.files)
  const staging = join(store, `.${v1.hash}.01234567.tmp`)
  mkdirSync(staging)
  writeFileSync(join(staging, 'partial'), 'partial')
  const currentTmp = join(store, '.current.01234567.tmp')
  symlinkSync(v1.hash, currentTmp)
  installCurrent(store, v2.hash, v2.files)
  assert.ok(existsSync(join(staging, 'partial')), 'fresh package staging is not another installer\'s garbage')
  assert.ok(existsSync(currentTmp), 'a fresh temporary current symlink is not removed')
  const old = new Date(Date.now() - 25 * 60 * 60 * 1000)
  utimesSync(staging, old, old)
  installCurrent(store, v2.hash, v2.files)
  assert.ok(!existsSync(staging), 'abandoned staging remains bounded')
})

test('installCurrent reports a live lock without blocking and reclaims a reused-PID lock', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-lock-'))
  t.after(() => {
    rmSync(join(store, '.install.lock'), { recursive: true, force: true })
    rmSync(store, { recursive: true, force: true })
  })
  const [v1, v2] = [version('1'), version('2')]
  installCurrent(store, v1.hash, v1.files)
  const lock = join(store, '.install.lock')
  const owner = join(lock, `${process.pid}-0000000000000000`)
  mkdirSync(lock)
  writeFileSync(owner, '')
  assert.throws(() => installCurrent(store, v2.hash, v2.files), { code: 'ERR_RUNTIME_STORE_BUSY' })
  assert.equal(currentHash(store), v1.hash)
  rmSync(owner)
  rmdirSync(lock)
  mkdirSync(lock)
  writeFileSync(owner, 'not-this-process-start')
  installCurrent(store, v2.hash, v2.files)
  assert.equal(currentHash(store), v2.hash)
  assert.ok(!existsSync(lock), 'a matching PID with a different process start identity is stale')
})

test('installCurrent preserves the caller\'s running version through A → B → C and bounds other generations', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-active-'))
  t.after(() => rmSync(store, { recursive: true, force: true }))
  const versions = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H'].map(version)
  for (const [index, v] of versions.entries()) {
    installCurrent(store, v.hash, v.files, { keep: [versions[0].hash] })
    const modified = new Date((index + 1) * 1000)
    utimesSync(join(store, v.hash), modified, modified)
    if (index === 2) assert.ok(existsSync(join(store, versions[0].hash)), 'running A survives A → B → C')
  }
  assert.equal(currentHash(store), versions.at(-1).hash)
  assert.ok(existsSync(join(store, versions[0].hash)), 'the running version survives any number of upgrades')
  assert.deepEqual(
    readdirSync(store).filter((n) => n !== 'current').sort(),
    [versions[0], ...versions.slice(3)].map((v) => v.hash).sort(),
    'current, before, three recent generations and the running version stay; older idle versions are removed',
  )
})

test('active process leases protect every companion generation and reclaim stale or released leases', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-leases-'))
  t.after(() => rmSync(store, { recursive: true, force: true }))
  const stale = version('stale')
  installCurrent(store, stale.hash, stale.files)
  const staleModified = new Date(500)
  utimesSync(join(store, stale.hash), staleModified, staleModified)
  const staleLease = join(store, '.active', stale.hash)
  mkdirSync(staleLease, { recursive: true })
  writeFileSync(join(staleLease, '999999999-0000000000000000'), '')
  writeFileSync(join(staleLease, `${process.pid}-1111111111111111`), 'not-this-process-start')
  const versions = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I'].map(version)
  installCurrent(store, versions[0].hash, versions[0].files)
  utimesSync(join(store, versions[0].hash), new Date(1000), new Date(1000))
  const release = registerActiveVersion(store, versions[0].hash)
  t.after(release)
  for (const [index, v] of versions.slice(1, 8).entries()) {
    installCurrent(store, v.hash, v.files)
    const modified = new Date((index + 2) * 1000)
    utimesSync(join(store, v.hash), modified, modified)
  }
  assert.ok(existsSync(join(store, versions[0].hash)), 'a live process protects A even when another process installs')
  assert.ok(!existsSync(join(store, stale.hash)), 'dead PIDs and reused PIDs with a different start identity do not pin a version')
  release()
  installCurrent(store, versions[8].hash, versions[8].files)
  assert.ok(!existsSync(join(store, '.active', versions[0].hash)), 'locked cleanup removes the empty lease directory')
  assert.ok(!existsSync(join(store, versions[0].hash)), 'a released generation is cleaned once it falls outside recent retention')
})

test('registerActiveVersion refuses a generation removed before registration', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-lease-gone-'))
  t.after(() => rmSync(store, { recursive: true, force: true }))
  const v = version('gone')
  assert.throws(() => registerActiveVersion(store, v.hash), { code: 'ERR_RUNTIME_VERSION_GONE' })
  installCurrent(store, v.hash, v.files)
  const release = registerActiveVersion(store, v.hash)
  release()
  rmSync(join(store, v.hash), { recursive: true, force: true })
  assert.throws(() => registerActiveVersion(store, v.hash), { code: 'ERR_RUNTIME_VERSION_GONE' })
})

test('selfPackage: a module run from a store version (through current) knows its store; from a checkout it has none', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-'))
  const checkout = mkdtempSync(join(tmpdir(), 'jrt-co-'))
  t.after(() => {
    rmSync(store, { recursive: true, force: true })
    rmSync(checkout, { recursive: true, force: true })
  })
  for (const f of ['pi/package.json', 'pi/index.js', 'pi/companion.mjs', 'pi/lib/pi.js', 'runtime-sdk/package.json', 'runtime-sdk/index.js', 'runtime-sdk/lib/runtime.js']) {
    mkdirSync(join(checkout, f, '..'), { recursive: true })
    writeFileSync(join(checkout, f), f)
  }
  const files = readShipped(checkout, PACKAGES['persona-pi-runtime'])
  const hash = packageHash(files)
  const fromCheckout = selfPackage(pathToFileURL(join(checkout, 'pi', 'index.js')).href, 'persona-pi-runtime')
  assert.equal(fromCheckout.hash, hash)
  assert.equal(fromCheckout.store, undefined)
  installCurrent(store, hash, files)
  const fromStore = selfPackage(pathToFileURL(join(store, 'current', 'pi', 'index.js')).href, 'persona-pi-runtime')
  assert.equal(fromStore.hash, hash)
  assert.equal(fromStore.store, realpathSync(store)) // through current, to the version's real directory
})

test('selfPackage keeps conventional store identity when the installed tree vanishes during startup', (t) => {
  const parent = mkdtempSync(join(tmpdir(), 'jrt-pkg-identity-'))
  t.after(() => rmSync(parent, { recursive: true, force: true }))
  const name = 'persona-antigravity-runtime'
  const store = join(parent, name)
  const root = fileURLToPath(new URL('../../', import.meta.url))
  const files = readShipped(root, PACKAGES[name])
  const hash = packageHash(files)
  installCurrent(store, hash, files)
  const url = pathToFileURL(join(store, hash, 'antigravity', 'companion.mjs')).href
  rmSync(join(store, hash), { recursive: true, force: true })
  const identified = selfPackage(url, name)
  assert.equal(identified.store, store)
  assert.notEqual(identified.hash, hash, 'a partial package is not mistaken for its original intact hash')
})

test('persona-antigravity-runtime hashes companion, mcp-proxy, lib and runtime-sdk identically across checkout and store', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-'))
  const checkout = mkdtempSync(join(tmpdir(), 'jrt-co-'))
  t.after(() => {
    rmSync(store, { recursive: true, force: true })
    rmSync(checkout, { recursive: true, force: true })
  })
  for (const f of [
    'antigravity/package.json',
    'antigravity/companion.mjs',
    'antigravity/desktop.mjs',
    'antigravity/mcp-proxy.mjs',
    'antigravity/lib/runtime.js',
    'antigravity/install.mjs',
    'runtime-sdk/package.json',
    'runtime-sdk/index.js',
    'runtime-sdk/lib/runtime.js',
  ]) {
    mkdirSync(join(checkout, f, '..'), { recursive: true })
    writeFileSync(join(checkout, f), f)
  }
  const files = readShipped(checkout, PACKAGES['persona-antigravity-runtime'])
  assert.deepEqual(files.map((f) => f.path), [
    'antigravity/package.json',
    'antigravity/companion.mjs',
    'antigravity/desktop.mjs',
    'antigravity/mcp-proxy.mjs',
    'antigravity/lib/runtime.js',
    'runtime-sdk/package.json',
    'runtime-sdk/index.js',
    'runtime-sdk/lib/runtime.js',
  ])
  const hash = packageHash(files)
  const fromCheckout = selfPackage(pathToFileURL(join(checkout, 'antigravity', 'companion.mjs')).href, 'persona-antigravity-runtime')
  assert.equal(fromCheckout.name, 'persona-antigravity-runtime')
  assert.equal(fromCheckout.hash, hash)
  assert.equal(fromCheckout.store, undefined)
  installCurrent(store, hash, files)
  const fromStore = selfPackage(pathToFileURL(join(store, 'current', 'antigravity', 'companion.mjs')).href, 'persona-antigravity-runtime')
  assert.equal(fromStore.name, 'persona-antigravity-runtime')
  assert.equal(fromStore.hash, hash)
  assert.equal(fromStore.store, realpathSync(store))
})

test('persona-opencode-runtime hashes index, companion, lib and runtime-sdk identically across checkout and store', (t) => {
  const store = mkdtempSync(join(tmpdir(), 'jrt-pkg-'))
  const checkout = mkdtempSync(join(tmpdir(), 'jrt-co-'))
  t.after(() => {
    rmSync(store, { recursive: true, force: true })
    rmSync(checkout, { recursive: true, force: true })
  })
  for (const f of [
    'opencode/package.json',
    'opencode/index.js',
    'opencode/companion.mjs',
    'opencode/lib/companion.js',
    'opencode/install.mjs',
    'runtime-sdk/package.json',
    'runtime-sdk/index.js',
    'runtime-sdk/lib/runtime.js',
  ]) {
    mkdirSync(join(checkout, f, '..'), { recursive: true })
    writeFileSync(join(checkout, f), f)
  }
  const files = readShipped(checkout, PACKAGES['persona-opencode-runtime'])
  assert.deepEqual(files.map((f) => f.path), [
    'opencode/package.json',
    'opencode/index.js',
    'opencode/companion.mjs',
    'opencode/lib/companion.js',
    'runtime-sdk/package.json',
    'runtime-sdk/index.js',
    'runtime-sdk/lib/runtime.js',
  ])
  const hash = packageHash(files)
  const fromCheckout = selfPackage(pathToFileURL(join(checkout, 'opencode', 'companion.mjs')).href, 'persona-opencode-runtime')
  assert.equal(fromCheckout.name, 'persona-opencode-runtime')
  assert.equal(fromCheckout.hash, hash)
  assert.equal(fromCheckout.store, undefined)
  installCurrent(store, hash, files)
  const fromStore = selfPackage(pathToFileURL(join(store, 'current', 'opencode', 'companion.mjs')).href, 'persona-opencode-runtime')
  assert.equal(fromStore.name, 'persona-opencode-runtime')
  assert.equal(fromStore.hash, hash)
  assert.equal(fromStore.store, realpathSync(store))
})
