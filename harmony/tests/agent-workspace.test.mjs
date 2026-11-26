import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const coreHelper = process.env.PERSONA_CORE_HOST_REPLAY;
globalThis.agentWorkspaceCoreSort = (input) => {
  if (!coreHelper) throw new Error('PERSONA_CORE_HOST_REPLAY is required for the real-core AgentWorkspace tests');
  if (!coreHelper) throw new Error('PERSONA_CORE_HOST_REPLAY is required for the real-core AgentWorkspace tests');
  if (!coreHelper) throw new Error('PERSONA_CORE_HOST_REPLAY is required for the real-core AgentWorkspace tests');
  const result = spawnSync(coreHelper, [], {input: (() => {
    const payload = Buffer.from(input);
    const frame = Buffer.alloc(5);
    frame.writeUInt32LE(payload.length + 1, 0); frame[4] = 10;
    return Buffer.concat([frame, payload]);
  })(), encoding: 'buffer', timeout: 10000, maxBuffer: 32 * 1024 * 1024});
  if (result.error || result.status !== 0) throw new Error('real core helper failed');
  const size = result.stdout.readUInt32LE(0);
  if (size < 1 || result.stdout[4] !== 1) throw new Error('real core rejected the input');
  return result.stdout.subarray(5, 4 + size).toString('utf8');
};


