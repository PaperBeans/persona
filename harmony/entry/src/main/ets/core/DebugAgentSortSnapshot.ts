import type { AgentListInput } from '../core/AgentListAdapter';
import { coreAgentSortInput, nativeCoreProjection } from '../core/AgentListAdapter';
import type { CoreAgentListProjection, CoreSortInput } from '../core/AgentListProjection';

export interface DebugAgentSortSnapshot {
  input: CoreSortInput;
  projection: CoreAgentListProjection;
}

/** Resolve owner lookups and preferences on the native data owner, before taskpool serialization. */
export function debugAgentSortSnapshot(input: AgentListInput): DebugAgentSortSnapshot | undefined {
  const coreInput = coreAgentSortInput(input), projection = nativeCoreProjection(input);
  return coreInput && projection ? {input: coreInput, projection} : undefined;
}
