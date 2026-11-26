import assert from 'node:assert/strict'
import { test } from 'node:test'
import { extractBackground } from '../lib/background.js'

const conversation = '11111111-2222-4333-8444-555555555555'
const id = `${conversation}/task-1`
const started = '2026-06-01T10:00:00Z'
const fired = '2026-06-01T10:01:01Z'

const cases = [
  { name: 'timer duration', args: { DurationSeconds: 60 }, description: 'Check status', completed: true },
  { name: 'timer description', completed: true },
  { name: 'cron expression', args: { CronExpression: '0 * * * *' } },
  { name: 'cron description', args: { DurationSeconds: '60' }, description: 'Cron: 0 * * * *' },
  { name: 'cron iteration', args: { DurationSeconds: '60' }, message: 'Check again (iteration 1)' },
  { name: 'heartbeat', args: { DurationSeconds: '60' }, message: 'Task is still running' },
  { name: 'different sender', sender: `${id}0` },
]

for (const { name, args, description = 'Timer: 60s', sender = id, message = 'Check again', completed } of cases) {
  test(`AGY schedule message: ${name}`, () => {
    const { running, ended } = extractBackground([
      { type: 'PLANNER_RESPONSE', tool_calls: [{ name: 'schedule', args }] },
      { type: 'GENERIC', status: 'RUNNING', created_at: started,
        content: `Tool is running as a background task with task id: ${id}\nTask Description: ${description}` },
      { type: 'SYSTEM_MESSAGE', created_at: fired,
        content: `<SYSTEM_MESSAGE>\n[Message] timestamp=${started} sender=${sender} priority=MESSAGE_PRIORITY_HIGH content=${message}\n</SYSTEM_MESSAGE>` },
    ], conversation)
    const task = { id, kind: 'monitor', description, started_at: started }
    assert.deepEqual(running, completed ? [] : [task])
    assert.deepEqual(ended, completed ? [{ ...task, status: 'completed', ended_at: fired }] : [])
  })
}