registerHooks({
  resolve(specifier, context, next) {
    if (specifier === 'libpersona_core_ohos.so') return {url: 'agent-workspace-core:', shortCircuit: true};
    if (context.parentURL?.endsWith('/AgentWorkspace.ets') && specifier === './Wire')
      return { url: new URL('./Wire.ets', context.parentURL).href, shortCircuit: true };
    if (context.parentURL?.includes('/entry/src/main/ets/')) {
      const resolved = new URL(specifier, context.parentURL);
      if (['/Wire','/AgentActivityGroups','/AgentRepositoryPlace','/MachineDashboard','/InteractionDecision'].some(name=>resolved.pathname.endsWith(name))) resolved.pathname += '.ets';
      else if (!/\.[a-z]+$/i.test(resolved.pathname)) resolved.pathname += '.ts';
      return { url: resolved.href, shortCircuit: true };
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'agent-workspace-core:') return {format: 'module', shortCircuit: true,
      source: 'export default {coreProjectSort(input) { return globalThis.agentWorkspaceCoreSort(input); }};'};
    if (url.endsWith('.ets') || url.endsWith('/AgentListAdapter.ts') || url.endsWith('/AgentListProjection.ts')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const { AgentWorkspace } = await import('../entry/src/main/ets/models/AgentWorkspace.ets');
const { ResourceRow, Wire } = await import('../entry/src/main/ets/models/Wire.ets');

function agent(id, fields = {}) {
  const data = {
    session_id: id, machine_id: 'm1', manager: 'work', title: 'Agent ' + id, cwd: '/repo', repo_root: '/repo',
    pane_id: 'herdr:pane-' + id, worktree: '/repo', branch: 'main', status: 'working', state: 'working', updated_at: '2026-11-11T10:00:00Z',
    ...fields
  };
  return ResourceRow.from(data, 'session');
}

function machine(id = 'm1', trust = 'personal', manager = 'work') {
  return ResourceRow.from({ id, manager, name: 'Computer ' + id, online: true, trust }, 'machine');
}

function clone(value) { return JSON.parse(JSON.stringify(value)); }

function clientFixture(options = {}) {
  const state = {
    scope: 'hub:work', generation: 3, manager: 'work', calls: [], blocked: new Set(),
    prefs: {
      pins: { spaces: ['m1/keep-space'], sessions: ['m1/keep-session'] },
      mutes: { spaces: ['m1/space'], sessions: ['m1/keep-muted'], allowed: ['m1/allowed', 'keep/allowed'] }
    }, history: (options.history || []).map((row) => row instanceof ResourceRow ? JSON.parse(row.raw) : row), views: new Map(), request: undefined
  };
  const repo = {
    async call(path, method = 'GET', body = {}) {
      const call = { path, method, body: clone(body) };
      state.calls.push(call);
      if (state.request) {
        const result = await state.request(call, state);
        if (result !== undefined) return clone(result);
      }
      if (path === '/api/v1/pins' && method === 'GET') return clone(state.prefs.pins);
      if (path === '/api/v1/mutes' && method === 'GET') return clone(state.prefs.mutes);
      if (path === '/api/v1/pins' && method === 'PUT') { state.prefs.pins = clone(body); return clone(body); }
      if (path === '/api/v1/mutes' && method === 'PUT') { state.prefs.mutes = clone(body); return clone(body); }
      if (path.startsWith('/api/v1/sessions/archived')) return { sessions: state.history };
      return { ok: true };
    }
  };
  const client = {
    connected: true, demo: false, sessionsReady: true, profile: { manager: state.manager }, features: [],
    sessions: options.sessions || [agent('s1', {manager: state.manager})], machines: options.machines || [machine('m1', 'personal', state.manager)],
    selected: new ResourceRow(),
    repo() { return repo; },
    scope() { return state.scope; },
    connectionGeneration() { return state.generation; },
    owns(generation, scope) { return generation === state.generation && scope === state.scope; },
    target(row) { return JSON.stringify([this.scope(), row.kind, row.machine, row.id]); },
    machineName(id) { return this.machines.find((row) => row.id === id)?.title || id; },
    shortPath(path) { return path.replace(/\\/g, '/').split('/').filter(Boolean).slice(-2).join('/'); },
    operationBlocked(path) { return state.blocked.has(path); },
    async loadView(key) { return state.views.get(key === 'agent-hub-mode' ? key : state.scope + ':' + key) || ''; },
    async saveView(key, value) { state.views.set(key === 'agent-hub-mode' ? key : state.scope + ':' + key, value); },
    directoryOwners(){return[this];}, groupName(){return 'Hub';}, directoryOwnerFor(row){return row.manager===this.profile.manager ? this : undefined;}, async refreshDirectory(){},
    async refresh() {}
  };
  return { state, client, repo };
}

async function opened(options = {}) {
  const fixture = clientFixture(options), workspace = new AgentWorkspace();
  await workspace.open(fixture.client);
  return { ...fixture, workspace };
}

test('Needs You omits only waiting rows with solely supervisor-held cards, including a folded child',async()=>{
  const {AgentDecisionQueue}=await import('../entry/src/main/ets/models/AgentDecisionQueue.ets');
  const waiting=id=>agent(id,{state:'needs_you',status:'idle'});
  const {workspace,client,repo}=await opened({sessions:['bare','held','mixed','parent'].map(waiting)});
  const card=(id,session_id,kind,delegating,extras={})=>({id,session_id,machine_id:'m1',manager:'work',kind,delegating,...extras});
  let cards=[card('approval','held','permission',true),card('plan','mixed','plan',true),
    card('ask','mixed','question',true),card('branch','child','screen',true,{folded_under:'parent'})];
  repo.decisions=async()=>structuredClone(cards);
  const queue=new AgentDecisionQueue();
  async function refresh(){await queue.reload(client);workspace.updateSupervisorCards(client,queue);}
  const needs=()=>workspace.groups.find(g=>g.id==='state/needs')?.rows.map(r=>r.id).sort();
  await refresh();
  assert.deepEqual(needs(),['bare','mixed']);
  assert.equal(workspace.filterCounts.needsYou,2);
  workspace.setQuickFilter('needsYou');
  assert.deepEqual(workspace.visibleRows().map(r=>r.id).sort(),['bare','mixed']);
  workspace.setSearch('parent');
  assert.equal(workspace.filterCounts.needsYou,0);
  workspace.setSearch('');workspace.setQuickFilter('all');
  cards=cards.map(c=>({...c,delegating:false}));
  await refresh();
  assert.deepEqual(needs(),['bare','held','mixed','parent']);
  assert.equal(workspace.filterCounts.needsYou,4);
});

test('the phone app and recent groupings read the native filtered inventory, retain history and use deterministic local folds', async () => {
  const when = n => `2026-01-01T00:00:${String(n).padStart(2,'0')}Z`;
  const sessions = [
    agent('z', {agent:'codex',agent_surface:'desktop',space_kind:'codex_app',space_id:'codex-project:/r/a',space_name:'Alpha',space_index:2,updated_at:when(8)}),
    agent('a', {agent:'codex',agent_surface:'desktop',space_kind:'codex_app',space_id:'codex-project:/r/b',space_name:'Beta',space_index:1,updated_at:when(4)}),
    agent('loose', {agent:'codex',agent_surface:'desktop',space_kind:'codex_app',space_id:'codex-project:/Users/u/Documents/Codex/2026-01-01/untitled',space_name:'untitled',updated_at:when(3)}),
    agent('old', {agent:'claude',agent_surface:'desktop',state:'offline',updated_at:when(1)}),
    agent('engine', {space_kind:'herdr-core',updated_at:when(5)}),
    agent('other', {updated_at:when(5)}),
    agent('keep-session',{agent:'codex',agent_surface:'desktop',updated_at:when(9)})
  ];
  const {workspace}=await opened({sessions});
  workspace.setGroupingMode('app');
  assert.deepEqual(workspace.groups.filter(g=>g.presentation==='app' || g.presentation==='app-parent').map(g=>g.id),
    ['app/desktop/claude code','app/desktop/codex','app/engine/herdr-core','app/other']);
  const codexProjects=()=>workspace.groups.filter(g=>g.parent==='app/desktop/codex').map(g=>[g.machineName,g.rows.map(x=>x.id)]);
  assert.deepEqual(codexProjects(),[['Beta',['a']],['Alpha',['z']],['',['loose']]]);
  workspace.toggleFold('app/desktop/codex'); assert.deepEqual(codexProjects(),[]);
  workspace.toggleFold('app/desktop/codex'); assert.equal(codexProjects().length,3);
  workspace.setGroupingMode('recent');
  assert.deepEqual(workspace.groups.find(g=>g.id==='recent').rows.map(x=>x.id),['z','engine','other','a','loose','old']);
  workspace.setSearch('Agent old');
  assert.deepEqual(workspace.unpinnedRows.map(x=>x.id),['old']);
  assert.deepEqual(workspace.groups.find(g=>g.id==='recent').rows.map(x=>x.id),['old']);
});

test('phone grouping preference survives temporarily unavailable tasks and the same choice is restored after lookup', async () => {
  const fixture=clientFixture(), workspace=new AgentWorkspace(undefined,'repo');
  await workspace.open(fixture.client);
  assert.equal(workspace.groupingMode,'repo');
  assert.deepEqual(workspace.groupingOptions(),['repo','space','state','machine','app','recent']);
  workspace.updateGroupingSupport(true,true); workspace.setGroupingMode('direction');
  workspace.updateGroupingSupport(true,false); assert.equal(workspace.groupingMode,'todo');
  workspace.updateGroupingSupport(false,false); assert.equal(workspace.groupingMode,'space');
  workspace.updateGroupingSupport(true,true); assert.equal(workspace.groupingMode,'direction');
  workspace.toggleGroupingFold('todo/unknown',true); assert.equal(workspace.groupingFolded('todo/unknown',true),false);
  workspace.toggleGroupingFold('todo/unknown',false); assert.equal(workspace.groupingFolded('todo/unknown',true),true);
});

test('machine and project groupings display the real pending rows above their outline only for an unfiltered or Needs You view', async () => {
  const {workspace}=await opened({sessions:[agent('pending',{state:'needs_you',status:'needs_you',space_name:'Weather'}),agent('running',{space_name:'Weather'})]});
  for(const mode of ['machine','space']){
    workspace.setGroupingMode(mode);
    assert.deepEqual(workspace.groups[0].rows.map(value=>value.id),['pending']);
    assert.equal(workspace.groups[0].id,'state/needs');
    workspace.setSearch('Weather');assert.ok(workspace.groups.every(group=>group.id!=='state/needs'));
    workspace.setSearch('');workspace.setQuickFilter('working');assert.ok(workspace.groups.every(group=>group.id!=='state/needs'));
    workspace.setQuickFilter('needsYou');assert.equal(workspace.groups[0].id,'state/needs');
    workspace.setQuickFilter('all');
  }
});

test('pins fetch the current document and preserve unrelated space and session keys', async () => {
  const { workspace, state, client } = await opened();
  const row = client.sessions[0];
  state.prefs.pins = { spaces: ['m1/space-a', 'm1/space-b'], sessions: ['m1/s1', 'm2/other'] };
  state.calls.length = 0;
  assert.equal(await workspace.togglePin(row), true);
  assert.deepEqual(state.calls.map((call) => [call.method, call.path]), [
    ['GET', '/api/v1/pins'], ['PUT', '/api/v1/pins']
  ]);
  assert.deepEqual(state.calls[1].body, { spaces: ['m1/space-a', 'm1/space-b'], sessions: ['m2/other'] });
});

test('unmuting an agent inherited from a muted space writes an allowed exception', async () => {
  const { workspace, state, client } = await opened();
  const row = client.sessions[0]; row.raw = JSON.stringify({ ...row.data(), space_name: 'space' });
  state.prefs.mutes = { spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session'], allowed: ['m1/existing', 'keep/allowed'] };
  state.calls.length = 0;
  assert.equal(workspace.isMuted(row), true);
  assert.equal(await workspace.toggleMute(row), true);
  assert.deepEqual(state.calls.map((call) => [call.method, call.path]), [
    ['GET', '/api/v1/mutes'], ['PUT', '/api/v1/mutes']
  ]);
  assert.deepEqual(state.calls[1].body, {
    spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session'], allowed: ['m1/existing', 'keep/allowed', 'm1/s1']
  });
  assert.equal(workspace.isMuted(row), false);
});

test('muting an agent opts it back into a mute beneath an allowed exception', async () => {
  const { workspace, state, client } = await opened();
  const row = client.sessions[0]; row.raw = JSON.stringify({ ...row.data(), space_name: 'space' });
  state.prefs.mutes = { spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session'], allowed: ['m1/s1', 'keep/allowed'] };
  await workspace.refresh(client);
  state.calls.length = 0;
  assert.equal(workspace.isMuted(row), false);
  assert.equal(await workspace.toggleMute(row), true);
  assert.deepEqual(state.calls[1].body, {
    spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session', 'm1/s1'], allowed: ['keep/allowed']
  });
  assert.equal(workspace.isMuted(row), true);
});

test('rename, close, delete, and seen use the existing routes and exact request bodies', async () => {
  const s1 = agent('s1');
  const done = agent('done', { state: 'unread', status: 'done' });
  const { workspace, state } = await opened({ sessions: [s1, done] });
  state.calls.length = 0;
  assert.equal(await workspace.rename(s1, 'Renamed', 'review'), true);
  assert.equal(await workspace.close(s1), true);
  assert.equal(await workspace.delete(done), true);
  assert.equal(await workspace.markSeen(done), true);
  assert.deepEqual(state.calls.filter((call) => call.path.includes('/sessions/')).map((call) => [call.method, call.path, call.body]), [
    ['POST', '/api/v1/sessions/m1/s1/rename', { title: 'Renamed', tab_name: 'review' }],
    ['POST', '/api/v1/sessions/m1/s1/close', {}],
    ['DELETE', '/api/v1/sessions/m1/done', {}],
    ['POST', '/api/v1/sessions/m1/done/seen', {}]
  ]);
});

test('a Just finished row uses the existing seen route and leaves its section immediately', async () => {
  const finished = agent('finished', {
    state: 'idle', status: 'done', work: { phase: 'done', just_finished: true }
  });
  const { workspace, state } = await opened({ sessions: [finished] });
  assert.deepEqual(workspace.groups.map((group) => group.id), ['state/just_finished']);
  assert.equal(workspace.canMarkSeen(finished), true);
  state.calls.length = 0;

  assert.equal(await workspace.markSeen(finished), true);
  assert.deepEqual(state.calls.map((call) => [call.method, call.path]), [
    ['POST', '/api/v1/sessions/m1/finished/seen']
  ]);
  assert.equal(finished.data().work.just_finished, true, 'the caller keeps its immutable row');
  const current = workspace.latestSourceRows.find((row) => row.id === 'finished');
  assert.equal(current.data().work.just_finished, false);
  assert.equal(workspace.canMarkSeen(current), false);
  assert.equal(workspace.groups.some((group) => group.id === 'state/just_finished'), false);
  assert.equal(workspace.visibleRows().some((row) => row.id === 'finished'), false);
});

test('the Just finished group has a native label in every resource set', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  assert.ok(source.includes("'state/just_finished': 'agent_group_just_finished'"));
  const expected = { base: 'Just finished', en_US: 'Just finished', zh_CN: '刚做完待看', ja_JP: '完了したばかり' };
  for (const [locale, value] of Object.entries(expected)) {
    const resources = JSON.parse(readFileSync(new URL(`../entry/src/main/resources/${locale}/element/string.json`, import.meta.url), 'utf8'));
    assert.equal(resources.string.find((entry) => entry.name === 'agent_group_just_finished')?.value, value, locale);
  }
});

test('history loads only when the hub advertises the archive capability and honors machine selection', async () => {
  const archived = agent('archive-1', { archived: true, state: 'offline', status: 'offline' });
  const { workspace, state, client } = await opened({ history: [archived] });
  client.features = ['agent_session_archives'];
  state.calls.length = 0;
  await workspace.setMachineFilter('m1');
  await workspace.setHistory(true);
  assert.ok(state.calls.some((call) => call.method === 'GET' && call.path === '/api/v1/sessions/archived?machine_id=m1&manager=work'));
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.groups.flatMap((group) => group.rows).map((row) => row.id), ['archive-1'], 'archived offline snapshots remain readable history');
});

test('the iOS state list shows friendly identity and state while retaining complete inspection metadata', async () => {
  const row = agent('s1', { cwd: '/work/persona', repo_root: '/work/persona', worktree: '/work/persona', last_text: 'Checking the build' });
  const { workspace } = await opened({ sessions: [row], machines: [machine('m1')] });
  assert.equal(workspace.groupingMode, 'state');
  assert.deepEqual(workspace.groups.map((group) => group.id), ['state/working']);
  assert.equal(workspace.groups[0].showMachine, false);
  assert.equal(workspace.groups[0].showRepository, false);
  assert.equal(workspace.groups[0].showWorktree, false);
  assert.deepEqual(workspace.rowMetadata(row), ['Hub · work', 'Computer m1', 'work/persona', 'working', 'Checking the build']);
  assert.deepEqual(workspace.listMetadata(row), ['Hub', 'Computer m1', 'working', 'Checking the build']);
});

test('iOS state, machine and space groupings share core visibility and release reading freeze', async () => {
  const first = agent('s1', { repo_root: '/work/persona', worktree: '/work/persona' });
  const second = agent('s2', { repo_root: '/work/persona', worktree: '/work/persona/feature', branch: 'feature' });
  const third = agent('s3', { machine_id: 'm2', repo_root: '/other/repo', cwd: '/other/repo', worktree: '/other/repo' });
  const { workspace, client } = await opened({ sessions: [first, second, third], machines: [machine('m1'), machine('m2')] });
  assert.equal(workspace.visibleRows().length, 3);
  assert.deepEqual(workspace.groups.map((group) => group.id), ['state/working']);
  workspace.setGroupingMode('machine');
  assert.deepEqual(workspace.groups.map((group) => group.id), ['machine/work|m1', 'machine/work|m2']);
  assert.ok(workspace.groups.every((group) => group.showMachine && !group.showRepository && !group.showWorktree));
  workspace.setGroupingMode('space');
  assert.equal(workspace.groups.length, 2);
  assert.ok(workspace.groups.every((group) => !group.showMachine && !group.showRepository && !group.showWorktree));
  workspace.setGroupingMode('state');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1', 's2', 's3']);
  workspace.setReadingDetail(true);
  client.sessions = [first, second, third];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1', 's2', 's3']);
  client.sessions = [first, second, third, agent('s4')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1', 's2', 's3']);
  workspace.setGroupingMode('machine');
  const groupedOrder = workspace.visibleRows().map((row) => row.id);
  assert.deepEqual(groupedOrder, ['s1', 's2', 's4', 's3']);
  client.sessions = [first, second, third, agent('s4'), agent('s5', { state: 'needs_you' })];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), groupedOrder);
});

test('external session selection updates and clears the left row selection', async () => {
  const first = agent('s1'), second = agent('s2');
  const { workspace, client } = await opened({ sessions: [first, second] });
  client.selected = agent('s2');
  workspace.toggleFold('state/idle');
  workspace.update(client);
  assert.equal(workspace.isSelected(second), true);
  client.selected = ResourceRow.from({ id: 'conversation-1', manager: 'work' }, 'conversation');
  workspace.update(client);
  assert.equal(workspace.selectedKey, '');
  assert.equal(workspace.isSelected(second), false);
  client.selected = agent('s1');
  await workspace.open(client);
  assert.equal(workspace.isSelected(first), true);
});

test('a detail reading freezes membership and refreezes after explicit filter changes', async () => {
  const first = agent('s1', { state: 'working', updated_at: '2026-11-11T10:00:00Z' });
  const second = agent('s2', { state: 'idle', status: 'idle', updated_at: '2026-11-11T09:00:00Z' });
  const { workspace, client } = await opened({ sessions: [first, second] });
  workspace.setReadingDetail(true);
  const latest = agent('s1', { state: 'idle', status: 'idle', updated_at: '2026-11-11T11:00:00Z' });
  workspace.toggleFold('state/idle');
  const added = agent('s3', { state: 'working', updated_at: '2026-11-11T12:00:00Z' });
  client.sessions = [latest, added];
  workspace.update(client, true);
  const frozen = workspace.groups.flatMap((group) => group.rows);
  assert.deepEqual(frozen.map((row) => row.id), ['s1'], 'the core folds idle state by default');
  assert.equal(frozen[0].state, 'idle');
  workspace.setQuickFilter('working');
  assert.deepEqual(workspace.groups.flatMap((group) => group.rows).map((row) => row.id), ['s3']);
  const updated = agent('s3', { state: 'idle', status: 'idle', updated_at: '2026-11-11T13:00:00Z' });
  const next = agent('s4', { state: 'working', updated_at: '2026-11-11T14:00:00Z' });
  client.sessions = [updated, next];
  workspace.update(client, true);
  assert.deepEqual(workspace.groups.flatMap((group) => group.rows).map((row) => row.id), ['s3']);
  assert.equal(workspace.visibleRows()[0].state, 'idle');

  workspace.setQuickFilter('all');
  const refrozen = workspace.visibleRows().map((row) => row.id);
  assert.deepEqual(refrozen, ['s4', 's3']);
  client.sessions = [updated, next, agent('s5', { state: 'needs_you', updated_at: '2026-11-11T15:00:00Z' })];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), refrozen);
});

test('an empty list also stays frozen until an explicit filter recomputes it', async () => {
  const { workspace, client } = await opened({ sessions: [] });
  workspace.setReadingDetail(true);
  client.sessions = [agent('arrived')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows(), []);
  workspace.setQuickFilter('all');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['arrived']);
});

