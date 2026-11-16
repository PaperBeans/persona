import { Wire } from '../models/Wire';

export interface AgentListRow {
  id: string;
  raw: Record<string, Object>;
  source: Object;
}

export interface AgentListPreferences {
  pinsSpaces: string[];
  pinsSessions: string[];
  mutesSpaces: string[];
  mutesSessions: string[];
  mutesAllowed: string[];
}

export interface AgentListInput {
  rows: AgentListRow[];
  history: boolean;
  grouping: string;
  quick: string;
  machineFilter: string;
  query: string;
  /** Already frozen while reading: stable membership, with live status refreshed in place. */
  reading: boolean;
  /** The already projected frozen groups, with current row data but unchanged membership. */
  frozen?: AgentListGroupProjection[];
  kept: string[];
  /** Full registry, including an empty registry when a session's machine is unknown. */
  machines: Record<string, Object>[];
  coreSettings: Record<string, Object>;
  preferencesFor(row: AgentListRow): AgentListPreferences;
  machineIdentity(row: AgentListRow): string;
  machineName(row: AgentListRow): string;
}

export interface AgentListGroupProjection {
  id: string;
  machineId: string;
  machineName: string;
  repositoryPath: string;
  repositoryName: string;
  worktreePath: string;
  worktreeName: string;
  branch: string;
  showMachine: boolean;
  showRepository: boolean;
  showWorktree: boolean;
  rows: AgentListRow[];
}

function rowMachine(row: AgentListRow): string { return Wire.text(row.raw, 'machine_id'); }
function rowSession(row: AgentListRow): string { return Wire.text(row.raw, 'session_id'); }
function rowSpaceKey(row: AgentListRow): string {
  const name = Wire.text(row.raw, 'space_name');
  return name ? rowMachine(row) + '/' + name : '';
}
function sessionKey(row: AgentListRow): string {
  const machine = rowMachine(row), session = rowSession(row);
  return machine && session ? machine + '/' + session : '';
}
function pathName(path: string): string {
  const normalized = path.replace(/\\/g, '/').replace(/\/+$/g, '');
  return normalized ? normalized.slice(normalized.lastIndexOf('/') + 1) : '';
}
function muted(row: AgentListRow, preferences: AgentListPreferences): boolean {
  const key = sessionKey(row), space = rowSpaceKey(row);
  return preferences.mutesSessions.includes(key) ||
    (!preferences.mutesAllowed.includes(key) && !!space && preferences.mutesSpaces.includes(space));
}
function rank(row: AgentListRow, preferences: AgentListPreferences): number {
  const state = Wire.text(row.raw, 'state', Wire.text(row.raw, 'status'));
  if (state === 'needs_you') return 0;
  if (state === 'unread') return 1;
  if (state === 'working') return 2;
  if (muted(row, preferences)) return 4;
  return 3;
}
function compareRows(left: AgentListRow, right: AgentListRow, input: AgentListInput): number {
  const leftRepository = Wire.text(left.raw, 'repo_root', Wire.text(left.raw, 'worktree'));
  const rightRepository = Wire.text(right.raw, 'repo_root', Wire.text(right.raw, 'worktree'));
  const leftWorktree = Wire.text(left.raw, 'worktree', Wire.text(left.raw, 'cwd', leftRepository));
  const rightWorktree = Wire.text(right.raw, 'worktree', Wire.text(right.raw, 'cwd', rightRepository));
  if (input.grouping !== 'flat') {
    const machineOrder = rowMachine(left).localeCompare(rowMachine(right));
    if (machineOrder) return machineOrder;
    if (input.grouping === 'repository' || input.grouping === 'worktree') {
      const repositoryOrder = leftRepository.localeCompare(rightRepository);
      if (repositoryOrder) return repositoryOrder;
    }
    if (input.grouping === 'worktree') {
      const worktreeOrder = leftWorktree.localeCompare(rightWorktree);
      if (worktreeOrder) return worktreeOrder;
    }
  }
  const leftPinned = input.preferencesFor(left).pinsSessions.indexOf(sessionKey(left));
  const rightPinned = input.preferencesFor(right).pinsSessions.indexOf(sessionKey(right));
  if (leftPinned >= 0 || rightPinned >= 0) {
    if (leftPinned < 0) return 1;
    if (rightPinned < 0) return -1;
    if (leftPinned !== rightPinned) return leftPinned - rightPinned;
  }
  const stateOrder = rank(left, input.preferencesFor(left)) - rank(right, input.preferencesFor(right));
  if (stateOrder) return stateOrder;
  const leftDate = Date.parse(Wire.text(left.raw, 'updated_at')) || 0;
  const rightDate = Date.parse(Wire.text(right.raw, 'updated_at')) || 0;
  if (leftDate !== rightDate) return rightDate - leftDate;
  return left.id.localeCompare(right.id);
}

