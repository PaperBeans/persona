// Runtime packages (docs/integrations/runtimes.md "Auto-update"): the code of a runtime's plugin as files with one hash, the same as
// personad's (server/internal/runtimepkg). The loader (loader.js) hashes the copy it ships with, keeps the versions the
// node sends in a store and checks every one before it runs it.

import { addPackageAliases, wirePackageName } from './compat.js'
import { execFileSync } from 'node:child_process'
import { createHash, randomBytes } from 'node:crypto'
import { existsSync, lstatSync, mkdirSync, readdirSync, readFileSync, readlinkSync, realpathSync, renameSync, rmSync, rmdirSync, statSync, symlinkSync, utimesSync, writeFileSync } from 'node:fs'
import { basename, dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * The packages and the files each is made of, relative to the integrations directory (`*` only in the last segment).
 * Must stay the same as personad's runtimepkg.Packages: a file one side counts and the other does not makes every
 * hello look like another version.
 */
export const PACKAGES = addPackageAliases({
  'persona-dsh-runtime': ['dsh/package.json', 'dsh/lib/*.js', 'runtime-sdk/package.json', 'runtime-sdk/index.js', 'runtime-sdk/lib/*.js'],
  'persona-pi-runtime': ['pi/package.json', 'pi/index.js', 'pi/companion.mjs', 'pi/lib/*.js', 'runtime-sdk/package.json', 'runtime-sdk/index.js', 'runtime-sdk/lib/*.js'],
  'persona-antigravity-runtime': ['antigravity/package.json', 'antigravity/companion.mjs', 'antigravity/desktop.mjs', 'antigravity/mcp-proxy.mjs', 'antigravity/media-guard.mjs', 'antigravity/lib/*.js', 'runtime-sdk/package.json', 'runtime-sdk/index.js', 'runtime-sdk/lib/*.js'],
  'persona-opencode-runtime': ['opencode/package.json', 'opencode/index.js', 'opencode/companion.mjs', 'opencode/lib/*.js', 'runtime-sdk/package.json', 'runtime-sdk/index.js', 'runtime-sdk/lib/*.js'],
  'persona-paseo-runtime': ['paseo/package.json', 'paseo/bridge.mjs', 'paseo/lib/*.js', 'runtime-sdk/package.json', 'runtime-sdk/index.js', 'runtime-sdk/lib/*.js'],
})

/** A package name, as personad takes it. */
export const NAME_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/

/** Bounds a package (all its files), as personad does. */
export const MAX_SIZE = 8 << 20

/** A file's path in a package: relative, slash-separated, no empty / . / .. segments. */
export function validPath(p) {
  if (typeof p !== 'string' || p === '' || p.startsWith('/') || p.includes('\\') || p.length > 256) return false
  return p.split('/').every((seg) => seg !== '' && seg !== '.' && seg !== '..')
}

/**
 * A package's hash: sha256 over its files sorted by path, each as "<path>\0<hex sha256 of the content>\n", in hex.
 * @param {{path: string, data: Buffer}[]} files
 */
export function packageHash(files) {
  const h = createHash('sha256')
  for (const f of [...files].sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))) {
    h.update(`${f.path}\0${createHash('sha256').update(f.data).digest('hex')}\n`)
  }
  return h.digest('hex')
}

/**
 * The files of a package in an integrations directory, by its patterns (what the loader ships with).
 * @param {string} root - the integrations directory (where dsh/ and runtime-sdk/ are).
 * @param {string[]} patterns
 */