test('session actions refuse read-only, untrusted, and already unresolved paths', async () => {
  const readOnly = agent('readonly', { source_kind: 'codex_history', read_only_reason: 'History' });
  const pending = agent('pending');
  const { workspace, state, client } = await opened({ sessions: [readOnly, pending], machines: [machine('m1', 'pending')] });
  assert.equal(workspace.canRename(readOnly), false);
  assert.equal(workspace.canClose(readOnly), false);
  assert.equal(await workspace.rename(readOnly, 'No', ''), false);
  client.machines = [machine()]; workspace.update(client);
  const path = '/api/v1/sessions/m1/pending/rename';
  state.blocked.add(path);
  assert.equal(await workspace.rename(pending, 'No', ''), false);
  assert.equal(workspace.error, 'pending_action');
  assert.equal(state.calls.some((call) => call.path === path), false);
  client.profile.manager = 'personal'; state.manager = 'personal'; state.scope = 'hub:personal'; state.generation++;
  assert.equal(await workspace.close(pending), false);
});

test('a scope change while reading pins prevents the old pairing from writing', async () => {
  const { workspace, state, client } = await opened();
  const entered = new Promise((resolve) => { state.entered = resolve; });
  let release;
  const waiting = new Promise((resolve) => { release = resolve; });
  state.request = async (call) => {
    if (call.path === '/api/v1/pins' && call.method === 'GET') {
      state.entered();
      await waiting;
      return { spaces: ['m1/space'], sessions: ['m1/s1'] };
    }
    return undefined;
  };
  const pending = workspace.togglePin(client.sessions[0]);
  await entered;
  client.profile.manager = 'personal'; state.manager = 'personal'; state.scope = 'hub:personal'; state.generation++;
  release();
  assert.equal(await pending, false);
  assert.equal(state.calls.some((call) => call.method === 'PUT' && call.path === '/api/v1/pins'), false);
});

