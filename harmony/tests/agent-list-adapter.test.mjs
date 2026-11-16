import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === 'libpersona_core_ohos.so') return {url: 'agent-sort-native:', shortCircuit: true};
    if (specifier === '@kit.PerformanceAnalysisKit') return {url: 'agent-sort-hilog:', shortCircuit: true};
    if (context.parentURL?.includes('/entry/src/main/ets/')) {
      const url = new URL(specifier, context.parentURL);
      if (url.pathname.endsWith('/Wire')) url.pathname += '.ets';
      else if (!/\.[a-z]+$/i.test(url.pathname)) url.pathname += '.ts';
      return {url: url.href, shortCircuit: true};
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'agent-sort-native:') return {format: 'module', shortCircuit: true, source: `
      export default {coreProjectSort(input) { globalThis.agentSortInput = input; return JSON.stringify(globalThis.agentSortOutput); }};
    `};
    if (url === 'agent-sort-hilog:') return {format: 'module', shortCircuit: true,
      source: 'export const hilog={info(...args){globalThis.agentSortLog=args},warn(...args){globalThis.agentSortLog=args}};'};
    if (url.endsWith('.ts') || url.endsWith('.ets')) return {format: 'module', shortCircuit: true,
      source: stripTypeScriptTypes(readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '').replace(/@Concurrent\s*/g, ''))};
    return next(url, context);
  },
});

const {NativeAgentListAdapter, nativeCoreProjection, shadowAgentList, coreAgentSortInput} = await import('../entry/src/main/ets/core/AgentListAdapter.ts');
const {differingAgentSortRules, agentInputShape} = await import('../entry/src/main/ets/core/AgentListProjection.ts');
const {DebugAgentListShadow} = await import('../entry/src/main/ets/platform/DebugAgentListShadow.ets');
const {ResourceRow} = await import('../entry/src/main/ets/models/Wire.ets');

function row(id, state = 'idle', extra = {}) {
  const data = {session_id: id, machine_id: 'machine-a', title: `Private ${id}`, cwd: `/private/${id}`,
    repo_root: `/private/${id}`, worktree: `/private/${id}`, state, status: state,
    updated_at: '2026-01-01T00:00:10Z', ...extra};
  const source = ResourceRow.from(data, 'session');
  return {id, raw: data, source};
}
const preferences = {pinsSpaces: [], pinsSessions: [], mutesSpaces: [], mutesSessions: [], mutesAllowed: []};
function input(rows = [row('a', 'working'), row('b', 'idle')], grouping = 'flat') {
  return {rows, history: false, grouping, quick: 'all', query: '', machineFilter: '', kept: [], reading: false,
    machines: [{id: 'machine-a', name: 'Example computer'}, {id: 'machine-b', name: 'Example computer'}],
    coreSettings: {sort: 'state', folded: [], expanded_idle: [], expanded_quiet: [], compact: false},
    preferencesFor: () => preferences, machineIdentity: (r) => r.raw.machine_id, machineName: (r) => 'Example computer'};
}
const empty = {counts: {needsYou: 0, unread: 0, working: 0, backgroundWaiting: 0}, groups: [], unread_shown: []};

test('native adapter returns the original ResourceRow identity and all existing group flags', () => {
  const native = new NativeAgentListAdapter();
  for (const grouping of ['flat', 'machine', 'repository', 'worktree']) {
    const groups = native.project(input([row('a', 'working'), row('b', 'idle', {machine_id: 'machine-b'})], grouping));
    assert.ok(groups.length > 0);
    assert.ok(groups.flatMap((g) => g.rows).every((r) => r.source instanceof ResourceRow));
    assert.ok(groups.every((g) => g.showMachine === (grouping === 'machine') &&
      g.showRepository === (grouping === 'repository') && g.showWorktree === (grouping === 'worktree')));
  }
});

