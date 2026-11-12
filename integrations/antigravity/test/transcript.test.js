import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { readTranscriptSteps } from '../lib/map.js'

function logs(t) {
  const brain = mkdtempSync(join(tmpdir(), 'agy-transcript-'))
  const directory = join(brain, '.system_generated/logs')
  mkdirSync(directory, { recursive: true })
  t.after(() => rmSync(brain, { recursive: true, force: true }))
  return { brain, directory }
}

const record = (content) => JSON.stringify({ step_index: 0, type: 'PLANNER_RESPONSE', content }) + '\n'

test('the complete transcript is authoritative while the compact export is ahead', (t) => {
  const { brain, directory } = logs(t)
  writeFileSync(join(directory, 'transcript.jsonl'), record('compact') + JSON.stringify({ step_index: 1, content: 'next' }) + '\n')
  writeFileSync(join(directory, 'transcript_full.jsonl'), record('complete') + '{"step_index":')
  assert.deepEqual(readTranscriptSteps(brain, true).map((step) => step.content), ['complete'])
  assert.equal(readTranscriptSteps(brain, false).length, 2)
})

test('complete reads fall back to compact-only histories and unavailable full files', (t) => {
  const { brain, directory } = logs(t)
  writeFileSync(join(directory, 'transcript.jsonl'), record('compact-only history'))
  assert.equal(readTranscriptSteps(brain, true)[0].content, 'compact-only history')
  mkdirSync(join(directory, 'transcript_full.jsonl'))
  assert.equal(readTranscriptSteps(brain, true)[0].content, 'compact-only history')
})

test('fresh rewrites bypass the cache and settled records refresh after atomic replacement', async (t) => {
  const { brain, directory } = logs(t)
  const file = join(directory, 'transcript_full.jsonl')
  writeFileSync(file, record('first'))
  assert.equal(readTranscriptSteps(brain, true)[0].content, 'first')
  assert.equal(readTranscriptSteps(brain, true)[0].content, 'first')
  writeFileSync(file, record('other'))
  assert.equal(readTranscriptSteps(brain, true)[0].content, 'other')
  await new Promise((resolve) => setTimeout(resolve, 1100))
  const cached = readTranscriptSteps(brain, true)
  assert.equal(readTranscriptSteps(brain, true), cached, 'settled files reuse parsed records')
  writeFileSync(file + '.new', record('final'))
  renameSync(file + '.new', file)
  assert.equal(readTranscriptSteps(brain, true)[0].content, 'final')
})