test('list filters and selection restore within their manager scope', async () => {
  const { workspace, state, client } = await opened();
  workspace.setQuickFilter('working');
  workspace.setGroupingMode('space');
  await new Promise((resolve) => setTimeout(resolve, 220));
  assert.equal(JSON.parse(state.views.get('hub:work:agent-workspace')).quick, 'working');
  assert.equal(JSON.parse(state.views.get('hub:work:agent-workspace')).grouping, 'space');
  state.scope = 'hub:personal'; state.manager = 'personal'; client.profile.manager = 'personal'; state.generation++;
  state.views.set('hub:personal:agent-workspace', JSON.stringify({ quick: 'needsYou', grouping: 'space', machine: 'p1', search: 'review', selected: '["session","personal","p1","p1-s1"]' }));
  await workspace.open(client);
  assert.equal(workspace.quickFilter, 'needsYou');
  assert.equal(workspace.groupingMode, 'space');
  assert.equal(workspace.machineFilter, 'p1');
  assert.equal(workspace.search, 'review');
  assert.equal(workspace.selectedKey, '["session","personal","p1","p1-s1"]');
});

test('the first loaded live catalog populates a cold Agents list before membership is frozen',async()=>{
 const {client}=clientFixture({sessions:[]});client.sessionsReady=false;const workspace=new AgentWorkspace();await workspace.open(client,true);assert.equal(workspace.groups.length,0);
 client.sessions=[agent('first')];client.sessionsReady=true;workspace.update(client,true);assert.deepEqual(workspace.groups.flatMap(g=>g.rows.map(r=>r.id)),['first']);
 client.sessions=[agent('later'),agent('first',{state:'needs_you'})];workspace.update(client,true);assert.deepEqual(workspace.groups.flatMap(g=>g.rows.map(r=>r.id)),['first']);
});

test('manager-qualified machine filters keep same-Hub same-ID owners independent', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub-a'; a.client.profile.manager = 'alpha'; a.state.manager = 'alpha';
  b.client.profile.id = 'hub-a'; b.client.profile.manager = 'beta'; b.state.manager = 'beta'; b.state.scope = 'hub-a:beta';
  a.client.directoryReady = true; a.client.directoryOwners = () => [a.client, b.client];
  a.client.machines[0].manager = 'alpha'; b.client.machines[0].manager = 'beta';
  a.client.directoryOwnerFor = (row) => row.manager === 'beta' ? b.client : a.client;
  const first = agent('shared-id', {manager: 'alpha'}), second = agent('shared-id', {manager: 'beta'});
  first.group = 'hub-a'; second.group = 'hub-a';
  a.client.directorySessions = [first, second];
  const w = new AgentWorkspace(); await w.open(a.client);
  w.setGroupingMode('machine');
  assert.deepEqual(w.groups.map((group) => group.id).sort(), ['machine/hub-a|alpha|m1', 'machine/hub-a|beta|m1']);
  assert.equal(w.groups.every((group) => group.rows.length === 1), true);
  await w.setMachineFilter(w.machineIdentity(b.client.machines[0]));
  assert.deepEqual(w.visibleRows().map((row) => row.manager), ['beta']);
});

test('per-Hub pins and allowed exceptions cannot affect another Hub with the same short key', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub-a'; b.client.profile.id = 'hub-b'; b.state.scope = 'hub-b:work';
  a.state.prefs.pins.sessions = ['m1/shared']; b.state.prefs.pins.sessions = [];
  a.state.prefs.mutes.spaces = ['m1/project']; b.state.prefs.mutes.allowed = ['m1/shared'];
  const first = agent('shared', {space_name: 'project'}), second = agent('shared', {space_name: 'project'});
  first.group = 'hub-a'; second.group = 'hub-b';
  a.client.directoryReady = true; a.client.directorySessions = [first, second];
  a.client.directoryOwners = () => [a.client, b.client]; a.client.directoryOwnerFor = (row) => row.group === 'hub-b' ? b.client : a.client;
  const w = new AgentWorkspace(); await w.open(a.client);
  assert.equal(w.isPinned(first), true); assert.equal(w.isPinned(second), false);
  assert.equal(w.isMuted(first), true); assert.equal(w.isMuted(second), false);
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows[0].group, 'hub-a');
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows.some((row) => row.group === 'hub-b'), false);
});

test('same-Hub manager preferences and held state remain isolated', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub'; a.client.profile.manager = 'alpha'; a.state.manager = 'alpha';
  b.client.profile.id = 'hub'; b.client.profile.manager = 'beta'; b.state.manager = 'beta'; b.state.scope = 'hub:beta';
  a.state.prefs.pins.sessions = ['m1/shared']; b.state.prefs.pins.sessions = [];
  a.state.prefs.mutes.allowed = ['m1/shared']; b.state.prefs.mutes.spaces = ['m1/project'];
  a.client.machines[0].manager = 'alpha'; b.client.machines[0].manager = 'beta';
  const first = agent('shared', {manager: 'alpha', state: 'idle', space_name: 'project'});
  const second = agent('shared', {manager: 'beta', state: 'idle', space_name: 'project'});
  first.group = 'hub'; second.group = 'hub';
  a.client.directoryReady = true; a.client.directorySessions = [first, second];
  a.client.directoryOwners = () => [a.client, b.client]; a.client.directoryOwnerFor = (row) => row.manager === 'beta' ? b.client : a.client;
  const w = new AgentWorkspace(); await w.open(a.client);
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows[0].manager, 'alpha');
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows.length, 1);
  w.selectRow(first);
  assert.equal(w.isPinned(first), true); assert.equal(w.isPinned(second), false);
  assert.equal(w.isMuted(second), true); assert.equal(w.isMuted(first), false);
});

test('legacy empty manager machine filters keep the Hub group', async () => {
  const {workspace, client} = await opened();
  client.profile.id = 'hub';
  client.sessions[0].group = 'hub';
  await workspace.setMachineFilter(JSON.stringify(['hub', '', 'm1']));
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1']);
});

