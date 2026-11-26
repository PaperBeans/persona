import { existsSync } from 'node:fs'
import { basename, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defaultStateRoot, legacyEnvName } from '../../runtime-sdk/lib/compat.js'
import { stateDir } from '../../runtime-sdk/lib/identity.js'
import { selfPackage } from '../../runtime-sdk/lib/package.js'

// The previous context tag still begins a cached preview; its literal stays in this adapter.
export function contextOpening(text) {
  return /^(?:<persona-context\b|<jarvis-context\b)/.test(text)
}

export function daemonCandidates(home, options) {
  // An explicit runtime home owns discovery too; an ambient PERSONA_HOME must not replace it.
  const roots = options.stateHome ? [options.stateHome] : [home ? stateDir(home, {}) : stateDir(), join(home, '.persona'), join(home, '.jarvis')]
  return [options.personadBin, options.jarvisdBin, ...roots.flatMap((root) => [join(root, 'bin', 'personad'), join(root, 'bin', 'jarvisd')])]
}
export function daemonBinary(home, options) {
  return daemonCandidates(home, options).find((p) => p && existsSync(p)) || (options.stateHome ? join(options.stateHome, 'bin', 'personad') : join(defaultStateRoot(home), 'bin', 'personad'))
}
const SOURCE_MCP_PROXY = fileURLToPath(new URL('../mcp-proxy.mjs', import.meta.url))
const SOURCE_MEDIA_GUARD = fileURLToPath(new URL('../media-guard.mjs', import.meta.url))
const SOURCE_PACKAGE = selfPackage(import.meta.url, 'persona-antigravity-runtime', 2)
function stableCurrentPath(source) {
  if (!SOURCE_PACKAGE.store) return source
  const current = join(SOURCE_PACKAGE.store, 'current', 'antigravity', basename(source))
  // Node resolves current to its hash at import time. Resolve it again for each new or resumed ACP session instead.
  return existsSync(current) ? current : source
}
function mcpProxyPath() {
  return stableCurrentPath(SOURCE_MCP_PROXY)
}
export function mediaGuardPath() {
  // The active-version lease keeps this running generation present; choosing current once here could go stale later.
  return SOURCE_MEDIA_GUARD
}
export function mcpServers(bin, pid, assistant, a2aToken) {
  const env = []
  for (const [name, value] of [['PERSONA_SESSION_ID', pid], ...(assistant ? [['PERSONA_ASSISTANT', '1']] : [])]) {
    env.push({ name, value }, { name: legacyEnvName(name), value })
  }
  // Route regular Agent persona tools through the agent-mode proxy so agy's eager tool budget keeps core/leader
  // coordination tools declared while secondary tools stay reachable via persona_call (#7333, #15822).
  // Keep persona_a2a on its separate direct scoped server.
  if (!assistant) {
    const server = { name: 'persona', command: process.execPath, args: [mcpProxyPath(), '--agent', bin, 'mcp'], env }
    if (!a2aToken) return [server]
    return [
      { ...server, env: [...env, { name: 'PERSONA_MCP_EXCLUDE_A2A', value: '1' }] },
      { name: 'persona_a2a', command: bin, args: ['mcp'], env: [...env, { name: 'PERSONA_A2A_TOKEN', value: a2aToken }] },
    ]
  }
  return [{ name: 'persona', command: process.execPath, args: [mcpProxyPath(), bin, 'mcp'], env }]
}
export function assistantSearchPaths(home) {
  return [...new Set([join(stateDir(home), 'assistant', '.bin'), join(home, '.jarvis', 'assistant', '.bin'), join(home, '.local', 'bin')])]
}