export function readShipped(root, patterns) {
  const files = []
  for (const pattern of patterns) {
    const at = pattern.lastIndexOf('/')
    const dir = at < 0 ? '' : pattern.slice(0, at)
    const base = pattern.slice(at + 1)
    if (!base.includes('*')) {
      const path = join(root, pattern)
      if (isFile(path)) files.push({ path: pattern, data: readFileSync(path) })
      continue
    }
    const [prefix, suffix] = base.split('*')
    let names = []
    try {
      names = readdirSync(join(root, dir))
    } catch {
      continue
    }
    for (const n of names.sort()) {
      if (!n.startsWith(prefix) || !n.endsWith(suffix) || n.length < prefix.length + suffix.length) continue
      const path = join(root, dir, n)
      if (isFile(path)) files.push({ path: dir ? `${dir}/${n}` : n, data: readFileSync(path) })
    }
  }
  return files
}

/** Every regular file under dir (a stored version), with slash-separated relative paths. */
export function readTree(dir) {
  const files = []
  const walk = (d) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      const path = join(d, e.name)
      if (e.isDirectory()) walk(path)
      else if (e.isFile()) files.push({ path: relative(dir, path).split(sep).join('/'), data: readFileSync(path) })
    }
  }
  walk(dir)
  return files
}

function isFile(path) {
  try {
    return statSync(path).isFile()
  } catch {
    return false
  }
}

/**
 * The files of a package the node sent (runtime.package's answer), checked: name, paths, size, and that they hash to
 * what it says. Throws on anything off.
 * @param {{name: string, hash: string, files: {path: string, data: string}[]}} pkg
 * @param {string} name - the package asked for.
 */
export function verifyPackage(pkg, name) {
  if (!pkg || pkg.name !== name) throw new Error(`the node sent package ${pkg?.name}, not ${name}`)
  if (!Array.isArray(pkg.files) || pkg.files.length === 0) throw new Error(`package ${name} has no files`)
  const files = []
  const seen = new Set()
  let size = 0
  for (const f of pkg.files) {
    if (!validPath(f?.path) || seen.has(f.path) || typeof f.data !== 'string') throw new Error(`package ${name}: bad file ${JSON.stringify(f?.path)}`)
    seen.add(f.path)
    const data = Buffer.from(f.data, 'base64')
    if ((size += data.length) > MAX_SIZE) throw new Error(`package ${name} is over ${MAX_SIZE} bytes`)
    files.push({ path: f.path, data })
  }
  const hash = packageHash(files)
  if (hash !== pkg.hash) throw new Error(`package ${name}: its files hash to ${hash.slice(0, 12)}, not ${String(pkg.hash).slice(0, 12)}`)
  return { hash, files }
}

/**
 * Put a checked package in the store as <store>/<hash>/ (written aside, then renamed into place: never half a version).
 * @returns {string} its directory.
 */
export function storeVersion(store, hash, files) {
  const dir = join(store, hash)
  if (existsSync(dir)) {
    try {
      if (packageHash(readTree(dir)) === hash) return dir
    } catch {
      // unreadable: written again below
    }
    rmSync(dir, { recursive: true, force: true })
  }
  mkdirSync(store, { recursive: true, mode: 0o700 })
  const tmp = join(store, `.${hash}.${randomBytes(4).toString('hex')}.tmp`)
  try {
    for (const f of files) {
      const path = join(tmp, ...f.path.split('/'))
      mkdirSync(join(path, '..'), { recursive: true })
      writeFileSync(path, f.data, { mode: 0o644 })
    }
    try {
      renameSync(tmp, dir)
    } catch (err) {
      // Another process stored the same version meanwhile (pi-web's extension and the companion both install it).
      if (!existsSync(dir) || packageHash(readTree(dir)) !== hash) throw err
    }
  } finally {
    rmSync(tmp, { recursive: true, force: true })
  }
  return dir
}

const HASH_DIR = /^[0-9a-f]{64}$/
const RECENT_VERSIONS = 3

/** The version <store>/current points at (installCurrent), or ''. */
export function currentHash(store) {
  try {
    const target = readlinkSync(join(store, 'current'))
    return HASH_DIR.test(target) ? target : ''
  } catch {
    return ''
  }
}

const ACTIVE_DIR = '.active'
const ACTIVE_MARKER = /^(\d+)-[0-9a-f]{16}$/