function switchedHubs() {
  const home = clientFixture(), work = clientFixture();
  home.client.profile.id = 'home'; home.state.scope = 'home:work';
  work.client.profile.id = 'work'; work.state.scope = 'work:work';
  const fixtures = [home, work];
  const rows = fixtures.map(({client}) => {
    const row = agent('shared-agent', {title: client.profile.id});
    row.group = client.profile.id; client.machines[0].group = row.group;
    return row;
  });
  for (const {client} of fixtures) {
    client.directoryReady = true; client.directorySessions = rows;
    client.directoryOwners = () => fixtures.map((fixture) => fixture.client);
    client.directoryOwnerFor = (row) => row.group === 'home' ? home.client : work.client;
    client.features = ['agent_session_archives'];
  }
  work.state.views = home.state.views;
  return {home, work};
}

test('All Hubs retains a deliberately chosen foreign machine across reloads and identical machine IDs', async () => {
  const {home, work} = switchedHubs();
  const workspace = new AgentWorkspace();
  await workspace.open(home.client);
  assert.deepEqual(workspace.visibleRows().map((row) => row.group).sort(), ['home', 'work']);
  const selected = workspace.machineIdentity(work.client.machines[0]);
  await workspace.setMachineFilter(selected);
  assert.deepEqual(workspace.visibleRows().map((row) => row.group), ['work']);
  await new Promise((resolve) => setTimeout(resolve, 240));
  const restored = new AgentWorkspace();
  await restored.open(home.client);
  assert.equal(restored.currentHubOnly, false);
  assert.equal(restored.machineFilter, selected);
  assert.deepEqual(restored.visibleRows().map((row) => row.group), ['work']);
});

test('one-Hub mode follows the current pairing at first appearance, external switch and cold start', async () => {
  const {home, work} = switchedHubs();
  const workspace = new AgentWorkspace();
  await workspace.open(home.client);
  const oldMachine = workspace.machineIdentity(home.client.machines[0]);
  await workspace.setMachineFilter(oldMachine);
  await workspace.setCurrentHubOnly(true);
  assert.equal(workspace.machineFilter, oldMachine);
  assert.deepEqual(workspace.visibleRows().map((row) => row.group), ['home']);
  work.state.views.set('work:work:agent-workspace', JSON.stringify({machine: oldMachine}));
  workspace.hide();
  await workspace.open(work.client);
  assert.equal(workspace.currentHubOnly, true);
  assert.equal(workspace.machineFilter, '');
  assert.deepEqual(workspace.visibleRows().map((row) => row.group), ['work']);
  const restored = new AgentWorkspace();
  await restored.open(work.client);
  assert.equal(restored.currentHubOnly, true);
  assert.equal(restored.machineFilter, '');
  assert.deepEqual(restored.visibleRows().map((row) => row.group), ['work']);
  await restored.setCurrentHubOnly(false);
  assert.deepEqual(restored.visibleRows().map((row) => row.group).sort(), ['home', 'work']);
});

test('narrowing a live archive invalidates foreign rows and ignores its older in-flight response', async () => {
  const {home, work} = switchedHubs();
  const workspace = new AgentWorkspace();
  await workspace.open(home.client);
  home.state.history = [JSON.parse(agent('home-archived').raw)];
  let release, started;
  const requested = new Promise((resolve) => started = resolve);
  work.state.request = async ({path}) => {
    if (!path.startsWith('/api/v1/sessions/archived')) return;
    started(); return new Promise((resolve) => release = resolve);
  };
  const first = workspace.setHistory(true);
  await requested;
  await workspace.setCurrentHubOnly(true);
  release({sessions: [JSON.parse(agent('foreign-archived').raw)]});
  await first;
  assert.deepEqual(workspace.visibleRows().map((row) => [row.group, row.id]), [['home', 'home-archived']]);
  assert.equal(work.state.calls.filter(({path}) => path.startsWith('/api/v1/sessions/archived')).length, 1);
});

test('All Hubs archives target the selected owner even when its computer has left the live inventory', async () => {
  const {home, work} = switchedHubs();
  const workspace = new AgentWorkspace();
  work.state.history = [JSON.parse(agent('old-work', {machine_id:'retired'}).raw)];
  work.client.machines = [];
  await workspace.open(home.client);
  const qualified = JSON.stringify(['work', 'work', 'retired']);
  await workspace.setMachineFilter(qualified); await workspace.setHistory(true);
  assert.equal(workspace.machineFilter, qualified);
  assert.deepEqual(workspace.visibleRows().map((row) => [row.group, row.id]), [['work', 'old-work']]);
  assert.ok(work.state.calls.some(({path}) => path === '/api/v1/sessions/archived?machine_id=retired&manager=work'));
  assert.ok(!home.state.calls.some(({path}) => path.startsWith('/api/v1/sessions/archived')));
  await workspace.setMachineFilter('work|work|retired');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['old-work']);
});

test('time-bounded searches use caller time and group titles remain visible', async () => {
  const {workspace} = await opened({sessions: [agent('s1', {updated_at: new Date().toISOString()})]});
  workspace.setSearch('since:24h');
  assert.equal(workspace.visibleRows().length, 1);
  workspace.setGroupingMode('machine');
  assert.equal(workspace.groups[0].machineName, 'Computer m1');
  workspace.setGroupingMode('space');
  assert.equal(workspace.groups[0].machineName, 'repo');
});

test('native failure retains the same displayed rows and action authority', async () => {
  const {workspace, client} = await opened();
  const original = client.sessions[0], ticket = workspace.actionTicket(original);
  workspace.adapter = {project() {throw new Error('native unavailable');}};
  client.sessions = [agent('s1', {title: 'Replacement target'})];
  workspace.update(client);
  assert.equal(workspace.visibleRows()[0], original);
  assert.equal(ticket(), true);
  assert.equal(workspace.error, 'failed');
});

test('a pinned offline row survives frozen reading and current fold visibility', async () => {
  const pinned = agent('pinned', {state: 'offline', status: 'offline'});
  const {workspace, client} = await opened({sessions: [pinned]});
  client.sessions[0].state = 'offline';
  assert.equal(await workspace.togglePin(pinned), true);
  workspace.setReadingDetail(true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['pinned']);
  assert.equal(workspace.groups.some((group) => group.id === 'pinned'), true);
});

test('frozen reconstruction applies core visible rows, not every frozen idle row', async () => {
  const idle = agent('idle', {state: 'idle', status: 'idle'});
  const {workspace} = await opened({sessions: [idle]});
  workspace.setReadingDetail(true);
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['idle']);
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows(), []);
});

test('archived history respects folded groups before and after expansion', async () => {
  const archived = agent('archive-folded', {archived: true, state: 'offline', status: 'offline'});
  const {workspace, client} = await opened({history: [archived]});
  client.features = ['agent_session_archives'];
  await workspace.setHistory(true);
  assert.deepEqual(workspace.visibleRows(), []);
  assert.ok(workspace.groups.some((group) => group.totalRowCount > 0 && group.folded));
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['archive-folded']);
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows(), []);
});

test('frozen reading drops deleted and offline rows without admitting new members', async () => {
  const first = agent('keep'), second = agent('offline');
  const {workspace, client} = await opened({sessions: [first, second]});
  workspace.setReadingDetail(true);
  client.sessions = [agent('keep', {state: 'idle'}), agent('new-member')];
  workspace.toggleFold('state/idle');
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['keep']);
  client.sessions = [agent('keep', {state: 'offline'}), agent('new-member')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows(), []);
});