export function matchesNativeAgentListRow(input: AgentListInput, row: AgentListRow): boolean {
  const needle = input.query.trim().toLowerCase();
  const state = Wire.text(row.raw, 'state', Wire.text(row.raw, 'status'));
  if (input.machineFilter && rowMachine(row) !== input.machineFilter && input.machineIdentity(row) !== input.machineFilter) return false;
  if (!input.history && (state === 'offline' || Wire.text(row.raw, 'source_kind') === 'codex_history')) return false;
  if (input.quick !== 'all' && state !== input.quick) return false;
  if (!needle) return true;
  const text = [Wire.text(row.raw, 'title'), Wire.text(row.raw, 'cwd'), Wire.text(row.raw, 'last_text'),
    Wire.text(row.raw, 'agent'), Wire.text(row.raw, 'agent_label'), Wire.text(row.raw, 'space_name'),
    Wire.text(row.raw, 'slot_name'), Wire.text(row.raw, 'repo_root'), Wire.text(row.raw, 'worktree'),
    input.machineName(row)].join(' ').toLowerCase();
  return text.includes(needle);
}

/** The current HarmonyOS projection, unchanged behind its adapter boundary. */
export function projectNativeAgentList(input: AgentListInput): AgentListGroupProjection[] {
  const rows = input.rows.filter((row: AgentListRow) => matchesNativeAgentListRow(input, row));
  rows.sort((left: AgentListRow, right: AgentListRow) => compareRows(left, right, input));
  const groups: AgentListGroupProjection[] = [];
  const lookup = new Map<string, AgentListGroupProjection>();
  rows.forEach((row: AgentListRow) => {
    const machineId = rowMachine(row);
    const repositoryPath = Wire.text(row.raw, 'repo_root', Wire.text(row.raw, 'worktree'));
    const repositoryName = pathName(repositoryPath);
    const worktreePath = Wire.text(row.raw, 'worktree', Wire.text(row.raw, 'cwd', repositoryPath));
    const branch = Wire.text(row.raw, 'branch');
    const worktreeName = branch || pathName(worktreePath) || repositoryName;
    const repositoryKey = repositoryPath || repositoryName;
    const worktreeKey = worktreePath || worktreeName;
    const id = input.grouping === 'flat' ? 'flat' : input.grouping === 'machine' ?
      JSON.stringify([Wire.text(row.raw, 'app_hub_group'), Wire.text(row.raw, 'manager'), machineId]) :
      input.grouping === 'repository' ?
      JSON.stringify([Wire.text(row.raw, 'app_hub_group'), Wire.text(row.raw, 'manager'), machineId, repositoryKey]) :
      JSON.stringify([Wire.text(row.raw, 'app_hub_group'), Wire.text(row.raw, 'manager'), machineId, repositoryKey, worktreeKey]);
    let group = lookup.get(id);
    if (!group) {
      group = {id, machineId, machineName: input.machineName(row), repositoryPath, repositoryName,
        worktreePath, worktreeName, branch, showMachine: input.grouping === 'machine',
        showRepository: input.grouping === 'repository', showWorktree: input.grouping === 'worktree', rows: []};
      lookup.set(id, group); groups.push(group);
    }
    group.rows.push(row);
  });
  return groups;
}