function processAlive(pid) {
  if (!Number.isSafeInteger(pid) || pid <= 0) return false
  try {
    process.kill(pid, 0)
    return true
  } catch (err) {
    return err.code === 'EPERM' // The process exists but belongs to another user.
  }
}

/** A PID plus its OS start identity, so a reused PID cannot inherit a dead companion's lease. */
function processStartIdentity(pid) {
  if (!Number.isSafeInteger(pid) || pid <= 0) return ''
  try {
    if (process.platform === 'linux') {
      const stat = readFileSync(`/proc/${pid}/stat`, 'utf8')
      return stat.slice(stat.lastIndexOf(') ') + 2).split(' ')[19] || ''
    }
    const args = process.platform === 'win32'
      ? ['-NoProfile', '-NonInteractive', '-Command', `(Get-Process -Id ${pid}).StartTime.ToUniversalTime().ToString("o")`]
      : ['-o', 'lstart=', '-p', String(pid)]
    const command = process.platform === 'win32' ? 'powershell.exe' : 'ps'
    return execFileSync(command, args, { encoding: 'utf8', timeout: 1000, stdio: ['ignore', 'pipe', 'ignore'], env: { ...process.env, TZ: 'UTC', LC_ALL: 'C' } }).trim().replace(/\s+/g, ' ')
  } catch {
    return ''
  }
}

let ownStartIdentity = ''
function ownProcessStartIdentity() {
  return ownStartIdentity ||= processStartIdentity(process.pid)
}

function processMatchesLease(pid, path) {
  if (!processAlive(pid)) return false
  let recorded = ''
  try { recorded = readFileSync(path, 'utf8').trim() } catch { return true } // An unreadable marker is not safe to reclaim.
  if (!recorded) return true // Compatibility with the first PID-only lease format.
  const current = processStartIdentity(pid)
  return current === '' || current === recorded // Unknown OS details fail closed; a proved mismatch is stale.
}

/** Live versions registered by registerActiveVersion; stale process leases are removed as cleanup runs. */
function activeVersions(store) {
  const active = new Set()
  const root = join(store, ACTIVE_DIR)
  let hashes
  try {
    hashes = readdirSync(root)
  } catch (err) {
    if (err.code === 'ENOENT') return active
    throw err
  }
  for (const hash of hashes) {
    if (!HASH_DIR.test(hash)) continue
    const dir = join(root, hash)
    for (const marker of readdirSync(dir)) {
      const match = ACTIVE_MARKER.exec(marker)
      if (!match || processMatchesLease(Number(match[1]), join(dir, marker))) {
        active.add(hash) // An unknown newer marker format is safer to retain than to remove.
        continue
      }
      rmSync(join(dir, marker), { force: true })
    }
    try { rmdirSync(dir) } catch {} // Nonempty while another process is registered.
  }
  return active
}

const STORE_LOCK = '.install.lock'
const EMPTY_LOCK_STALE_MS = 30_000
const TEMP_STALE_MS = 24 * 60 * 60 * 1000

function storeBusy() {
  const err = new Error('the runtime package store is busy; retry the installation')
  err.code = 'ERR_RUNTIME_STORE_BUSY'
  return err
}