test('forgetting a Hub refolds frozen groups without resurrecting rows', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub-a'; b.client.profile.id = 'hub-b'; b.state.scope = 'hub-b:work';
  const first = agent('first'), second = agent('second');
  first.group = 'hub-a'; second.group = 'hub-b';
  a.client.directoryReady = true; a.client.directorySessions = [first, second];
  a.client.directoryOwners = () => [a.client, b.client]; a.client.directoryOwnerFor = (row) => row.group === 'hub-b' ? b.client : a.client;
  const w = new AgentWorkspace(); await w.open(a.client, true);
  w.toggleFold('state/working');
  w.toggleFold('state/working');
  w.forgetHub('hub-b');
  assert.deepEqual(w.visibleRows().map((row) => row.group), ['hub-a']);
  w.toggleFold('state/working');
  assert.ok(w.visibleRows().every((row) => row.group !== 'hub-b'));
});

test('archived sessions get a registry entry for machine grouping', async () => {
  const a = clientFixture({history: [agent('archived', {archived: true, state: 'working', machine_id: 'retired'})]});
  a.client.features = ['agent_session_archives'];
  const w = new AgentWorkspace(); await w.open(a.client);
  await w.setHistory(true);
  w.setGroupingMode('machine');
  assert.ok(w.groups.some((group) => group.id.includes('retired')));
  assert.equal(w.groups[0].machineName, 'retired');
});