export interface CoreAgentListGroup {
  id: string;
  title: string;
  rows: string[];
  visible_rows: string[];
  folded: boolean;
  parent?: string;
  state_counts?: Record<string, Object>;
  spoken?: string;
}
export interface CoreAgentListProjection {
  counts: Record<string, Object>;
  groups: CoreAgentListGroup[];
  unread_shown: string[];
  quiet_title?: string | null;
}
export interface CoreSortInput {
  sessions: Record<string, Object>[];
  machines: Record<string, Object>[];
  pins: Record<string, Object>;
  mutes: Record<string, Object>;
  settings: Record<string, Object>;
}
export type CoreProjectSort = (input: string) => string;
export type AgentSortRule = 'counts' | 'group-order' | 'row-order' | 'visible-rows' | 'group-metadata' | 'unread-shown' | 'native-failure';
export interface AgentSortReport {
  domain: 'agents-list-sort';
  rule: AgentSortRule;
  inputShape: string;
}

/** Identify every differing rule, but return only its enum and shape to diagnostics. */
export function differingAgentSortRules(native: CoreAgentListProjection, core: CoreAgentListProjection): AgentSortRule[] {
  const rules: AgentSortRule[] = [];
  if (JSON.stringify(native.counts) !== JSON.stringify(core.counts)) rules.push('counts');
  if (native.groups.map((group) => group.id).join('\u0000') !== core.groups.map((group) => group.id).join('\u0000')) rules.push('group-order');
  const coreById = new Map<string, CoreAgentListGroup>(core.groups.map((group) => [group.id, group]));
  let rowOrder = false, visible = false, metadata = false;
  native.groups.forEach((group) => {
    const other = coreById.get(group.id);
    if (!other) return;
    if (JSON.stringify(group.rows) !== JSON.stringify(other.rows)) rowOrder = true;
    if (JSON.stringify(group.visible_rows) !== JSON.stringify(other.visible_rows)) visible = true;
    if (group.title !== other.title || group.folded !== other.folded || group.parent !== other.parent ||
      JSON.stringify(group.state_counts) !== JSON.stringify(other.state_counts) || group.spoken !== other.spoken) metadata = true;
  });
  if (rowOrder) rules.push('row-order');
  if (visible) rules.push('visible-rows');
  if (metadata) rules.push('group-metadata');
  if (JSON.stringify(native.unread_shown) !== JSON.stringify(core.unread_shown)) rules.push('unread-shown');
  return rules;
}
export function agentInputShape(input: CoreSortInput): string {
  const query = Wire.text(input.settings, 'query');
  return 'sessions=' + input.sessions.length + ' machines=' + input.machines.length +
    ' pins=' + Wire.strings(input.pins, 'sessions').length + '/' + Wire.strings(input.pins, 'spaces').length +
    ' mutes=' + Wire.strings(input.mutes, 'sessions').length + '/' + Wire.strings(input.mutes, 'spaces').length +
    ' allowed=' + Wire.strings(input.mutes, 'allowed').length + ' query=' + (query ? 'set' : 'empty') +
    ' grouping=' + Wire.text(input.settings, 'grouping') + ' quick=' + Wire.text(input.settings, 'quick');
}
export function compareNativeWithCore(input: CoreSortInput, native: CoreAgentListProjection,
  project: CoreProjectSort, report: (report: AgentSortReport) => void): boolean {
  const shape = agentInputShape(input);
  try {
    const raw: Object = JSON.parse(project(JSON.stringify(input))) as Object;
    const core = Wire.record(raw) as Object as CoreAgentListProjection;
    if (!Array.isArray(core.groups) || !Array.isArray(core.unread_shown) ||
      typeof core.counts !== 'object' || core.counts === null) {
      report({domain: 'agents-list-sort', rule: 'native-failure', inputShape: shape});
      return false;
    }
    const rules = differingAgentSortRules(native, core);
    rules.forEach((rule: AgentSortRule) => report({domain: 'agents-list-sort', rule, inputShape: shape}));
    return rules.length === 0;
  } catch (_) {
    report({domain: 'agents-list-sort', rule: 'native-failure', inputShape: shape});
    return false;
  }
}
