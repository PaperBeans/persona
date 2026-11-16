import native from 'libpersona_core_ohos.so';
import { hilog } from '@kit.PerformanceAnalysisKit';
import type { AgentListInput } from '../core/AgentListAdapter';
import { compareNativeWithCore } from '../core/AgentListProjection';
import type { AgentSortReport, CoreAgentListProjection, CoreSortInput } from '../core/AgentListProjection';
import type { DebugAgentSortSnapshot } from '../core/DebugAgentSortSnapshot';
import { debugAgentSortSnapshot } from '../core/DebugAgentSortSnapshot';

/** ArkTS taskpool accepts this standalone @Concurrent function, not a captured class method. */
@Concurrent
export function runDebugAgentSortShadow(input: CoreSortInput, projection: CoreAgentListProjection): AgentSortReport[] {
  const reports: AgentSortReport[] = [];
  compareNativeWithCore(input, projection, native.coreProjectSort, (report) => reports.push(report));
  return reports;
}

/** The caller owns serial execution. Native rows never depend on this worker. */
export class DebugAgentListShadow {
  private compared: number = 0;
  private mismatches: number = 0;
  private readonly report: (report: AgentSortReport) => void;
  constructor(report: (report: AgentSortReport) => void = debugAgentSortReport) { this.report = report; }
  receive(reports: AgentSortReport[]): void {
    reports.forEach(this.report);
    this.compared++;
    if (reports.length > 0) this.mismatches++;
    if (this.compared % 10 === 0) hilog.info(0x0000, 'PersonaCoreShadow', 'compared=%{public}d mismatches=%{public}d', this.compared, this.mismatches);
  }
  failure(): void { this.report({domain: 'agents-list-sort', rule: 'native-failure', inputShape: 'worker-failed=true'}); }
  snapshot(input: AgentListInput): DebugAgentSortSnapshot | undefined { return debugAgentSortSnapshot(input); }
}

/** No exception, title, path, identifier or query text crosses this diagnostic boundary. */
export function debugAgentSortReport(report: AgentSortReport): void {
  hilog.warn(0x0000, 'PersonaCoreShadow', '%{public}s %{public}s %{public}s', report.domain, report.rule, report.inputShape);
}

export type DebugAgentListShadowFactory = typeof DebugAgentListShadow;