test('the whole group header announces the section, count and next fold action; idle expansion retains its count', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  const header=source.slice(source.indexOf('  @Builder groupHeader('),source.indexOf('  private rowStatus('));
  assert.match(source,/return tr\(STATE_LABELS\[group\.id\] \|\| ''\) \|\| group\.machineName/);
  assert.match(header,/Button\(\{ type: ButtonType.Normal \}\)[\s\S]*Text\(this\.groupTitle\(binding\.value\)\)[\s\S]*String\(binding\.value\.totalRowCount\)/);
  assert.match(header,/accessibilityText\(this\.groupTitle\(binding\.value\)[\s\S]*binding\.value\.totalRowCount[\s\S]*tr\(binding\.value\.folded \? 'agent_expand' : 'agent_collapse'\)/);
  assert.match(header,/\.onClick\(\(\) => this\.workspace\.toggleFold\(binding\.value\.id\)\)\.id\('agents\.group\.toggle.'/);
  assert.match(source,/String\(binding\.value\.totalRowCount\s*-\s*binding\.value\.rows\.length\)\s*\+\s*' '\s*\+\s*tr\('agent_group_more_idle'\)/);
  assert.ok(source.includes('this.workspace.toggleIdleRows(binding.value.id)'));
});

test('merged rows with identical runtime ids isolate pin, mute and mutation ownership',async()=>{
 const a=clientFixture(),b=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';
 const first=agent('same'),second=agent('same');first.group='hub-a';second.group='hub-b';a.client.sessions=[first];b.client.sessions=[second];
 a.client.directoryReady=true;a.client.directorySessions=[first,second];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:a.client;
 a.client.groupName=row=>row.group==='hub-b'?'Second Hub':'First Hub';
 const w=new AgentWorkspace();await w.open(a.client);assert.equal(w.visibleRows().length,2);
 b.state.calls=[];a.state.calls=[];assert.equal(await w.togglePin(second),true);
 assert.equal(b.state.calls.some(c=>c.method==='PUT'&&c.path==='/api/v1/pins'),true);assert.equal(a.state.calls.length,0);
 assert.equal(w.isPinned(second),true);assert.equal(w.isPinned(first),false);
 b.state.calls=[];assert.equal(await w.rename(second,'Renamed',''),true);assert.equal(b.state.calls[0].path,'/api/v1/sessions/m1/same/rename');assert.equal(a.state.calls.length,0);
 w.selectExternal(second);w.update(a.client);assert.equal(w.isSelected(second),true);assert.equal(w.isSelected(first),false);
 assert.ok(w.rowMetadata(second)[0].includes('Second Hub'));
});
test('a mutation journal admission is revoked when its captured Hub goes away',async()=>{
 const f=clientFixture(),w=new AgentWorkspace();await w.open(f.client);let dispatched=0;
 f.repo.call=async(path,method,body,op,admit)=>{f.state.generation++;if(admit&&!admit())throw Error('request_updated');dispatched++;return{};};
 assert.equal(await w.rename(f.client.sessions[0],'changed',''),false);assert.equal(dispatched,0);
});

test('a background directory load cannot bypass established reading membership',async()=>{const first=agent('frozen-a'),second=agent('frozen-b'),{workspace,client}=await opened({sessions:[first,second]});workspace.setReadingDetail(true);client.directoryReady=true;client.directoryLoading=true;workspace.toggleFold('state/idle');client.directorySessions=[agent('new-member'),agent('frozen-a',{state:'idle'}),second];workspace.update(client,true);assert.deepEqual(workspace.visibleRows().map(row=>row.id),['frozen-a','frozen-b']);assert.equal(workspace.visibleRows()[0].state,'idle');});
test('forgetting a foreign Hub removes frozen rows, history, selection and action authority',async()=>{const a=clientFixture(),b=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';const first=agent('same'),second=agent('same');first.group='hub-a';second.group='hub-b';a.client.directoryReady=true;a.client.directorySessions=[first,second];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:a.client;const w=new AgentWorkspace();await w.open(a.client,true);w.selectExternal(second);w.historyRows=[second];const ticket=w.actionTicket(second);assert.equal(ticket(),true);w.forgetHub('hub-b');assert.deepEqual(w.visibleRows().map(row=>row.group),['hub-a']);assert.equal(w.selectedKey,'');assert.equal(w.historyRows.length,0);assert.equal(ticket(),false);});
test('an archive-only foreign owner is included and its open confirmation cannot survive reconnect',async()=>{const a=clientFixture(),b=clientFixture({history:[agent('archived',{archived:true,state:'offline'})]});a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';b.client.features=['agent_session_archives'];b.client.sessions=[];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:a.client;const w=new AgentWorkspace();await w.open(a.client);assert.equal(w.historySupported(),true);await w.setHistory(true);assert.ok(b.state.calls.some(call=>call.path.startsWith('/api/v1/sessions/archived')));w.toggleFold('state/idle');assert.equal(w.visibleRows().length,1,'the archived offline Agent remains readable history');await w.setHistory(false);const row=a.client.sessions[0],ticket=w.actionTicket(row);assert.equal(ticket(),true);a.state.generation++;assert.equal(ticket(),false);});

const nativePageSource=readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets',import.meta.url),'utf8');
function nativePageMethod(name){const start=nativePageSource.search(new RegExp('^  private (?:async )?'+name+'\\(','m'));assert.ok(start>=0);const body=nativePageSource.indexOf('{',start);let depth=1,quote='',escape=false,at=body+1;for(;at<nativePageSource.length&&depth;at++){const c=nativePageSource[at];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote='';}else if(c==='"'||c==="'"||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}return nativePageSource.slice(start,at);}
const AgentNativeActions=new Function('tr','Wire','return '+stripTypeScriptTypes('class AgentNativeActions{'+['openRename','saveRename','confirmClose','confirmDelete'].map(nativePageMethod).join('\n')+'}'))(key=>key,(await import('../entry/src/main/ets/models/Wire.ets')).Wire);
test('native rename, close and delete retain the owner generation captured when the editor or confirmation opened',async()=>{
 for(const action of ['rename','close','delete']){
  const a=clientFixture(),b=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';const row=agent('foreign',{state:'offline',status:'offline'});row.group='hub-b';a.client.directoryReady=true;a.client.directorySessions=[row];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=()=>b.client;
  const workspace=new AgentWorkspace();await workspace.open(a.client);b.state.calls=[];const page=new AgentNativeActions();page.workspace=workspace;page.model=a.client;page.active=true;let dialog;page.getUIContext=()=>({showAlertDialog:value=>dialog=value});
  if(action==='rename'){page.openRename(row);page.renameTitle='Changed';b.state.generation++;await page.saveRename();assert.equal(page.editorOpen,false);}
  else{page[action==='close'?'confirmClose':'confirmDelete'](row);b.state.generation++;dialog.secondaryButton.action();await new Promise(resolve=>setImmediate(resolve));}
  assert.equal(b.state.calls.some(call=>call.method!=='GET'),false);
 }
});

test('forget cancels an aggregate history pass even after that Hub rows were collected',async()=>{const a=clientFixture(),b=clientFixture({history:[agent('b-history')]}),c=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';c.client.profile.id='hub-c';b.state.scope='hub-b:work';c.state.scope='hub-c:work';b.client.features=c.client.features=['agent_session_archives'];a.client.directoryOwners=()=>[a.client,b.client,c.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:row.group==='hub-c'?c.client:a.client;let release,started;const gate=new Promise(r=>release=r),entered=new Promise(r=>started=r);c.state.request=async call=>{if(call.path.startsWith('/api/v1/sessions/archived')){started();return gate;}};const w=new AgentWorkspace();await w.open(a.client);const history=w.setHistory(true);await entered;w.forgetHub('hub-b');release({sessions:[agent('c-history').data()]});await history;assert.equal(w.loading,false);assert.equal(w.historyRows.some(row=>row.group==='hub-b'),false);assert.equal(w.visibleRows().some(row=>row.group==='hub-b'),false);});
test('turning history off invalidates its pending response before restoring the live list',async()=>{const f=clientFixture();f.client.features=['agent_session_archives'];let release,started;const gate=new Promise(r=>release=r),entered=new Promise(r=>started=r);f.state.request=async call=>{if(call.path.startsWith('/api/v1/sessions/archived')){started();return gate;}};const w=new AgentWorkspace();await w.open(f.client);const old=w.setHistory(true);await entered;await w.setHistory(false);release({sessions:[agent('stale-archive').data()]});await old;assert.equal(w.history,false);assert.deepEqual(w.visibleRows().map(row=>row.id),['s1']);assert.equal(w.loading,false);});

test('detail activation while opening cannot freeze an empty list before its saved view loads', async () => {
  const { client } = clientFixture(), workspace = new AgentWorkspace();
  const opening = workspace.open(client, true);
  workspace.setReadingDetail(true);
  await opening;
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows().map(row => row.id), ['s1']);
  client.sessions = [agent('s1', { state: 'idle' }), agent('new-later')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map(row => row.id), ['s1']);
  assert.equal(workspace.visibleRows()[0].state, 'idle');
  workspace.hide();
});


test('leaving a retained list revokes an in-flight pin while keeping grouping preferences', async () => {
 const {workspace,client,state}=await opened();workspace.setGroupingMode('machine');
 let release,entered;const ready=new Promise(r=>entered=r),gate=new Promise(r=>release=r);
 state.request=async call=>{if(call.path==='/api/v1/pins' && call.method==='GET'){entered();await gate;return {spaces:[],sessions:[]};}};
 const pending=workspace.togglePin(client.sessions[0]);await ready;workspace.revokeRowActions();release();
 assert.equal(await pending,false);assert.equal(state.calls.some(c=>c.method==='PUT'),false);assert.equal(workspace.groupingMode,'machine');assert.equal(workspace.isBoundTo(client),true);
});


test('a retired pin completion cannot release the busy lock of a new list operation', async () => {
 const {workspace,client,state}=await opened({sessions:[agent('s1'),agent('s2')]});
 const releases=[],arrivals=[];state.request=async call=>{if(call.path==='/api/v1/pins'&&call.method==='GET'){const n=arrivals.length;let release;const gate=new Promise(r=>release=r);releases.push(release);arrivals.push(n);await gate;return {spaces:[],sessions:[]};}};
 const old=workspace.togglePin(client.sessions[0]);while(arrivals.length<1)await new Promise(r=>setImmediate(r));
 workspace.revokeRowActions();const fresh=workspace.togglePin(client.sessions[1]);while(arrivals.length<2)await new Promise(r=>setImmediate(r));
 releases[0]();assert.equal(await old,false);assert.equal(workspace.busy,true,'old finally must not release new operation');
 releases[1]();assert.equal(await fresh,true);assert.equal(workspace.busy,false);assert.equal(state.calls.filter(c=>c.method==='PUT').length,1);
});

test('a Projects query uses the same real Hub and machine source inventory without changing the current Agents view', async () => {
  const input=(group,id,pane,title)=>{const row=agent(id,{title,pane_id:pane,manager:'work',cwd:'/other',state:'idle'});row.group=group;return row;};
  const one=input('one','shared','w:p1','weather'), two=input('two','shared','botmux:p1','weather');
  const other=input('one','outside','w:p3','other'), native=input('one','orca','orca:p1','weather');
  const masked=input('two','else','w:p2','weather');
  const {client}=clientFixture();client.directoryMachines=[];
  const workspace=new AgentWorkspace(undefined,'repo');
  workspace.search='other tab';workspace.quickFilter='unread';workspace.machineFilter='unrelated';
  const inventory=[one,two,other,native,masked], filter=JSON.stringify(['one','work',one.machine]);
  const query=(quick,machineFilter='')=>workspace.repositoryQuery(client,inventory,'weather',quick,machineFilter);
  const terminal=query('terminal',filter);
  assert.deepEqual(terminal.filtered,['one|work|'+one.machine+'|shared']);
  assert.equal(terminal.filter_counts.terminal,1);assert.equal(terminal.filter_counts.feishu,0);
  assert.deepEqual(new Set(terminal.queryMatches),new Set(['one|work|'+one.machine+'|shared','one|work|'+native.machine+'|orca']));
  const lark=query('feishu');
  assert.deepEqual(lark.filtered,['two|work|'+two.machine+'|shared']);
  assert.equal(lark.filter_counts.terminal,2);assert.equal(lark.filter_counts.feishu,1);
  assert.deepEqual([workspace.search,workspace.quickFilter,workspace.machineFilter],['other tab','unread','unrelated']);
});

function projectSearchMethods(names, schedule=setTimeout) {
  const pageSource = readFileSync(new URL('../entry/src/main/ets/components/ProjectsPage.ets', import.meta.url), 'utf8');
  const methods = names.map(name=>pageSource.match(new RegExp('^  private '+name+'\\([\\s\\S]+?^  \\}', 'm'))[0]);
  return new Function('Wire','setTimeout','return '+stripTypeScriptTypes('class Page {'+methods.join('\n')+'}'))(Wire,schedule);
}

test('project grouping changes the native result groups and keeps its machine, query and quick filter separate from Agents', async () => {
  const Page=projectSearchMethods(['selectSearchGrouping','syncSearchGroups']);
  const make = (group, id, title, machine, state='working') => {
    const row=agent(id,{title,machine_id:machine,state,status:state,agent:'claude',space_name:'weather team'});row.group=group;return row;
  };
  const chosen=make('one','shared','Weather notes','m1'), otherMachine=make('one','far','Weather notes','m2');
  const otherQuery=make('one','off','Summary','m1'), otherState=make('one','idle','Weather notes','m1','idle');
  otherQuery.raw=JSON.stringify({...otherQuery.data(),space_name:'documentation'});
  const foreign=make('two','shared','Weather notes','m1'), sources=[chosen,otherMachine,otherQuery,otherState,foreign];
  const own=clientFixture({sessions:[chosen],machines:[machine('m1'),machine('m2')]}), peer=clientFixture({sessions:[foreign]});
  own.client.profile.id='one';peer.client.profile.id='two';own.client.directoryOwners=()=>[own.client,peer.client];
  own.client.directoryOwnerFor=row=>row.group==='two'?peer.client:own.client;
  const standalone=new AgentWorkspace(undefined,'repo');await standalone.open(own.client);
  standalone.setSearch('other tab');standalone.setQuickFilter('unread');await standalone.setMachineFilter('m2');
  const projected=new AgentWorkspace(undefined,'repo',false);await projected.open(own.client);
  const machineKey=JSON.stringify(['one','work','m1']);
  const {AgentDecisionQueue}=await import('../entry/src/main/ets/models/AgentDecisionQueue.ets');
  const page=Object.assign(new Page(),{model:own.client,groupedSearchRows:()=>{},spaceDecisions:new AgentDecisionQueue(),searchMode:true,quick:'working',
    searchWorkspace:standalone,searchGroups:projected,searchTasks:{supportsTodos:false,supportsDirections:false},
    directorySearchRows:sources,catalog:{browser:{query:'weather',machine:machineKey,searchGrouping:'repo'}}});
  for(const mode of ['state','machine','repo','recent','state']){
    page.selectSearchGrouping(mode);
    assert.equal(projected.groupingMode,mode);assert.equal(standalone.groupingMode,mode);
    assert.deepEqual([page.catalog.browser.query,page.quick,page.catalog.browser.machine],['weather','working',machineKey]);
    assert.deepEqual([projected.search,projected.quickFilter,projected.machineFilter],['weather','working',machineKey]);
    assert.deepEqual([standalone.search,standalone.quickFilter,standalone.machineFilter],['other tab','unread','m2']);
    assert.deepEqual(projected.unpinnedRows.map(row=>row.key()),[chosen.key()]);
    if(mode==='state')assert.deepEqual(projected.visibleRows().map(row=>row.key()),[chosen.key()]);
    if(mode==='machine')assert.ok(projected.groups.some(group=>group.id.startsWith('machine/')));
  }
  page.selectSearchGrouping('direction');
  assert.equal(projected.groupingMode,'state');
  const previous=projected.visibleRows()[0];projected.selectRow(previous);
  page.syncSearchGroups();assert.equal(projected.isSelected(previous),true);
  await new Promise(resolve=>setTimeout(resolve,220));
  const saved=JSON.parse(own.state.views.get(own.state.scope+':agent-workspace'));
  assert.deepEqual([saved.search,saved.quick,saved.machine,saved.grouping],['other tab','unread','m2','state']);
  projected.hide();page.syncSearchGroups();assert.equal(projected.isBoundTo(own.client),false);assert.deepEqual(projected.visibleRows(),[]);
});

test('a Projects search uses one decision snapshot for its query count, grouped rows and quick-answer admission across Hubs', async () => {
  const {AgentDecisionQueue}=await import('../entry/src/main/ets/models/AgentDecisionQueue.ets');
  const {projectSearchResult}=await import('../entry/src/main/ets/models/ProjectSearch.ets');
  const cases=JSON.parse(readFileSync(new URL('../../spec/fixtures/needs-you/cases.json',import.meta.url))).cases;
  const dataset=name=>cases.find(item=>item.name===name);
  const terminal=dataset('a_routed_terminal_question_is_held');
  const mixed=dataset('a_supervisor_held_card_beside_an_open_one_leaves_the_row');
  function owner(group,fixture){
    const rows=fixture.input.sessions.map(data=>{const row=agent(data.session_id,{...data,manager:'work'});row.group=group;return row;});
    const guest=clientFixture({sessions:rows});
    guest.client.profile.id=group;
    guest.client.canApproveSession=()=>true;
    guest.cards=structuredClone(fixture.input.interactions);
    guest.writes=[];
    guest.repo.decisions=async()=>structuredClone(guest.cards);
    guest.repo.resolve=async(card,_option,_values,_op,admit)=>{
      assert.equal(admit(),true);guest.writes.push(card.id);guest.cards=guest.cards.filter(value=>value.id!==card.id);
    };
    guest.client.directoryMachines=[];
    return guest;
  }
  const primary=owner('primary',mixed),peer=owner('peer',terminal),root=primary.client;
  root.directoryOwners=()=>[root,peer.client];
  root.directoryOwnerFor=row=>row.group==='peer'?peer.client:row.group==='primary'?root:undefined;
  const sessions=[...root.sessions,...peer.client.sessions];
  const queue=new AgentDecisionQueue(),workspace=new AgentWorkspace(undefined,'repo',false),scheduled=[];
  const Page=projectSearchMethods(['refreshSpaceDecisions','syncSearchGroups'],fn=>{scheduled.push(fn);return -1;});
  const page=Object.assign(new Page(),{page:'projects',model:root,searchMode:true,groupedSearchRows:()=>{},quick:'needsYou',
    catalog:{browser:{query:'',machine:'',searchGrouping:'state'}},spaceDecisions:queue,searchGroups:workspace,directorySearchRows:sessions});
  const reload=async()=>{page.refreshSpaceDecisions();await scheduled.shift()();};
  // Reading the remote directory can complete before opening the projection workspace.
  await reload();await workspace.open(root);page.syncSearchGroups();
  const state=()=>workspace.groups.find(item=>item.id==='state/needs')?.rows.map(row=>row.id).sort() || [];
  const counted=()=>projectSearchResult(root,sessions,workspace.repositoryQuery(root,sessions,'','needsYou',''),page.searchCards,'','needsYou',
    row=>[row.group,row.manager,row.machine,row.id].join('|')).counts.needsYou;
  assert.deepEqual(state(),['p']);assert.equal(workspace.filterCounts.needsYou,1);assert.equal(counted(),1);
  assert.deepEqual(queue.forSession(root,sessions[0]).map(card=>card.request.id),mixed.expected.rows[0].cards);
  assert.deepEqual(queue.forSession(root,sessions[1]),[]);
  const userQuestion=queue.forSession(root,sessions[0])[0];
  assert.equal(queue.enabled(root,sessions[0],userQuestion),true);assert.deepEqual(userQuestion.quickOptions(),['8080','9090']);
  // The secondary Hub can escalate while the primary does not change.
  peer.cards[0]={...peer.cards[0],kind:'permission',delegating:false,tool_name:'Read'};
  await reload();
  assert.deepEqual(state(),['p','t']);assert.equal(workspace.filterCounts.needsYou,2);assert.equal(counted(),2);
  const open=queue.forSession(root,sessions[1])[0];
  assert.deepEqual(open.quickOptions(),['allow','deny']);
  peer.cards[0].delegating=true;
  await queue.answer(root,sessions[1],open,'allow','stale-preview',workspace.actionTicket(sessions[1]));
  assert.deepEqual(peer.writes,[]);assert.deepEqual(primary.writes,[]);
  // The answer's own authoritative re-read has to update both presentations even without a push event.
  assert.deepEqual(state(),['p']);assert.equal(counted(),1);
  assert.deepEqual(queue.forSession(root,sessions[1]),[]);assert.equal(queue.enabled(root,sessions[1],open),false);
  peer.cards[0].delegating=false;await reload();
  const handed=queue.forSession(root,sessions[1])[0];
  assert.equal(queue.enabled(root,sessions[1],handed),true);
  await queue.answer(root,sessions[1],handed,'allow','fresh-owner',workspace.actionTicket(sessions[1]));
  assert.deepEqual(peer.writes,['ix-terminal']);assert.deepEqual(primary.writes,[]);
  assert.deepEqual(queue.forSession(root,sessions[1]),[]);
  const listSource=readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets',import.meta.url),'utf8');
  const lifecycle=['reloadDecisions','retireDecisions'].map(name=>listSource.match(new RegExp('^  private '+name+'\\([\\s\\S]+?^  \\}','m'))[0]);
  const List=new Function('return '+stripTypeScriptTypes('class List {'+lifecycle.join('\n')+'}'))();
  const rendered=Object.assign(new List(),{active:true,compactRoot:true,externalSearchControls:true,decisions:queue,workspace,model:root});
  rendered.reloadDecisions();rendered.retireDecisions();
  assert.equal(queue.forSession(root,sessions[0])[0].request.id,'ix-asked','returning to repository mode leaves the shared snapshot live');
  const last=page.searchCards;
  queue.clear();await queue.reload(root);
  assert.equal(page.searchCards,last,'an owner change or a closed page releases its snapshot subscription');
});
