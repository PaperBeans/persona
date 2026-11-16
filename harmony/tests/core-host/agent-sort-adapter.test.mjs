// Production ArkTS vs the real Rust core. Host evidence only: never a NAPI, HAP or physical-device claim.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import './agent-sort-loader.mjs';
import { HostCore, helperPath } from './host.mjs';

const {debugAgentSortSnapshot} = await import('../../entry/src/main/ets/core/DebugAgentSortSnapshot.ts');
const {NativeAgentListAdapter, coreAgentSortInput, nativeCoreProjection} = await import('../../entry/src/main/ets/core/AgentListAdapter.ts');
const {differingAgentSortRules, compareNativeWithCore} = await import('../../entry/src/main/ets/core/AgentListProjection.ts');

const fixtureDir = new URL('../../../spec/fixtures/sort/', import.meta.url);
const documents = readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort().map((file) => ({
  file, ...JSON.parse(readFileSync(new URL(file, fixtureDir), 'utf8')),
}));
const cases = documents.filter((doc) => doc.operation === 'agents_list').flatMap((doc) => doc.cases);
assert.equal(cases.length, 30, 'the exact checked-in projection corpus');
assert.equal(new Set(cases.map((c) => c.name)).size, 30, 'unique case names');
const prefs = {pinsSpaces: [], pinsSessions: [], mutesSpaces: [], mutesSessions: [], mutesAllowed: []};

function nativeInput(c) {
  const input = c.input;
  const rows = input.sessions.map((raw) => ({id: raw.session_id, raw: structuredClone(raw), source: {id: raw.session_id}}));
  const machines = new Map(input.machines.map((m) => [`${m.group ?? ''}|${m.id}`, m.name]));
  return {
    rows,
    history: false, reading: false, machines: input.machines,
    grouping: {state: 'flat', space: 'repository', machine: 'machine'}[input.settings.grouping],
    quick: {all: 'all', needsYou: 'needs_you', unread: 'unread', working: 'working', backgroundWaiting: 'working'}[input.settings.quick],
    machineFilter: input.settings.machine_filter,
    query: input.settings.query,
    kept: input.settings.kept,
    coreSettings: {...input.settings},
    preferencesFor: () => ({...prefs, pinsSpaces: input.pins.spaces, pinsSessions: input.pins.sessions,
      mutesSpaces: input.mutes.spaces, mutesSessions: input.mutes.sessions, mutesAllowed: input.mutes.allowed}),
    machineIdentity: (r) => r.raw.machine_id,
    machineName: (r) => machines.get(`${r.raw.app_hub_group ?? ''}|${r.raw.machine_id}`) ?? r.raw.machine_id,
  };
}

// Complete structural projection through the actual production snapshot function, never a second native implementation.
test('all 30 fixtures reach both native ArkTS and the real core; differences are reported, not hidden', async () => {
  const host = new HostCore(helperPath);
  try {
    const results = [];
    for (const c of cases) {
      const input = nativeInput(c);
      const coreInput = coreAgentSortInput(input);
      assert.ok(coreInput, `${c.name}: supported native-to-core input`);
      // Send exactly the same session/registry/preferences values. Native-only modes and membership rules are #8966.
      assert.deepEqual(coreInput.sessions.map((s) => s.session_id), c.input.sessions.map((s) => s.session_id), c.name);
      const core = await host.sort(coreInput);
      assert.deepEqual(core, c.expected, `${c.name}: real core remains pinned to the unchanged fixture`);
      const snapshot = debugAgentSortSnapshot(input);
      assert.ok(snapshot, `${c.name}: production snapshot supports the input`);
      assert.deepEqual(snapshot.input, coreInput, 'the exact snapshot is sent to core');
      const native = snapshot.projection;
      assert.deepEqual(native, nativeCoreProjection(input));
      const rules = differingAgentSortRules(native, core);
      const reports = [];
      assert.equal(compareNativeWithCore(snapshot.input, native, (raw) => {
        assert.deepEqual(JSON.parse(raw), coreInput);
        return JSON.stringify(core);
      }, (report) => reports.push(report)), rules.length === 0, c.name);
      assert.deepEqual(reports.map((report) => report.rule), rules, c.name);
      assert.ok(reports.every((report) => !JSON.stringify(report).includes(c.name)), 'no content in reports');
      results.push({name: c.name, matched: rules.length === 0, rules});
      if (rules.length === 0) assert.deepEqual(native, c.expected, c.name);
    }
    const matched = results.filter((r) => r.matched).length;
    const rules = [...new Set(results.flatMap((r) => r.rules))].sort();
    console.log(`  production ArkTS/core projection: ${matched}/${results.length} equal; ${results.length - matched} recorded by #8966; rules=${rules.join(',')}`);
    assert.equal(results.length, 30);
    assert.ok(matched < results.length, 'the parity oracle must not silently erase the known native/iOS mismatch (#8966)');
    assert.deepEqual(rules, ['counts', 'group-order', 'unread-shown']);
    assert.equal(matched, 1, 'current host baseline; #8966 must update this expectation as parity improves');
  } finally { await host.end(); }
});

test('the production comparison rejects a wrong expectation and native failure without content', async () => {
  const host = new HostCore(helperPath);
  try {
    const c = cases[0];
    const native = nativeCoreProjection(nativeInput(c));
    assert.notDeepEqual(native, {...c.expected, counts: {needsYou: 0, unread: 0, working: 0, backgroundWaiting: 0}});
    await assert.rejects(() => host.sort({private: 'value must not be repeated'}), /sort/);
  } finally { await host.end(); }
});
