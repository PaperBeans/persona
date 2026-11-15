// The loader of a runtime that lives inside another program (docs/integrations/runtimes.md "Auto-update"): DSH loads the plugin's
// index.js once and keeps it, so that file stays thin and the runtime itself is imported from a versioned directory
// of the store, <store>/<hash>/ — a copy of the files it ships with, or the package the node sent. Never from the
// shipped files themselves: the host's module cache keeps the first import of a path for good, so the old code would
// come back when the plugin is reloaded in the same process after its files changed. When the node offers its package (runtime.hello's
// update), the loader fetches it, checks it, waits until no card is out on the phone, stops the running version,
// imports the new one (a new path: new code, whatever the host caches) and starts it; if that fails it goes back to the
// version it ran. No plugin manager and no hot reload of the host's are involved.
//
// The implementation module exports `api` (LOADER_API) and `start(host)`; host has the loader's `context`, plus
// `package: {name, hash}` and `onUpdate(offer)` for the runtime SDK (createRuntime's options of the same names) and
// `log`. start returns `{stop(), busy?(), fetchPackage?(name)}` — createRuntime's stop, busy and fetchPackage.

import { wirePackageName } from './compat.js'
import { readdirSync, readFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { PACKAGES, packageHash, readShipped, readTree, storeVersion, verifyPackage, writeJSON } from './package.js'

/** The loader ↔ implementation contract's version: an implementation of another is not started. */
export const LOADER_API = 1

const HASH_DIR = /^[0-9a-f]{64}$/

/**
 * @param {{ name: string, root: string, entry: string, store?: string, autoUpdate?: boolean, context?: object,
 *   log?: (msg: string) => void, idleEveryMs?: number, maxWaitMs?: number, patterns?: string[] }} opts - name: the
 *   package; patterns: its files (default package.js PACKAGES[name], which must match personad's list); root: the
 *   integrations directory the loader ships in; entry: the implementation's path in a
 *   package ("dsh/lib/plugin.js"); store: where the versions are kept (none: the shipped files run in place, and no
 *   update is taken);
 *   autoUpdate: take the node's offers (false: report the package, never switch); idleEveryMs / maxWaitMs: how often
 *   to look whether the phone still holds a card, and how long at most to wait for it before switching anyway.
 */
export function createLoader(opts) {
  const { root, entry, store, context = {}, log = () => {} } = opts
  const name = wirePackageName(opts.name)
  const patterns = opts.patterns ?? PACKAGES[name]
  if (!patterns) throw new Error(`createLoader: unknown package ${name}`)
  const autoUpdate = opts.autoUpdate !== false && typeof store === 'string' && store !== ''
  const idleEveryMs = opts.idleEveryMs ?? 5000
  const maxWaitMs = opts.maxWaitMs ?? 2 * 60 * 60 * 1000
  const bad = new Set() // hashes that failed here: not tried again while this process runs
  let shippedHash = ''
  let current // { hash, dir, shipped, instance }
  let starting
  let switching = false
  let pending // an offer that came before the first version finished starting
  let stopped = false
  let stops = 0 // bumped by stop(): a switch under way when it is called starts nothing after it

  const short = (h) => String(h ?? '').slice(0, 12) || 'none'

  /** The version to start: the one the node sent last, unless a copy installed since (by hand) replaced the shipped files. */
  function choose(shipped) {
    if (!store) return shipped
    let cur
    try {
      cur = JSON.parse(readFileSync(join(store, 'current.json'), 'utf8'))
    } catch {
      return shipped
    }
    if (typeof cur?.hash !== 'string' || !HASH_DIR.test(cur.hash) || cur.shipped !== shipped.hash || bad.has(cur.hash)) return shipped
    const dir = join(store, cur.hash)
    try {
      if (packageHash(readTree(dir)) !== cur.hash) return shipped
    } catch {
      return shipped
    }
    return { hash: cur.hash, dir, shipped: false }
  }

  async function run(version) {
    const url = pathToFileURL(join(version.dir, ...entry.split('/'))).href
    const mod = await import(url)
    if (mod.api !== LOADER_API || typeof mod.start !== 'function') throw new Error(`${url} is not an implementation for loader api ${LOADER_API}`)
    const instance = await mod.start({
      ...context,
      package: { name, hash: version.hash },
      onUpdate: autoUpdate ? (offer) => void update(offer) : undefined,
      log,
    })
    return { ...version, instance }
  }

  async function stopInstance(v) {
    try {
      await v?.instance?.stop?.()
    } catch (err) {
      log(`persona: stopping ${name} ${short(v?.hash)}: ${err.message}`)
    }
  }

  async function begin() {
    const files = readShipped(root, patterns)
    if (files.length === 0) throw new Error(`${name}: no files under ${root}`)
    shippedHash = packageHash(files)
    let dir = root
    if (store) {
      try {
        dir = storeVersion(store, shippedHash, files)
      } catch (err) {
        log(`persona: ${name}: cannot keep the shipped version in ${store} (${err.message}); running it in place`)
      }
    }
    const shipped = { hash: shippedHash, dir, shipped: true }
    const chosen = choose(shipped)
    try {
      current = await run(chosen)
    } catch (err) {
      if (chosen.shipped) throw err
      log(`persona: ${name} ${short(chosen.hash)} from the node failed to start (${err.message}); running the shipped ${short(shipped.hash)}`)
      bad.add(chosen.hash)
      current = await run(shipped)
    }
    log(`persona: ${name} ${short(current.hash)} (${current.shipped ? 'shipped' : 'from the node'})`)
    if (stopped) {
      await stopInstance(current)
      return
    }
    if (pending) {
      const offer = pending
      pending = undefined
      void update(offer)
    }
  }

  /** Remove stored versions other than keep (and leftovers of interrupted writes). */
  function prune(keep) {
    let names = []
    try {
      names = readdirSync(store)
    } catch {
      return
    }
    for (const n of names) {
      if ((HASH_DIR.test(n) && !keep.includes(n)) || (n.startsWith('.') && n.endsWith('.tmp'))) {
        rmSync(join(store, n), { recursive: true, force: true })
      }
    }
  }

  async function update(offer) {
    if (!autoUpdate || stopped || offer?.name !== name || typeof offer.hash !== 'string' || bad.has(offer.hash)) return
    if (!current) {
      pending = offer // the hello answered before start() returned: once it has
      return
    }
    if (switching || offer.hash === current.hash) return
    switching = true
    const mine = stops
    const gone = () => stopped || stops !== mine // stop() came (and maybe start() again) while this switch awaited
    const old = current
    let oldStopped = false
    let next
    let target = offer.hash
    try {
      if (typeof old.instance.fetchPackage !== 'function') throw new Error('the running version cannot fetch packages')
      const { hash, files } = verifyPackage(await old.instance.fetchPackage(name), name)
      target = hash
      if (hash === old.hash || bad.has(hash)) return
      const dir = storeVersion(store, hash, files)
      log(`persona: ${name} ${short(hash)} from the node is stored; switching from ${short(old.hash)} once no card waits on the phone`)
      const since = Date.now()
      while (!gone() && current === old && old.instance.busy?.() && Date.now() - since < maxWaitMs) {
        await new Promise((r) => setTimeout(r, idleEveryMs).unref?.())
      }
      if (gone() || current !== old) return
      await stopInstance(old)
      oldStopped = true
      if (gone()) return
      next = await run({ hash, dir, shipped: false })
      if (gone()) {
        // Stopped while it started: stop() has already stopped what it found running (the old version).
        await stopInstance(next)
        return
      }
      current = next
      writeJSON(join(store, 'current.json'), { hash, shipped: shippedHash, version: typeof offer.version === 'string' ? offer.version : '', at: new Date().toISOString() })
      try {
        prune([hash, old.hash, shippedHash])
      } catch (err) {
        log(`persona: ${name} cleanup after switching to ${short(hash)} failed: ${err.message}; keeping the new version`)
      }
      log(`persona: ${name} switched to ${short(hash)} (was ${short(old.hash)})`)
    } catch (err) {
      bad.add(target)
      bad.add(offer.hash)
      log(`persona: ${name} update to ${short(target)} failed: ${err.message}; staying on ${short(old.hash)}`)
      if (next) {
        // The receipt did not name next, so it must not outlive this rollback. Keep it in current while its stop awaits
        // so loader.stop() finds it too; clear it before the old restart, whose late start is checked with gone().
        await stopInstance(next)
        if (current === next) current = undefined
      }
      if (oldStopped && !gone()) {
        try {
          const again = await run(old)
          if (gone()) await stopInstance(again)
          else current = again
        } catch (e) {
          if (!gone()) current = undefined
          log(`persona: ${name} ${short(old.hash)} did not start again: ${e.message}`)
        }
      }
    } finally {
      switching = false
    }
  }

  return {
    /** Start the chosen version (resolves once it runs; rejects when even the shipped one cannot start). */
    start() {
      stopped = false
      starting ??= begin().finally(() => {
        starting = undefined
      })
      return starting
    },
    /** Stop whatever version runs. */
    async stop() {
      stopped = true
      stops++
      pending = undefined
      if (starting) await starting.catch(() => {})
      const v = current
      current = undefined
      await stopInstance(v)
    },
    /** The running version's hash and whether it is the shipped one (tests, logs). */
    get running() {
      return current ? { hash: current.hash, shipped: current.shipped } : undefined
    },
    /** The running implementation (tests). */
    get instance() {
      return current?.instance
    },
    /** An update offer, as runtime.hello's reply would make it (tests). */
    offer: (o) => update(o),
  }
}