/** Nonblocking atomic lock with owner verification and fencing before destructive operations. */
function acquireStoreLock(store) {
  mkdirSync(store, { recursive: true, mode: 0o700 })
  const lock = join(store, STORE_LOCK)
  const owner = `${process.pid}-${randomBytes(8).toString('hex')}`
  const ownerPath = join(lock, owner)
  for (let attempt = 0; attempt < 8; attempt++) {
    let created = false
    try {
      mkdirSync(lock, { mode: 0o700 })
      created = true
    } catch (err) {
      if (err.code !== 'EEXIST') throw err
    }
    if (created) {
      try {
        writeFileSync(ownerPath, ownProcessStartIdentity(), { mode: 0o600 })
        const owners = readdirSync(lock)
        if (owners.length !== 1 || owners[0] !== owner) throw storeBusy()
      } catch (err) {
        rmSync(ownerPath, { force: true })
        try { rmdirSync(lock) } catch {}
        if (err.code === 'ENOENT' || err.code === 'ERR_RUNTIME_STORE_BUSY') throw storeBusy()
        throw err
      }
      const check = () => {
        let owners
        try { owners = readdirSync(lock) } catch { throw storeBusy() }
        if (owners.length !== 1 || owners[0] !== owner) throw storeBusy()
      }
      const release = () => {
        rmSync(ownerPath, { force: true })
        try { rmdirSync(lock) } catch {}
      }
      return { check, release }
    }
    let reclaimed = false
    try {
      const owners = readdirSync(lock)
      if (owners.length === 0) {
        // The mkdir/write window is indistinguishable from a crash until it has had time to finish.
        if (Date.now() - statSync(lock).mtimeMs > EMPTY_LOCK_STALE_MS) {
          rmdirSync(lock)
          reclaimed = true
        }
      } else {
        const stale = owners.every((name) => {
          const match = ACTIVE_MARKER.exec(name)
          return match && !processMatchesLease(Number(match[1]), join(lock, name))
        })
        if (stale) {
          for (const name of owners) rmSync(join(lock, name), { force: true })
          // A new owner makes this fail. Acquirers also verify sole ownership after publishing.
          rmdirSync(lock)
          reclaimed = true
        }
      }
    } catch (err) {
      if (err.code === 'ENOENT') reclaimed = true
      else if (err.code !== 'ENOTEMPTY') throw err
    }
    if (!reclaimed) throw storeBusy()
  }
  throw storeBusy()
}

function withStoreLock(store, fn) {
  const { check, release } = acquireStoreLock(store)
  try { return fn(check) } finally { release() }
}

/**
 * Register a store version as used by this process. The lease is removed by the returned idempotent function or on
 * process exit; installCurrent probes the PID to reclaim leases left by a crashed process.
 * @returns {() => void} release this process's lease.
 */
export function registerActiveVersion(store, hash) {
  if (!HASH_DIR.test(hash)) throw new Error('a registered runtime version must be a package hash')
  return withStoreLock(store, (check) => {
    if (!existsSync(join(store, hash))) {
      const err = new Error('the running runtime version was removed; restart from current')
      err.code = 'ERR_RUNTIME_VERSION_GONE'
      throw err
    }
    activeVersions(store) // Also scavenges stale built-in leases, whose store never self-updates.
    const dir = join(store, ACTIVE_DIR, hash)
    mkdirSync(dir, { recursive: true, mode: 0o700 })
    const marker = join(dir, `${process.pid}-${randomBytes(8).toString('hex')}`)
    writeFileSync(marker, ownProcessStartIdentity(), { mode: 0o600 })
    check()
    if (!existsSync(join(store, hash))) {
      rmSync(marker, { force: true })
      const err = new Error('the running runtime version was removed; restart from current')
      err.code = 'ERR_RUNTIME_VERSION_GONE'
      throw err
    }
    let registered = true
    const release = () => {
      if (!registered) return
      registered = false
      process.removeListener('exit', release)
      rmSync(marker, { force: true })
      // Only the locked scavenger removes the directory, never a racing release.
    }
    process.once('exit', release)
    return release
  })
}

/**
 * Store a checked package and point <store>/current at it (a symlink replaced by a rename: whoever reads `current`
 * sees the old version or the new one, never neither). For a runtime whose host loads it from a fixed path
 * (pi's settings name <store>/current/pi). Keeps the current and previous versions, hashes still used by running
 * callers, and the three most recently installed other versions. Older idle versions and temporary files are removed.
 * @param {{keep?: Iterable<string>}} [options] - keep: package hashes that running processes still use.
 * @returns {string} the version's directory.
 */
