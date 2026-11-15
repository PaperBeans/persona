// Only the guest's declared MCP tool is registered with the SDK and calls the
// same stdio Persona server; no built-in tools or arbitrary command is added.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { guestMcpServer } from '../src/guest-mcp.mjs'

const EXIT_FIXTURE = fileURLToPath(new URL('./guest-mcp-exit.mjs', import.meta.url))
const settleWithin = (promise, message) => {
  let timer
  return Promise.race([
    promise,
    new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(message)), 2500) }),
  ]).finally(() => clearTimeout(timer))
}

test('guest MCP registers and invokes only its declared tools', async () => {
  const { server, close } = await guestMcpServer({
    command: process.execPath,
    args: [fileURLToPath(new URL('./guest-mcp-server.mjs', import.meta.url))],
    env: { PERSONA_GUEST: '1', PERSONA_SESSION_ID: 'guest-test' },
  })
  try {
    assert.equal(server.type, 'sdk')
    assert.equal(server.name, 'persona')
    const messages = []
    const transport = { send: (message) => messages.push(message) }
    await server.instance.connect(transport)
    await transport.onmessage({ jsonrpc: '2.0', id: 1, method: 'tools/list' })
    assert.deepEqual(messages[0].result.tools.map((definition) => definition.name), ['exampledocs_search'])
    await transport.onmessage({ jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name: 'exampledocs_search', arguments: { query: 'synthetic' } } })
    assert.deepEqual(messages[1].result.content, [{ type: 'text', text: 'synthetic:synthetic' }])
  } finally {
    await close()
  }
})

test('guest MCP rejects initialize when the child exits without a response', async () => {
  await assert.rejects(settleWithin(guestMcpServer({
    command: process.execPath,
    args: [EXIT_FIXTURE],
  }), 'initialize remained pending after the child exited'), /Persona MCP exited \(code 0\)/)
})

test('guest MCP rejects an in-flight tools/call when the child exits', async () => {
  const { server } = await guestMcpServer({
    command: process.execPath,
    args: [EXIT_FIXTURE],
    env: { PERSONA_TEST_EXIT_ON: 'tools/call' },
  })
  const messages = []
  const transport = { send: (message) => messages.push(message) }
  await server.instance.connect(transport)
  await settleWithin(transport.onmessage({
    jsonrpc: '2.0', id: 1, method: 'tools/call',
    params: { name: 'exampledocs_search', arguments: { query: 'synthetic' } },
  }), 'tools/call remained pending after the child exited')
  assert.equal(messages[0].result.isError, true)
  assert.match(messages[0].result.content[0].text, /Persona MCP exited \(code 0\)/)
})