test('shadow compares the actual core output and keeps native results authoritative', () => {
  const list = input();
  const native = nativeCoreProjection(list);
  globalThis.agentSortOutput = native;
  const reports = [];
  assert.equal(shadowAgentList(list, native, (raw) => {globalThis.agentSortOutput = native; return JSON.stringify(native);}, (r) => reports.push(r)), true);
  assert.deepEqual(reports, []);
  const before = new NativeAgentListAdapter().project(list).map((g) => g.rows.map((r) => r.id));
  globalThis.agentSortOutput = empty;
  assert.equal(shadowAgentList(list, native, () => JSON.stringify(empty), (r) => reports.push(r)), false);
  assert.deepEqual(new NativeAgentListAdapter().project(list).map((g) => g.rows.map((r) => r.id)), before);
  assert.deepEqual(reports.map((r) => r.rule), ['counts', 'group-order']);
  const serialized = JSON.stringify(reports);
  for (const secret of ['Private a', '/private/a', 'machine-a', 'session_id', 'cwd', 'Private b']) assert.ok(!serialized.includes(secret), serialized);
});

test('each projection dimension gets a rule; malformed and throwing native calls cannot escape', () => {
  const native = nativeCoreProjection(input());
  const wrong = structuredClone(native);
  wrong.groups[0].visible_rows = [];
  wrong.groups[0].title = 'Different';
  wrong.unread_shown = ['unknown/value'];
  assert.deepEqual(differingAgentSortRules(native, wrong), ['visible-rows', 'group-metadata', 'unread-shown']);
  for (const output of [{}, {groups: [], counts: {}}]) {
    const reports = [];
    assert.equal(shadowAgentList(input(), native, () => JSON.stringify(output), (r) => reports.push(r)), false);
    assert.deepEqual(reports.map((r) => r.rule), ['native-failure']);
  }
  const reports = [];
  assert.equal(shadowAgentList(input(), native, () => {throw new Error('private content must not escape');}, (r) => reports.push(r)), false);
  assert.ok(!JSON.stringify(reports).includes('private content'));
  assert.equal(coreAgentSortInput(input([row('a')], 'worktree')), undefined, 'never relabel worktree as machine');
});

test('input shape is counts and enum flags only, including a nonempty private query', () => {
  const list = {...input(), query: 'private query'};
  const shape = agentInputShape(coreAgentSortInput(list));
  assert.ok(shape.includes('query=set'));
  assert.ok(!shape.includes('private query'));
});

test('debug receiver reports only shape and counts and never loads Rust during snapshot', () => {
  const reports = [], shadow = new DebugAgentListShadow((r) => reports.push(r));
  const snapshot = shadow.snapshot(input());
  assert.ok(snapshot);
  assert.equal(globalThis.agentSortInput, undefined, 'snapshot must not call NAPI');
  shadow.receive([]);
  assert.equal(reports.length, 0);
  shadow.receive([{domain: 'agents-list-sort', rule: 'row-order', inputShape: 'sessions=1'}]);
  assert.equal(reports.length, 1);
  shadow.failure();
  assert.deepEqual(reports.at(-1).inputShape, 'worker-failed=true');
  assert.equal(globalThis.agentSortLog, undefined, 'a custom reporter owns diagnostics');
});

