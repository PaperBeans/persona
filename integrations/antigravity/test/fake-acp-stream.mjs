import { appendFileSync, readFileSync } from 'node:fs'
import { createInterface } from 'node:readline'

if (process.argv.includes('models')) { console.log('test-model\tTest Model'); process.exit(0) }

const directory = process.argv[2]
const sessionId = '11111111-2222-4333-8444-555555555555'
const send = (value) => process.stdout.write(`${JSON.stringify({ jsonrpc: '2.0', ...value })}\n`)
let prompt
let consumed = 0

const timer = setInterval(() => {
  const commands = readFileSync(`${directory}/updates.jsonl`, 'utf8').split('\n').filter(Boolean)
  for (const line of commands.slice(consumed)) {
    const update = JSON.parse(line)
    if (update.end) {
      send({ id: prompt, result: { stopReason: 'end_turn' } })
      prompt = undefined
    } else {
      send({ method: 'session/update', params: { sessionId, update } })
    }
  }
  consumed = commands.length
}, 10)

const input = createInterface({ input: process.stdin })
input.on('line', (line) => {
  const request = JSON.parse(line)
  appendFileSync(`${directory}/calls.jsonl`, `${line}\n`)
  if (request.method === 'session/prompt') { prompt = request.id; return }
  if (request.id !== undefined) send({ id: request.id, result: request.method === 'session/new' ? { sessionId } : {} })
})
input.on('close', () => { clearInterval(timer); process.exit(0) })
