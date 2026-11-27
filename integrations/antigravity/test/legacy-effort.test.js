import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const CARBON = 'gemini-4-carbon-high'
const CARBON_MEDIUM = 'gemini-4-carbon-medium'
const NEON = 'gemini-4-neon-high'
const NEON_XHIGH = 'gemini-4-neon-xhigh'
const models = [
  { modelId: CARBON, name: 'Gemini 4 Carbon (High)' },
  { modelId: CARBON_MEDIUM, name: 'Gemini 4 Carbon (Medium)' },
  { modelId: NEON, name: 'Gemini 4 Neon (High)' },
  { modelId: NEON_XHIGH, name: 'Gemini 4 Neon (Extra High)' },
]
const rawId = '11111111-2222-3333-4444-555555555555'
const sessionId = `agy-${rawId}`
const fake = fileURLToPath(new URL('./fake-acp-models.mjs', import.meta.url))

async function fixture(t, { transcriptModel, effortFallback } = {}) {
  const home = mkdtempSync(join(tmpdir(), 'agy-legacy-effort-'))
  const agyDir = join(home, 'settings')
  mkdirSync(agyDir)
  writeFileSync(join(agyDir, 'settings.json'), JSON.stringify({ model: NEON }))
  writeFileSync(join(home, 'catalog.json'), JSON.stringify({ models, resumeModel: NEON, effortFallback }))
  writeFileSync(join(home, 'calls.jsonl'), '')
  const agyBin = join(home, 'agy')
  writeFileSync(agyBin, `#!/bin/sh\nexec '${process.execPath}' '${fake}' '${home}' "$@"\n`, { mode: 0o700 })
  if (transcriptModel) {
    const logs = join(agyDir, 'brain', rawId, '.system_generated', 'logs')
    mkdirSync(logs, { recursive: true })
    const steps = [CARBON, transcriptModel, '<synthetic>'].map((model, step_index) => ({
      type: 'PLANNER_RESPONSE', step_index, model, input_tokens: 100, content: 'Reply',
    }))
    writeFileSync(join(logs, 'transcript_full.jsonl'), steps.map(step => JSON.stringify(step)).join('\n') + '\n')
  }
  const n = await startFakeNode(t, )
  const runtime = createAgyRuntime({ home, agyDir, agyBin, personadBin: join(home, 'missing'), socketPath: n.path, log: () => {} })
  t.after(async () => { await runtime.stop(); rmSync(home, { recursive: true, force: true }) })
  runtime.start()
  await n.ready
  const calls = () => readFileSync(join(home, 'calls.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)
  const withSession = (method, params) => n.peer.call(`session.${method}`, { session_id: sessionId, ...params })
  const promptModel = async () => (await until('native prompt model', () => calls().find(call => call.method === 'fake/prompt_model'))).model
  const setModels = () => calls().filter(call => call.method === 'session/set_model').map(call => call.params.modelId)
  return { calls, withSession, promptModel, setModels }
}

test('legacy effort-only modes select the matching model in the saved family after resume', async (t) => {
  const { calls, withSession, promptModel, setModels } = await fixture(t, { transcriptModel: CARBON.toUpperCase() })
  await withSession('modes', { effort: 'medium' })
  await withSession('send', { text: 'Use the new effort' })
  assert.equal(await promptModel(), CARBON_MEDIUM)
  assert.deepEqual(setModels(), [CARBON_MEDIUM])
  assert.ok(!calls().some(call => call.method === 'session/set_config_option'))
})

test('an unlisted effort keeps its legacy request but a native family switch is reverted', async (t) => {
  const { calls, withSession, promptModel, setModels } = await fixture(t, {
    transcriptModel: CARBON, effortFallback: { xhigh: NEON_XHIGH },
  })
  await withSession('modes', { effort: 'xhigh' })
  assert.deepEqual(calls().filter(call => call.method === 'session/set_config_option').map(call => call.params.value), ['xhigh'])
  assert.deepEqual(setModels(), [CARBON, CARBON])
  await withSession('send', { text: 'Keep the original family' })
  assert.equal(await promptModel(), CARBON)
})
