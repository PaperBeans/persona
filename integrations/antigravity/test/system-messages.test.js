import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { mapToolInput, stepsToItems } from '../lib/map.js'

// Shapes agy 1.5.15 writes for what subagents and background tasks send back (#14625); all text is synthetic.
const AT = '2026-11-24T08:00:00Z'
const PARENT = '00000000-0000-4000-8000-000000000001'
const CHILD = '00000000-0000-4000-8000-000000000002'
const wrap = (inner) => `The following is a <SYSTEM_MESSAGE> not actually sent by the user. It is provided by the system as important information to pay attention to.\n\n<SYSTEM_MESSAGE>\n${inner}\n</SYSTEM_MESSAGE>`
const message = (sender, content, priority = 'MESSAGE_PRIORITY_HIGH') => wrap(`[Message] timestamp=${AT} sender=${sender} priority=${priority} content=${content}`)
const reminder = wrap('Reminder: work efficiently. Batch independent reads.')
const step = (step_index, type, extra = {}) => ({ step_index, type, source: type === 'USER_INPUT' ? 'USER_EXPLICIT' : type === 'SYSTEM_MESSAGE' ? 'SYSTEM' : 'MODEL', status: 'DONE', created_at: AT, ...extra })
const system = (step_index, content) => step(step_index, 'SYSTEM_MESSAGE', { content })
const notices = (items) => items.filter((item) => item.kind === 'notice')

describe('agy SYSTEM_MESSAGE steps', () => {
  it('shows subagent reports, task results and notices, and keeps agy reminders hidden', () => {
    const { items } = stepsToItems([
      step(0, 'USER_INPUT', { content: 'Split the survey.' }),
      step(1, 'PLANNER_RESPONSE', { tool_calls: [{ name: 'invoke_subagent', args: { Role: 'Survey Worker', TypeName: 'self', Prompt: 'Summarize part one.' } }] }),
      system(2, reminder),
      system(3, message('system', `Subagent ${CHILD} has gone idle. This was the subagent's most recent response: Part one is done.\n\nThree findings follow.`)),
      system(4, message(`${PARENT}/task-7`, `Task id "${PARENT}/task-7" finished with result:\nCommand exited with code 2.\n\nCommand output:\nmissing input`)),
      system(5, message(`${PARENT}/task-8`, `Task id "${PARENT}/task-8" was canceled with result:\nTool execution was canceled`, 'MESSAGE_PRIORITY_LOW')),
      system(6, message('system', '[Notice] All your subagents and background tasks have been stopped due to server restart.', 'MESSAGE_PRIORITY_LOW')),
      system(7, message(CHILD, 'Part two is done.\n\n| a | b |')),
      step(8, 'PLANNER_RESPONSE', { content: 'All parts are in.' }),
    ])
    assert.deepEqual(notices(items).map(({ text, task, wake }) => ({ text, task, wake })), [
      { text: '子 Agent 完成 · Part one is done.', task: { kind: 'agent', status: 'completed', title: 'Part one is done.', task_id: CHILD, detail: 'Part one is done.\n\nThree findings follow.' }, wake: undefined },
      { text: 'task-7', task: { kind: 'task', status: 'failed', title: 'task-7', task_id: `${PARENT}/task-7`, exit_code: 2, detail: 'Command exited with code 2.\n\nCommand output:\nmissing input' }, wake: undefined },
      { text: 'task-8', task: { kind: 'task', status: 'stopped', title: 'task-8', task_id: `${PARENT}/task-8`, detail: 'Tool execution was canceled' }, wake: undefined },
      { text: 'All your subagents and background tasks have been stopped due to server restart.', task: { kind: 'task', status: '', title: 'All your subagents and background tasks have been stopped due to server restart.', detail: '[Notice] All your subagents and background tasks have been stopped due to server restart.' }, wake: undefined },
      { text: '子 Agent · Part two is done.', task: { kind: 'agent', status: '', title: 'Part two is done.', task_id: CHILD, detail: 'Part two is done.\n\n| a | b |' }, wake: undefined },
    ])
    assert.doesNotMatch(JSON.stringify(items), /Reminder|SYSTEM_MESSAGE/)
  })

  it('keeps the generic wake notice on the resumed step, so an append after the report still carries it', () => {
    const report = message('system', `Subagent ${CHILD} has gone idle. This was the subagent's most recent response: Done.`)
    const base = [step(0, 'USER_INPUT', { content: 'Start a worker.' }), step(1, 'PLANNER_RESPONSE', { content: 'Started; waiting for it.' }), system(2, reminder), system(3, report), system(4, message(CHILD, 'Extra note.'))]
    const before = stepsToItems(base).items
    assert.deepEqual(notices(before).map((item) => [item.task?.title, item.wake]), [['Done.', undefined], ['Extra note.', undefined]])
    const pushed = Math.max(...before.map((item) => item.seq))
    const woken = stepsToItems([...base, step(5, 'PLANNER_RESPONSE', { content: 'The worker finished.' })])
    assert.deepEqual(woken.items.filter((item) => item.seq > pushed).map(({ kind, text, wake }) => [kind, text, wake]), [['notice', '后台任务有更新', true], ['text', 'The worker finished.', undefined]])
    assert.equal(woken.lastText, 'The worker finished.')
  })

  it('cuts a long report on the page and keeps it whole for session.item', () => {
    const long = 'Report line.\n' + 'x'.repeat(30 * 1024)
    const steps = [step(0, 'USER_INPUT', { content: 'go' }), system(1, message(CHILD, long))]
    const [page] = notices(stepsToItems(steps).items)
    assert.equal(page.full_size, Buffer.byteLength(long))
    assert.ok(Buffer.byteLength(page.task.detail) < 25 * 1024)
    const [full] = notices(stepsToItems(steps, { full: true }).items)
    assert.equal(full.task.detail, long)
    assert.equal(full.full_size, undefined)
  })
})

describe('agy invoke_subagent card', () => {
  it('falls back to Role, TypeName and Prompt', () => {
    assert.deepEqual(mapToolInput('invoke_subagent', { Role: 'Survey Worker', TypeName: 'self', Model: 'inherit', Prompt: 'Summarize part one.' }), { description: 'Survey Worker', prompt: 'Summarize part one.' })
    assert.deepEqual(mapToolInput('invoke_subagent', { TypeName: 'image-generator', Prompt: 'Draw it.' }), { description: 'image-generator', prompt: 'Draw it.' })
    assert.deepEqual(mapToolInput('invoke_subagent', { toolSummary: 'Older shape', Subagents: 'Do it.' }), { description: 'Older shape', prompt: 'Do it.' })
  })
})
