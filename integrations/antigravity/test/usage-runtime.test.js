import assert from 'node:assert/strict'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const FAKE = fileURLToPath(new URL('./fake-acp-usage.mjs', import.meta.url))

async function fixture(t, features, extraOptions = { usageWindowsEveryMs: 20 }) {
  const home = mkdtempSync(join(tmpdir(), 'agy-usage-'))
  const agyDir = join(home, 'agy')
  mkdirSync(agyDir, { recursive: true })
  const quotaFile = join(home, 'quota.json')
  writeFileSync(quotaFile, JSON.stringify({ buckets: [
    { bucketId: '3p-5h', window: '18000s', remainingFraction: 0.75, resetTime: '2099-01-01T00:00:00Z' },
  ] }))
  writeFileSync(join(home, 'native-calls.jsonl'), '')
  writeFileSync(join(home, 'acp-starts.jsonl'), '')
  const binary = join(home, 'fake-agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${FAKE}' '${home}' "$@"\n`)
  chmodSync(binary, 0o700)
  const node = await startFakeNode(t, { features })
  const runtime = createAgyRuntime({ home, agyDir, agyBin: binary, personadBin: '/missing', socketPath: node.path, log: () => {}, ...extraOptions })
  t.after(async () => { await runtime.stop(); rmSync(home, { recursive: true, force: true }) })
  runtime.start()
  await node.ready
  const calls = () => readFileSync(join(home, 'native-calls.jsonl'), 'utf8').split('\n').filter(Boolean).map(JSON.parse)
  const starts = () => readFileSync(join(home, 'acp-starts.jsonl'), 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line).pid)
  return { home, node, runtime, quotaFile, calls, starts }
}

test('agy polls native quota summaries and publishes normalized usage windows', async (t) => {
  const { node, runtime, quotaFile, calls } = await fixture(t, ['usage_windows'])
  const params = await until('first usage window report', () => node.last('usage.windows'))
  assert.equal(params.provider, 'agy')
  assert.equal(params.name, 'Antigravity')
  assert.ok(Number.isFinite(Date.parse(params.sampled_at)))
  assert.deepEqual(params.windows, [{ label: '5h', window_minutes: 300, used_percent: 25, resets_at: '2099-01-01T00:00:00.000Z' }])
  assert.equal(calls()[0].method, 'RetrieveUserQuotaSummary')
  assert.deepEqual(calls()[0].params, { request: { forceRefresh: false } })

  writeFileSync(quotaFile, JSON.stringify({ buckets: [] }))
  await until('empty native snapshot clears the account', () => node.last('usage.windows')?.windows?.length === 0)
  const count = calls().length
  await runtime.stop()
  await new Promise((resolve) => setTimeout(resolve, 60))
  assert.equal(calls().length, count, 'a stopped runtime does not poll again')
})

test('agy does not start a native quota reader against an older node', async (t) => {
  const { node, calls } = await fixture(t, [])
  await new Promise((resolve) => setTimeout(resolve, 80))
  assert.equal(node.last('usage.windows'), undefined)
  assert.deepEqual(calls(), [])
})

test('default runtime never spawns an idle ACP process for quota and samples from live workers without keeping them awake', async (t) => {
  const { home, node, quotaFile, calls, starts } = await fixture(t, ['usage_windows', 'assistant_pids'], {
    sharedAcpIdleMs: 250, assistantAcpIdleMs: 250,
  })
  await new Promise((resolve) => setTimeout(resolve, 120))
  assert.deepEqual(starts(), [], 'idle startup spawns no agy acp process')
  assert.deepEqual(calls(), [])
  assert.equal(node.last('usage.windows'), undefined)

  const { session } = await node.peer.call('session.start', { cwd: home })
  await until('session open samples quota from shared worker', () => node.last('usage.windows')?.windows?.[0]?.used_percent === 25)
  assert.equal(starts().length, 1)
  const sharedPid = starts()[0]
  assert.equal(calls().at(-1)?.worker, sharedPid)

  writeFileSync(quotaFile, JSON.stringify({ buckets: [
    { bucketId: '3p-5h', window: '18000s', remainingFraction: 0.5, resetTime: '2099-01-01T00:00:00Z' },
  ] }))
  await node.peer.call('session.send', { session_id: session.session_id, text: 'run turn' })
  await until('turn completion refreshes quota from shared worker', () => node.last('usage.windows')?.windows?.[0]?.used_percent === 50)

  await until('shared worker still retires after idle window', () => {
    try { process.kill(sharedPid, 0); return false } catch (error) { assert.equal(error.code, 'ESRCH'); return true }
  })

  writeFileSync(quotaFile, JSON.stringify({ buckets: [
    { bucketId: '3p-5h', window: '18000s', remainingFraction: 0.2, resetTime: '2099-01-01T00:00:00Z' },
  ] }))
  await node.peer.call('session.start', { cwd: home, assistant: true })
  await until('assistant session samples quota from its own worker without spawning shared worker', () => node.last('usage.windows')?.windows?.[0]?.used_percent === 80)
  assert.equal(starts().length, 2)
  const assistantPid = starts()[1]
  assert.equal(calls().at(-1)?.worker, assistantPid)
  await until('assistant worker also retires after idle window', () => {
    try { process.kill(assistantPid, 0); return false } catch (error) { assert.equal(error.code, 'ESRCH'); return true }
  })
})