export function installCurrent(store, hash, files, { keep = [] } = {}) {
  return withStoreLock(store, (check) => {
    const dir = storeVersion(store, hash, files)
    check()
    utimesSync(dir, new Date(), new Date()) // Reusing a cached generation makes it recent again (including a rollback).
    const before = currentHash(store)
    if (before !== hash) {
      check()
      const tmp = join(store, `.current.${randomBytes(4).toString('hex')}.tmp`)
      try {
        symlinkSync(hash, tmp)
        renameSync(tmp, join(store, 'current'))
      } finally {
        rmSync(tmp, { force: true })
      }
    }
    const entries = readdirSync(store)
    const preserve = new Set([hash, before, currentHash(store)])
    for (const active of keep) if (HASH_DIR.test(active)) preserve.add(active)
    try {
      for (const active of activeVersions(store)) preserve.add(active)
    } catch {
      for (const name of entries) if (HASH_DIR.test(name)) preserve.add(name) // No cleanup without readable leases.
    }
    const versions = []
    for (const name of entries) {
      if (!HASH_DIR.test(name) || preserve.has(name)) continue
      try {
        versions.push({ name, modified: statSync(join(store, name)).mtimeMs })
      } catch {
        preserve.add(name) // A version that cannot be inspected is not safe to remove.
      }
    }
    versions.sort((a, b) => b.modified - a.modified || (a.name < b.name ? 1 : a.name > b.name ? -1 : 0))
    for (const { name } of versions.slice(0, RECENT_VERSIONS)) preserve.add(name)
    for (const name of entries) {
      if (HASH_DIR.test(name) && !preserve.has(name)) {
        check()
        if (currentHash(store) === name) continue // Also tolerate a current change by an older, unlocked installer.
        rmSync(join(store, name), { recursive: true, force: true })
      } else if (name.startsWith('.') && name.endsWith('.tmp')) {
        try {
          // Cooperating installers are locked out; age also avoids deleting a legacy installer's live staging.
          if (Date.now() - lstatSync(join(store, name)).mtimeMs > TEMP_STALE_MS) {
            check()
            rmSync(join(store, name), { recursive: true, force: true })
          }
        } catch (err) {
          if (err.code === 'ERR_RUNTIME_STORE_BUSY') throw err
        }
      }
    }
    return dir
  })
}

/**
 * The package a module of it runs from: the directory holding its dirs (dsh/, pi/, runtime-sdk/), its hash, and —
 * when that directory is a version in a store (<store>/<hash>/, reached through <store>/current or not) — the store,
 * so the runtime may install the node's version there.
 * @param {string} url - import.meta.url of a module at <root>/<dir>/<file> (depth: how many dirs below root).
 * @param {string} name - the package (PACKAGES).
 * @param {number} [depth] - 1 for <root>/pi/index.js, 2 for <root>/pi/lib/x.js.
 * @returns {{ name: string, root: string, hash: string, store?: string }}
 */
export function selfPackage(url, name, depth = 1) {
  name = wirePackageName(name)
  let root = dirname(fileURLToPath(url))
  for (let i = 0; i < depth; i++) root = dirname(root)
  try {
    root = realpathSync(root)
  } catch {
    // as given
  }
  const hash = packageHash(readShipped(root, PACKAGES[name] ?? []))
  const rootHash = basename(root)
  const parent = dirname(root)
  const storeName = basename(parent)
  // The conventional store name preserves identity if an installed tree is pruned during startup. Content verification
  // still recognizes custom stores, while an ordinary hash-named checkout is not enough on its own.
  const inStore = HASH_DIR.test(rootHash) && (rootHash === hash || storeName === name || storeName === `${name}-builtin`)
  return { name, root, hash, ...(inStore ? { store: parent } : {}) }
}

/** Write a small JSON file atomically. */
export function writeJSON(path, value) {
  const tmp = `${path}.${randomBytes(4).toString('hex')}.tmp`
  writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 })
  renameSync(tmp, path)
}
