// A guest MCP server that exits instead of answering the selected request.
import { createInterface } from 'node:readline'
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity })
for await (const line of lines) {
  const msg = JSON.parse(line)
  if (msg.method === (process.env.PERSONA_TEST_EXIT_ON ?? 'initialize')) process.exit(0)
  if (msg.method === 'initialize') {
    console.log(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result: {
      protocolVersion: '2025-03-26', capabilities: { tools: { listChanged: false } },
      serverInfo: { name: 'persona-test', version: '1.0.0' },
    } }))
  } else if (msg.method === 'tools/list') {
    console.log(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result: { tools: [{
      name: 'exampledocs_search', description: 'Read synthetic docs.',
      inputSchema: { type: 'object', properties: { query: { type: 'string' } } },
    }] } }))
  }
}
