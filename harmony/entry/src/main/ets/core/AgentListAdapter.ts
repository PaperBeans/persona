import native from 'libpersona_core_ohos.so';
import type { AgentListInput, CoreAgentListProjection, CoreSortInput } from './AgentListProjection';


export interface AgentListAdapter {
  project(input: AgentListInput): CoreAgentListProjection;
}

/** The production adapter has one sorter: the shared Rust core. */
export class CoreAgentListAdapter implements AgentListAdapter {
  project(input: AgentListInput): CoreAgentListProjection {
    if (!input.includeSourceFilters) return this.sort(coreAgentSortInput(input));
    const sourceFilter = input.quick === 'feishu' || input.quick === 'terminal';
    // The companion phone layouts consume the core's same filtered, unpinned inventory.
    const grouping = ['repo','todo','direction','app','recent'].includes(input.grouping) ? 'state' : input.grouping;
    const baseInput = coreAgentSortInput({ ...input, grouping, quick: sourceFilter ? 'all' : input.quick });
    const selectedQuick = baseInput.settings['quick'];
    baseInput.settings['quick'] = 'all';
    baseInput.settings['inventory'] = true;
    const base = this.sort(baseInput);
    if (!base.filtered) throw new Error('missing_core_inventory');
    const included = new Set(base.filtered), kept = new Set(input.kept);
    const counts: Record<string, Object> = { ...base.filter_counts, feishu: 0, terminal: 0 };
    const selected: Record<string, Object>[] = [];
    for (const row of input.rows) {
      if (!included.has(row.id)) continue;
      const origin = agentPaneSource(rowText(row.raw, 'pane_id'));
      if (origin && row.raw['nested_subtask'] !== true) counts[origin] = Number(counts[origin]) + 1;
      if (sourceFilter && (origin === input.quick || kept.has(row.id))) selected.push(row.raw);
    }
    let shown = base;
    if (sourceFilter) {
      baseInput.sessions = selected;
      shown = this.sort(baseInput);
    } else if (input.quick !== 'all') {
      baseInput.settings['quick'] = selectedQuick;
      shown = this.sort(baseInput);
    }
    if ((input.grouping === 'machine' || input.grouping === 'space') && !input.query &&
        (input.quick === 'all' || input.quick === 'needsYou') && Number(counts['needsYou']) > 0) {
      const attention = this.sort({...baseInput, settings:{...baseInput.settings,grouping:'state',quick:'needsYou'}})
        .groups.find((group) => group.id === 'state/needs');
      if (attention) shown.groups = [attention, ...shown.groups];
    }
    shown.queryMatches = base.filtered;
    shown.filter_counts = counts;
    return shown;
  }

  private sort(input: CoreSortInput): CoreAgentListProjection {
    const projected = JSON.parse(native.coreProjectSort(JSON.stringify(input))) as CoreAgentListProjection;
    if (!projected || !Array.isArray(projected.groups) || !Array.isArray(projected.unread_shown) || !projected.counts) throw new Error('invalid_core_projection');
    return projected;
  }
}

// Session.source on iPhone classifies the native pane identity, independently of the Agent provider.
export function agentPaneSource(pane: string): string {
  if (!pane) return '';
  const marker = pane.indexOf(':');
  const host = marker < 0 ? '' : pane.slice(0, marker);
  if (host === 'botmux') return 'feishu';
  return host === 'orca' ? '' : 'terminal';
}

export function coreAgentSortInput(input: AgentListInput): CoreSortInput {
  const machineFilter = coreMachineFilter(input);
  if (machineFilter === undefined) throw new Error('unsupported_machine_filter');
  const grouping = input.grouping === 'flat' ? 'state' : input.grouping === 'repository' ? 'space' : input.grouping;
  if (!['state', 'space', 'machine'].includes(grouping)) throw new Error('unsupported_grouping');
  const quick = {all: 'all', needs_you: 'needsYou', unread: 'unread', working: 'working'}[input.quick] || input.quick;
  if (!['all', 'needsYou', 'unread', 'working', 'backgroundWaiting'].includes(quick)) throw new Error('unsupported_quick_filter');
  const pinsSpaces: string[] = [], pinsSessions: string[] = [];
  const mutesSpaces: string[] = [], mutesSessions: string[] = [], mutesAllowed: string[] = [];
  const hubPreferences: Record<string, Object> = {};
  const managerPreferences: Record<string, Object> = {};
  input.rows.forEach((row) => {
    const current = input.preferencesFor(row), group = rowText(row.raw, 'app_hub_group');
    const preferences: Record<string, Object> = {
      pins: {spaces: current.pinsSpaces.slice(), sessions: current.pinsSessions.slice()},
      mutes: {spaces: current.mutesSpaces.slice(), sessions: current.mutesSessions.slice(), allowed: current.mutesAllowed.slice()}
    };
    if (rowText(row.raw, 'manager')) {
      managerPreferences[group + '|' + rowText(row.raw, 'manager')] = preferences;
    } else if (group) hubPreferences[group] = preferences;
    else {
      // The legacy maps belong to the default Hub only; never concatenate owners.
      pinsSpaces.splice(0, pinsSpaces.length, ...current.pinsSpaces);
      pinsSessions.splice(0, pinsSessions.length, ...current.pinsSessions);
      mutesSpaces.splice(0, mutesSpaces.length, ...current.mutesSpaces);
      mutesSessions.splice(0, mutesSessions.length, ...current.mutesSessions);
      mutesAllowed.splice(0, mutesAllowed.length, ...current.mutesAllowed);
    }
  });
  return {sessions: input.rows.map((row) => row.raw), machines: input.machines,
    pins: {spaces: pinsSpaces, sessions: pinsSessions}, mutes: {spaces: mutesSpaces, sessions: mutesSessions, allowed: mutesAllowed},
    hub_preferences: hubPreferences,
    manager_preferences: managerPreferences,
    settings: {...input.coreSettings, grouping, quick, query: input.query, machine_filter: machineFilter, kept: input.kept, history: input.history}};
}

export function coreMachineFilter(input: AgentListInput): string | undefined {
  if (!input.machineFilter) return '';
  if (input.machineFilter.includes('|') && input.machineFilter[0] !== '[') return input.machineFilter;
  if (input.machineFilter[0] === '[') {
    try {
      const parsed = JSON.parse(input.machineFilter) as Object[];
      if (parsed.length !== 3) return undefined;
      const group = typeof parsed[0] === 'string' ? parsed[0] as string : '';
      const id = typeof parsed[2] === 'string' ? parsed[2] as string : '';
      const manager = typeof parsed[1] === 'string' ? parsed[1] as string : '';
      if (!id) return undefined;
      if (manager) return (group ? group + '|' : '') + manager + '|' + id;
      return group ? group + '|' + id : id;
    } catch (_) { return undefined; }
  }
  return input.machineFilter;
}

export function nativeCoreProjection(input: AgentListInput): CoreAgentListProjection {
  return JSON.parse(native.coreProjectSort(JSON.stringify(coreAgentSortInput(input)))) as CoreAgentListProjection;
}

function rowText(value: Record<string, Object>, key: string): string {
  const item = value[key];
  return typeof item === 'string' ? item : '';
}
