// Failure-path checks for the real E2E's resource ownership. No Paseo, Codex or account is used here.
// POSIX executables/signals match the real E2E; Windows does not run these fixtures.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { chmodSync, existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'

const alive = (pid) => { try { process.kill(pid, 0); return true } catch { return false } }
function fakeNode(t, ready = false) {
  const root = mkdtempSync(join(tmpdir(), 'persona-codex-cleanup-'))
  const marker = join(root, 'child.json')
  const bin = join(root, 'personad')
  writeFileSync(bin, `#!${process.execPath}\nimport fs from 'node:fs';\nimport readline from 'node:readline';\nfs.writeFileSync(${JSON.stringify(marker)}, JSON.stringify({pid:process.pid,home:process.env.HOME}));\nprocess.on('SIGTERM',()=>{});\nreadline.createInterface({input:process.stdin}).on('line',line=>{const m=JSON.parse(line);if(${ready}&&m.method==='node.info')process.stdout.write(JSON.stringify({id:m.id,result:{}})+'\\n')});\nsetInterval(()=>{},1000);\n`)
  chmodSync(bin, 0o700)
  t.after(() => {
    if (existsSync(marker)) {
      const child = JSON.parse(readFileSync(marker, 'utf8'))
      if (alive(child.pid)) process.kill(child.pid, 'SIGKILL')
      rmSync(dirname(child.home), { recursive: true, force: true })
    }
    rmSync(root, { recursive: true, force: true })
  })
  return { bin, marker, child: () => JSON.parse(readFileSync(marker, 'utf8')) }
}
function assertClean(child) {
  assert.equal(alive(child.pid), false, 'owned personad exited')
  assert.equal(existsSync(dirname(child.home)), false, 'owned node temporary directory removed')
}

test('node.info timeout reaps a child that ignores SIGTERM before startNode rejects', { timeout: 55_000, skip: process.platform === 'win32' }, async (t) => {
  const fixture = fakeNode(t)
  await assert.rejects(startNode({ personad: fixture.bin }), /node.info: timed out/)
  assertClean(fixture.child())
})

test('stopAndWait waits for and reaps a running child that ignores SIGTERM', { timeout: 15_000, skip: process.platform === 'win32' }, async (t) => {
  const fixture = fakeNode(t, true)
  const node = await startNode({ personad: fixture.bin })
  const stopped = node.stopAndWait()
  assert.equal(node.stopAndWait(), stopped, 'cleanup is idempotent')
  await stopped
  assertClean(fixture.child())
})

test('legacy stop removes its temporary home synchronously', { timeout: 15_000, skip: process.platform === 'win32' }, async (t) => {
  const fixture = fakeNode(t, true)
  const node = await startNode({ personad: fixture.bin })
  node.stop()
  assert.equal(existsSync(node.root), false, 'old callers may process.exit immediately after stop')
  await node.stopAndWait()
  assertClean(fixture.child())
})

test('aborting startup cleans up before the caller can receive a node handle', { timeout: 15_000, skip: process.platform === 'win32' }, async (t) => {
  const fixture = fakeNode(t)
  const controller = new AbortController()
  const starting = startNode({ personad: fixture.bin, signal: controller.signal })
  const rejected = assert.rejects(starting, /test cancelled startup/)
  await until('fake node started', () => existsSync(fixture.marker))
  controller.abort(new Error('test cancelled startup'))
  await rejected
  assertClean(fixture.child())
})

test('a non-executable Paseo fails through cleanup with no E2E temporary home left', { timeout: 15_000, skip: process.platform === 'win32' }, async (t) => {
  const root = mkdtempSync(join(tmpdir(), 'persona-codex-cleanup-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const bad = join(root, 'paseo')
  writeFileSync(bad, '#!/bin/sh\nexit 0\n', { mode: 0o600 })
  const before = new Set(readdirSync('/tmp').filter((name) => name.startsWith('persona-paseo-codex-')))
  const script = resolve(dirname(fileURLToPath(import.meta.url)), '../e2e/codex.mjs')
  const child = spawn(process.execPath, [script], {
    env: { PATH: '/usr/bin:/bin', HOME: root, PERSONAD: process.execPath, CODEX_BIN: process.execPath, PASEO_BIN: bad, NODE_TLS_REJECT_UNAUTHORIZED: '1' },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL') })
  let output = ''
  child.stdout.on('data', (d) => { output += d })
  child.stderr.on('data', (d) => { output += d })
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject)
    child.once('exit', resolve)
  })
  assert.equal(code, 1, output)
  assert.match(output, /FAIL real Codex archive lifecycle completed.*EACCES/)
  assert.doesNotMatch(output, /Unhandled 'error' event|FAIL cleanup/)
  assert.deepEqual(readdirSync('/tmp').filter((name) => name.startsWith('persona-paseo-codex-') && !before.has(name)), [])
})