test('frozen reading uses the projected membership and a real core input, not an undefined helper', () => {
  const list = input([row('removed', 'offline'), row('kept', 'working', {machine_id: 'machine-b'})]);
  const groups = new NativeAgentListAdapter().project(list).map((group) => ({...group, id: `machine/${group.machineId}`}));
  const frozen = {...list, reading: true, grouping: 'machine', kept: ['machine-a/removed', 'machine-b/kept'],
    machineFilter: 'machine-b', frozen: groups};
  const core = coreAgentSortInput(frozen);
  assert.ok(core);
  assert.deepEqual(core.sessions.map((s) => s.session_id), ['kept'], 'core receives exactly the native-visible frozen rows');
  assert.deepEqual(core.settings.kept, ['machine-b/kept'], 'only visible frozen members are kept for core');
  assert.equal(core.settings.machine_filter, 'machine-b');
  const reports = [];
  assert.equal(shadowAgentList(frozen, nativeCoreProjection(frozen), () => JSON.stringify({
    counts: {needsYou: 0, unread: 0, working: 1, backgroundWaiting: 0},
    groups: [{id: 'machine/machine-b', title: 'Example computer', rows: ['machine-b/kept'], visible_rows: [], folded: true}],
    unread_shown: [],
  }), (report) => reports.push(report)), false, 'native group naming remains a genuine tracked difference');
  assert.deepEqual(reports.map((report) => report.rule), ['visible-rows', 'group-metadata'],
    'the frozen other machine adds no missing-group or row-order mismatch');
  const native = nativeCoreProjection(frozen);
  assert.deepEqual(native.groups.map((group) => group.rows), [['machine-b/kept']]);
  const snapshot = new DebugAgentListShadow().snapshot(frozen);
  assert.ok(snapshot, 'snapshot must be a plain object');
  assert.equal(Object.getPrototypeOf(snapshot.input), Object.prototype);
  assert.equal(Object.getPrototypeOf(snapshot.projection), Object.prototype);
});

test('machine filters map by owner and never drop an ambiguous cross-Hub group', () => {
  const base = input([row('a', 'working', {app_hub_group: 'hub-a'}),
    row('b', 'working', {app_hub_group: 'hub-b'})]);
  const json = {...base, machines: [{id: 'machine-a', name: 'A', group: 'hub-a'}], machineFilter: '["hub-a","work","machine-a"]'};
  assert.equal(coreAgentSortInput(json).settings.machine_filter, 'hub-a|machine-a');
  assert.equal(coreAgentSortInput({...json, machineFilter: 'machine-a'}).settings.machine_filter, 'hub-a|machine-a');
  const ambiguous = {...json, rows: [row('a', 'working', {app_hub_group: 'hub-a'}),
    row('b', 'working', {app_hub_group: 'hub-b'})],
    machines: [{id: 'machine-a', name: 'A', group: 'hub-a'}, {id: 'machine-a', name: 'B', group: 'hub-b'}], machineFilter: 'machine-a'};
  assert.equal(coreAgentSortInput(ambiguous), undefined);
});

test('coalescing retains one pending newest snapshot and teardown always runs', () => {
  const index = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8');
  const start = index.slice(index.indexOf('private runAgentListShadow'), index.indexOf('aboutToDisappear'));
  assert.ok(!index.includes('agentShadowQueue'));
  assert.match(start, /this\.agentShadowPending = undefined;/);
  assert.match(start, /try \{\n\s+const task = new taskpool\.Task/);
  const dispose = index.slice(index.indexOf('aboutToDisappear'), index.indexOf('@Monitor', index.indexOf('aboutToDisappear')));
  assert.match(dispose, /finally \{ this\.agentWorkspace\.setListShadow/);
});

test('release has no shadow module reference and DEBUG is the only dynamic-load gate', () => {
  const index = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8');
  const start = index.slice(index.indexOf('private startAgentListShadow'), index.indexOf('aboutToDisappear'));
  assert.match(start, /if \(!BuildProfile\.DEBUG(?: \|\| [^)]+)?\) return;/);
  assert.ok(start.indexOf('BuildProfile.DEBUG') < start.indexOf("import('../platform/DebugAgentListShadow')"));
  assert.ok(!/^import\s+\{[^}]*\}\s+from\s+['"]\.\.\/platform\/DebugAgentListShadow['"];?$/m.test(index), 'no runtime static import');
  assert.ok(index.includes("import type { DebugAgentListShadow }"));
  const worker = readFileSync(new URL('../entry/src/main/ets/platform/DebugAgentListShadow.ets', import.meta.url), 'utf8');
  assert.match(worker, /@Concurrent\nexport function runDebugAgentSortShadow/);
  assert.ok(!/new taskpool\.Task\(shadow\./.test(index));
});
