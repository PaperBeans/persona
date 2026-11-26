import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === './ClientModel' && context.parentURL?.endsWith('/TodoWorkspace.ets'))
      return { url: 'persona-todo:client', shortCircuit: true };
    if (specifier === './HubRepository' && context.parentURL?.endsWith('/TodoWorkspace.ets'))
      return { url: 'persona-todo:repository', shortCircuit: true };
    if (specifier === '../platform/HubTransport' && context.parentURL?.endsWith('/TodoWorkspace.ets'))
      return { url: 'persona-todo:transport', shortCircuit: true };
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.'))
      return { url: new URL(specifier + '.ets', context.parentURL).href, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'persona-todo:client') return { format: 'module', source: 'export class ClientModel {}', shortCircuit: true };
    if (url === 'persona-todo:repository') return { format: 'module', source: `
      export function segment(value) { return encodeURIComponent(value); }
      export class HubRepository {}
    `, shortCircuit: true };
    if (url === 'persona-todo:transport') return { format: 'module', source: `
      export class HttpFailure extends Error {
        constructor(status, code, resultUnknown = false, resultPending = false) {
          super(code); this.status = status; this.code = code; this.resultUnknown = resultUnknown; this.resultPending = resultPending;
        }
      }
    `, shortCircuit: true };
    if (url.endsWith('.ets')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const { todoAgentStateLabel, todoDayKey, todoCalendarDays, todoCalendarDrop, todoDropDue, TodoWorkspace, TodoWorkspaceItem, TodoWorkspaceSpace, TodoWorkspaceSpaceChoice,
  todoWorkspaceStatus, relatedTodoMembers, todoMatchesDue } = await import('../entry/src/main/ets/models/TodoWorkspace.ets');
const { reconcileBoundRows } = await import('../entry/src/main/ets/models/RowBinding.ets');
const { ResourceRow } = await import('../entry/src/main/ets/models/Wire.ets');
const { TodoPhoneSections } = await import('../entry/src/main/ets/models/TodoPhoneSections.ets');
const { TodoHubCounts, TodoHubRow, TODO_HUB_SECTIONS } = await import('../entry/src/main/ets/models/TodoHubView.ets');
const { HttpFailure } = await import('persona-todo:transport');

function todo(id, fields = {}) {
  return {
    id, title: 'Todo ' + id, notes: '', manager: 'work', created_at: '2026-11-10T10:00:00Z',
    updated_at: '2026-11-10T10:00:00Z', agents: [], spaces: [], ...fields
  };
}

function session(id, state = 'offline', fields = {}) {
  return ResourceRow.from({ id, session_id: id, machine_id: 'm1', manager: 'work', title: 'Agent ' + id,
    state, status: state, ...fields }, 'session');
}

function machine(id = 'm1', fields = {}) {
  return ResourceRow.from({ id, manager: 'work', name: 'Computer ' + id, online: true, trust: '', ...fields }, 'machine');
}

test('compact task status follows the attached Agent and current review, independently of the list section', () => {
  const stamp='2026-11-22T12:00:00Z', later='2026-11-23T12:00:00Z';
  const accepted={state:'accepted',reviews:[{at:stamp,verdict:'accepted'}]};
  const neutral={state:'',reviews:[]};
  const linked={machine_id:'m1',session_id:'subject'};
  const result=(fields={},row=undefined) => new TodoPhoneSections(row ? [row] : [], Date.now())
    .rowContext(new TodoWorkspaceItem(todo('case',fields)));
  const scenarios=[
    [{},undefined,'not_started','not_started','todo_state_not_started'],
    [{agents:[linked]},session('subject','unread'),'unread','unread','agent_task_state_unread'],
    [{agents:[linked]},undefined,'followup','followup','assistant_feed_state_followup'],
    [{agents:[{...linked,released_at:stamp}]},session('subject','needs_you'),'not_started','not_started','todo_state_not_started'],
    [{agents:[linked]},session('subject','working',{background_working:true}),'waiting','waiting','waiting'],
    [{agents:[linked]},session('subject','idle',{work:{phase:'blocked',on:'external'}}),'waiting','waiting','waiting'],
    [{agents:[linked]},session('subject','idle',{starting:true,work:{phase:'blocked',on:'agent'}}),'working','working','phone_todo_section_working'],
    [{agents:[linked]},session('subject','needs_you',{starting:true,background_working:true}),'needs_you','needs_you','phone_todo_section_needs'],
    [{start_error:'cannot start',done_at:stamp},undefined,'failed','failed','phone_todo_did_not_start'],
    [{supervision:accepted},undefined,'not_started','done','phone_todo_row_accepted'],
    [{kind:'concern',supervision:accepted},undefined,'not_started','not_started','todo_state_not_started'],
    [{done_at:stamp,done_by:'review',supervision:{state:'cancelled'}},undefined,'done','done','phone_todo_row_cancelled'],
    [{done_at:stamp,done_by:'auto'},undefined,'done','done','phone_todo_row_auto_done'],
    [{done_at:stamp,done_by:'review'},undefined,'done','done','phone_todo_row_accepted'],
    [{supervision:{state:'needs_user',reviews:[{at:stamp}]},decision_review:{review_at:stamp,dismissal_id:'ack'}},undefined,'not_started','not_started','todo_state_not_started'],
    [{supervision:{state:'needs_user',reviews:[{at:later}]},decision_review:{review_at:stamp,dismissal_id:'ack'}},undefined,'not_started','needs_you','phone_todo_section_needs'],
    [{ask:{asked_at:stamp,outcome:'cannot'}},undefined,'needs_you','needs_you','phone_todo_section_needs'],
    [{delivery:{state:'live'},seen_at:stamp},undefined,'not_started','unread','phone_todo_section_delivered'],
    [{supervision:{state:'pending_review'}},undefined,'not_started','unread','phone_todo_section_delivered'],
    [{kind:'concern',delivery:{state:'live'}},undefined,'not_started','not_started','todo_state_not_started'],
  ];
  for (const [fields,session,base,shown,label] of scenarios) {
    const r=result(fields,session); assert.deepEqual([r.taskState,r.shownState,r.statusKey],[base,shown,label],JSON.stringify(fields));
  }
  const checks={state:'rework',round:2,done_when:['one','two'],reviews:[{checks:[{criterion:'one',met:false},{criterion:'extra',met:true}]},{checks:[{criterion:'one',met:true}]}]};
  const rework=result({supervision:checks}); assert.deepEqual([rework.reviewKey,rework.reviewTone,rework.reviewRound,rework.reviewCount],['phone_todo_review_rework','caution',2,'2/3']);
  const done=result({supervision:{...checks,state:'accepted',reviews:[...checks.reviews,{verdict:'accepted'}]}});
  assert.equal(done.reviewCount,'2/2'); assert.equal(done.reviewKey,'phone_todo_row_accepted');
  assert.equal(result({supervision:neutral}).reviewKey,'phone_todo_review_ongoing');
  assert.equal(result({kind:'concern',supervision:accepted}).reviewKey,'');
  assert.equal(result({done_at:stamp,supervision:accepted}).reviewKey,'');
});

test('phone rows show the most urgent Agent preview and independently name the working or most recently moved Agent', () => {
  const working = session('build', 'working', { model: 'claude-opus-5-5', effort: 'high', updated_at: '2026-11-10T08:00:00Z' });
  const needs = session('test', 'needs_you', { last_text: 'Choose a test framework.', updated_at: '2026-11-10T09:00:00Z' });
  const foreign = session('test', 'needs_you', { manager: 'different', last_text: 'An unrelated hub.' });
  const ended = session('released', 'needs_you', { last_text: 'No longer assigned.' });
  const item = fields => new TodoWorkspaceItem(todo('one', fields));
  const agents = ['build', 'test', 'released', 'unknown'].map(session_id => ({
    machine_id:'m1', session_id, ...(session_id === 'released' ? { released_at:'2026-11-10T09:01:00Z' } : {}),
  }));
  const row = item({agents, notes:'Original task notes.'});
  let directory = new TodoPhoneSections([working, needs, ended, foreign], Date.now());
  let context = directory.rowContext(row);
  assert.equal(context.lead, working);
  assert.equal(context.activeAgents, 3);
  assert.deepEqual([context.urgentState, context.summaryKind, context.summary], ['needs_you', 'session', needs.data().last_text]);
  const latest = session('build','idle',{state_since:'2026-11-10T10:00:00Z'});
  directory = new TodoPhoneSections([latest, needs], Date.now());
  assert.equal(directory.rowContext(row).lead, latest);
  const failure = directory.rowContext(item({agents, start_error:'rpc: connection closed', done_at:'2026-11-10T10:00:00Z'}));
  assert.deepEqual([failure.summaryKind,failure.summary], ['failure','rpc: connection closed']);
  assert.equal(directory.rowContext(item({agents, done_at:'2026-11-10T10:00:00Z', done_by:'auto', done_reason:'Merged.'})).summary, 'Merged.');
  assert.equal(directory.rowContext(item({agents, cancelled_at:'2026-11-10T10:00:00Z', cancelled_reason:'No longer requested.'})).summary, 'No longer requested.');
  assert.equal(directory.rowContext(item({agents:[{machine_id:'m1',session_id:'missing',title:'Unloaded agent'}]})).summary,'Unloaded agent');
  assert.equal(directory.rowContext(item({agents:[{machine_id:'m1',session_id:'missing',start_error:'Lost connection'}]})).summaryKind,'failure');
  directory = new TodoPhoneSections([foreign], Date.now());
  context = directory.rowContext(item({agents:[{machine_id:'m1',session_id:'test'}],notes:'Use the task notes.'}));
  assert.equal(context.lead, undefined);
  assert.deepEqual([context.activeAgents,context.summaryKind,context.summary],[1,'notes','Use the task notes.']);
  assert.equal(directory.rowContext(item({})).summaryKind,'empty');
});

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function makeHarness(options = {}) {
  const state = {
    manager: options.manager || 'work', group: options.group || 'profile-1', generation: 4,
    scope: 'hub:' + (options.manager || 'work'), connected: true, demo: false,
    todos: (options.todos || []).map((item) => structuredClone(item)),
    sessions: options.sessions || [], machines: options.machines || [machine()], conversations: options.conversations || [],
    calls: [], views: new Map(), blocked: new Set(), request: options.request, viewLoad: options.viewLoad
  };
  const repo = {
    async call(path, method = 'GET', body = {}) {
      const call = { path, method, body: structuredClone(body) };
      state.calls.push(call);
      if (state.request) return state.request(call, state);
      if (path.split('?')[0] === '/api/v1/todos' && method === 'GET') return { todos: structuredClone(state.todos) };
      if (path.split('?')[0] === '/api/v1/todos' && method === 'POST') {
        const item = todo('created-1', { ...body, created_at: '2026-11-11T10:00:00Z' });
        state.todos.unshift(item);
        return { todo: structuredClone(item) };
      }
      if (path.startsWith('/api/v1/todos/') && method === 'PATCH') {
        const id = decodeURIComponent(path.split('/')[4].split('?')[0]);
        const index = state.todos.findIndex((item) => item.id === id);
        const current = state.todos[index] || todo(id);
        const updated = { ...current, ...body, updated_at: '2026-11-11T10:01:00Z' };
        if (body.done === true) { updated.done_at = current.start_error ? '2026-11-11T10:01:00Z' : (current.done_at || '2026-11-11T10:01:00Z'); updated.start_error = ''; }
        if (body.done === false) { updated.done_at = null; updated.start_error = ''; updated.reopened_at = '2026-11-11T10:01:00Z'; }
        state.todos[index] = updated;
        return { todo: structuredClone(updated) };
      }
      if (path.startsWith('/api/v1/todos/') && method === 'DELETE') {
        const id = decodeURIComponent(path.split('/')[4].split('?')[0]);
        state.todos = state.todos.filter((item) => item.id !== id);
        return { ok: true };
      }
      return { ok: true };
    }
  };
  state.repo = repo;
  const client = {
    get connected() { return state.connected; },
    get demo() { return state.demo; },
    get sessions() { return state.sessions; },
    get machines() { return state.machines; },
    get conversations() { return state.conversations; },
    get knowledge() { return options.knowledge || []; },
    profile: { id: state.group, manager: state.manager },
    repo() { if (!state.connected) throw new Error('offline'); return state.repo; },
    scope() { return state.scope; },
    connectionGeneration() { return state.generation; },
    owns(generation, scope) { return generation === state.generation && scope === state.scope; },
    operationBlocked(path) { return state.blocked.has(path); },
    async loadView(key) {
      if (state.viewLoad) return state.viewLoad(key, state);
      return state.views.get(state.scope + ':' + key) || '';
    },
    viewRevision(){return state.epoch||0;},
    async saveView(key, value) { state.views.set(state.scope + ':' + key, value); },
    async viewFor(row) {
      if (row.manager === state.manager) return client;
      return state.otherClient;
    },
    todos: [], features: options.features || []
  };
  return { client, repo, state };
}

async function opened(options = {}) {
  const harness = makeHarness(options);
  const workspace = new TodoWorkspace();
  await workspace.open(harness.client);
  return { ...harness, workspace };
}

function pathOnly(path) { return path.split('?')[0]; }

test('Got It files an eligible phone row immediately, preserves its list fields and queues Undo behind the owner acknowledgement',async()=>{
  const when=new Date(Date.now()-60000).toISOString(), gate=deferred();
  const task=todo('report',{ask:{verdict:'ready',asked_at:when,outcome:'done'},notes:'Keep this summary'});
  const {workspace,state}=await opened({features:['todo_seen'],todos:[task]});
  const section=()=>workspace.sections().find(group=>group.rows.some(row=>row.lead.id==='report'))?.key;
  assert.equal(section(),'just_done');
  state.request=async({path,method})=>{
    assert.equal(path,'/api/v1/todos/report/seen?manager=work');
    if(method==='PUT'){await gate.promise;return {todo:{id:'report',seen_at:new Date().toISOString()}};}
    assert.equal(method,'DELETE');return {todo:{id:'report'}};
  };
  const first=workspace.markSeen(workspace.items[0]);
  assert.equal(section(),'done');
  assert.equal(await workspace.markSeen(workspace.items[0]),false);
  const undo=workspace.undoSeen();
  gate.resolve();
  assert.equal(await first,true);
  assert.equal(await undo,true);
  assert.equal(section(),'just_done');
  assert.deepEqual(workspace.items[0].ask,task.ask);
  assert.equal(workspace.items[0].notes,task.notes);
  assert.deepEqual(state.calls.filter(call=>call.method!=='GET').map(call=>call.method),['PUT','DELETE']);
});

test('a retained compact row files the current member when the previous lead was seen',async()=>{
  const stamp=new Date(Date.now()-60000).toISOString(),group='one-plan',make=id=>todo(id,{title:'One shared title',plan_id:group,
    ask:{verdict:'ready',asked_at:stamp,outcome:'done'},updated_at:stamp});
  const original=[make('first'),make('second')];
  const {workspace,state,client}=await opened({features:['todo_seen'],todos:original});
  const writes=[];
  state.request=async({path,method})=>{
    const key=pathOnly(path).split('/').at(-2);writes.push([method,key]);
    const item=original.find(v=>v.id===key);return {todo:{...item,seen_at:method==='PUT'?new Date().toISOString():''}};
  };
  const rows=()=>workspace.sections().find(s=>s.key==='just_done')?.rows||[];
  const bind=old=>reconcileBoundRows(old,rows(),group=>group.key,client,client,'list','list');
  let bound=bind([]);assert.equal(bound.length,1);const button=()=>bound[0].value.lead;
  const first=button();assert.equal(await workspace.markSeen(first),true);
  const old=bound[0];bound=bind(bound);assert.equal(bound[0],old);
  const second=button();assert.notEqual(second.id,first.id);assert.equal(await workspace.markSeen(second),true);
  assert.equal(await workspace.undoSeen(),true);
  assert.deepEqual(writes,[['PUT',first.id],['PUT',second.id],['DELETE',second.id]]);
});

test('seen respects feature, acceptance, current owner and failed writes',async()=>{
  const when=new Date(Date.now()-5000).toISOString();
  const task=todo('report',{ask:{verdict:'ready',asked_at:when,outcome:'done'}});
  for(const fields of [{delivery:{state:'live'}},{supervision:{state:'pending_review'}},{supervision:{supervisor:'leader'}}]){
    const {workspace,state}=await opened({features:['todo_seen'],todos:[{...task,...fields}]});
    assert.equal(await workspace.markSeen(workspace.items[0]),false);
    assert.equal(state.calls.filter(x=>x.method!=='GET').length,0);
  }
  const legacy=await opened({todos:[task]});
  assert.equal(await legacy.workspace.markSeen(legacy.workspace.items[0]),false);
  const {workspace,state,client}=await opened({features:['todo_seen'],todos:[task]});
  state.request=async()=>{throw new HttpFailure(403,'forbidden');};
  assert.equal(await workspace.markSeen(workspace.items[0]),false);
  assert.equal(workspace.seenUndo,undefined);
  assert.equal(workspace.error,'permission_denied');
  assert.equal(workspace.sections()[0].key,'just_done');
  const gate=deferred();
  state.request=async({method})=>method==='PUT'?gate.promise:{todos:[]};
  const old=workspace.markSeen(workspace.items[0]);
  state.generation++;await workspace.open(client);
  gate.resolve({todo:{id:'report',seen_at:new Date().toISOString()}});
  assert.equal(await old,false);
  assert.equal(workspace.items.length,0);
  assert.equal(await workspace.undoSeen(),false);
});

test('a reopened calendar restores its mode and local date even when the user leaves immediately',async()=>{
  const e=await opened(), anchor=new Date(2026,10,25,16).getTime();
  e.workspace.setCalendar(true,'week',anchor); e.workspace.close();
  await e.workspace.open(e.client);
  assert.equal(e.workspace.calendarOpen,true); assert.equal(e.workspace.calendarMode,'week');
  assert.equal(todoDayKey(new Date(e.workspace.calendarAnchor)),'2026-11-25');
  e.state.scope='hub:another'; e.state.generation++; await e.workspace.open(e.client);
  assert.equal(e.workspace.calendarOpen,false); assert.equal(e.workspace.calendarMode,'day');
});

test('the local calendar date can be saved offline and does not depend on a usable write connection',async()=>{
  const e=makeHarness();e.state.connected=false;
  const ws=new TodoWorkspace();await ws.open(e.client);assert.equal(ws.calendarReady,true);
  ws.setCalendar(true,'month',new Date(2026,11,4).getTime());ws.close();await ws.open(e.client);
  assert.equal(ws.calendarMode,'month');assert.equal(todoDayKey(new Date(ws.calendarAnchor)),'2026-12-04');
  assert.deepEqual(e.state.calls,[]);
});

test('native calendar drop geometry handles week columns, quarter hours, all-day conversion and month time preservation',()=>{
  const days=[new Date(2026,10,22).getTime(),new Date(2026,10,23).getTime()];
  const timed={left:10,top:100,width:200,height:60,days,hour:8};
  assert.deepEqual(todoCalendarDrop([timed],110,126),{day:days[1],minute:510});
  assert.equal(todoCalendarDrop([timed],210,126),undefined);
  assert.equal(todoCalendarDrop([{...timed,hour:23}],12,159).minute,1425);
  const item=new TodoWorkspaceItem(todo('a',{due_at:new Date(2026,10,22,14,45).toISOString()}),'work');
  assert.deepEqual(todoDropDue(item,{day:days[1],minute:-1}),{day:'2026-11-23',at:''});
  assert.deepEqual(todoDropDue(item,{day:days[1],minute:-2}),{at:new Date(2026,10,23,14,45).toISOString(),day:''});
});

test('a calendar move reads the owner again, shows a provisional position, changes only the due field and can be undone',async()=>{
  const original=todo('a',{due_date:'2026-11-22',notes:'retain me'}),e=await opened({todos:[original]});
  const before=deferred(), allow=deferred(), requests=[];
  e.state.request=async ({method,path,body},s)=>{
    if (method==='GET') { before.resolve(); await allow.promise; return {todos:structuredClone(s.todos)}; }
    requests.push({path,body});
    Object.assign(s.todos[0],{due_at:null,due_date:null},body);
    return {todo:structuredClone(s.todos[0])};
  };
  const target={at:'2026-11-24T11:15:00.000Z',day:''};
  const moving=e.workspace.reschedule(e.workspace.items[0],target);
  await before.promise;
  assert.equal(e.workspace.calendarRows()[0].dueAt,target.at);
  assert.equal(e.workspace.items[0].dueDate,'2026-11-22');
  allow.resolve(); assert.equal(await moving,true);
  assert.equal(await e.workspace.undoCalendarMove(),true); assert.equal(e.workspace.undoTodo,'');
  assert.deepEqual(requests,[{path:'/api/v1/todos/a?manager=work',body:{due_at:target.at}},
    {path:'/api/v1/todos/a?manager=work',body:{due_date:'2026-11-22'}}]);
  assert.equal(e.state.todos[0].notes,'retain me');
});

test('read-only, stale or changed-owner calendar gestures preserve the deadline and never submit a second move',async()=>{
  const next={day:'2026-11-28',at:''}, e=await opened({todos:[todo('a',{due_date:'2026-11-22'})]});
  const permission=deferred();
  e.state.request=async ({method},s)=>{
    if (method==='GET') return {todos:structuredClone(s.todos)};
    await permission.promise;
    throw new HttpFailure(403,'forbidden');
  };
  const denied=e.workspace.reschedule(e.workspace.items[0],next);
  assert.equal(await e.workspace.reschedule(e.workspace.items[0],next),false);
  permission.resolve(); assert.equal(await denied,false);
  assert.equal(e.workspace.calendarRows()[0].dueDate,'2026-11-22'); assert.equal(e.workspace.calendarReadOnly,true);
  assert.equal(e.workspace.error,'permission_denied'); assert.equal(e.workspace.undoTodo,'');
  await e.workspace.open(e.client);
  e.state.todos[0].due_date='2026-11-25'; const patches=e.state.calls.filter(x=>x.method==='PATCH').length;
  assert.equal(await e.workspace.reschedule(e.workspace.items[0],next),false); assert.equal(e.workspace.error,'request_updated');
  assert.equal(e.workspace.items[0].dueDate,'2026-11-25');
  const gate=deferred(); e.state.request=async()=>{await gate.promise;return {todos:e.state.todos}};
  const stale=e.workspace.reschedule(e.workspace.items[0],next); e.state.generation++; gate.resolve(); assert.equal(await stale,false);
  assert.equal(e.workspace.movingItem,undefined); assert.equal(e.state.calls.filter(x=>x.method==='PATCH').length,patches);
});

test('two managers using the same to-do id keep independent deadline rows and undo targets',async()=>{
  const e=await opened({todos:[todo('same',{manager:'other',due_date:'2026-11-20'}),todo('same',{due_date:'2026-11-23'})]});
  const [other,own]=e.workspace.items;
  assert.equal(e.workspace.canMove(other),false);
  e.state.request=async({method,body},s)=>{
    if (method==='GET') return {todos:structuredClone(s.todos)};
    Object.assign(s.todos[1],body);return {todo:structuredClone(s.todos[1])};
  };
  assert.equal(await e.workspace.reschedule(own,{at:'',day:'2026-11-25'}),true);
  assert.equal(e.workspace.movedItem().manager,'work');assert.equal(e.workspace.items[0],other);
  assert.equal(await e.workspace.undoCalendarMove(),true);
  assert.deepEqual(e.state.todos.map(x=>x.due_date),['2026-11-20','2026-11-23']);
});

function countsFor(sections, needs = sections.needs_you || 0) {
  const values = Object.fromEntries(TODO_HUB_SECTIONS.map(key => [key, sections[key] || 0]));
  return { open: Object.entries(values).reduce((n, [key, v]) => n + (['someday', 'done'].includes(key) ? 0 : v), 0),
    needs_you: needs, in_progress: values.working + values.waiting, sections: values };
}

test('reads production Hub To-do filing and count fixtures, including every stable section, rank and zero', () => {
  const corpus = name => JSON.parse(readFileSync(new URL('../../spec/fixtures/' + name, import.meta.url))).cases;
  for (const example of corpus('todo-view/todo_view.json')) {
    const expected = example.expected.view;
    const row = TodoHubRow.read({ view: expected });
    assert.ok(row, example.name);
    assert.deepEqual([row.phase, row.rank, row.section], [expected.phase, expected.rank, expected.section], example.name);
    if (expected.line?.key === 'failed_start') assert.equal(row.line, expected.line.args?.error || '', example.name);
  }
  for (const example of corpus('todo-counts/todo_counts.json')) {
    const raw = example.expected.counts, read = TodoHubCounts.read({ counts: raw });
    assert.deepEqual([read.open, read.needsYou, read.inProgress], [raw.open, raw.needs_you, raw.in_progress], example.name);
    assert.deepEqual(Object.fromEntries(read.sections), raw.sections, example.name);
  }
  assert.equal(TodoHubRow.read({ view: { section: 'future-section', rank: 0, phase: 'future' } }), undefined);
  assert.equal(TodoHubRow.read({ view: { section: 'working', rank: 2.5, phase: 'working' } }), undefined);
  assert.equal(TodoHubCounts.read({ counts: { ...countsFor({}), open: -2 } }), undefined);
  assert.equal(TodoHubCounts.read({ counts: { ...countsFor({}), sections: { todo: 1 } } }), undefined);
});

test('Hub opt-in files by the authenticated snapshot, ranks distinct rows and uses quiet-aware Needs you counts', async () => {
  const view = (section, rank, fields = {}) => ({ section, phase: section, reason: 'future_reason', rank, ...fields });
  const todos = [
    todo('q2', { title: 'One related reminder', plan_id: 'pair', quiet: true, view: view('needs_you', 2) }),
    todo('q1', { title: 'One related reminder', plan_id: 'pair', view: view('needs_you', 0,
      { line: { key: 'agent_asks', args: { run: 'Build bot', text: 'Choose the channel' } } }) }),
    todo('later', { view: view('can_wait', 0) }),
    todo('worked', { view: view('working', 1) }),
    todo('finished', { view: view('just_finished', 0, { phase: 'ready' }) }),
    todo('child', { part_of: 'worked', view: view('working', 0) }),
    todo('future', { view: view('new_section', 0) })
  ];
  const counts = countsFor({ needs_you: 2, can_wait: 1, working: 1, just_finished: 1, todo: 1 }, 1);
  const { workspace, state } = await opened({ features: ['todo_view'], request: () => ({ todos, counts }) });
  assert.equal(workspace.effectiveGroup(), 'status', 'released iOS opens the existing list');
  workspace.setGroupBy('hub');
  let sections = workspace.sections();
  assert.deepEqual(sections.map(s => s.key), ['needs_you','can_wait','working','todo','just_finished']);
  assert.deepEqual(sections[0].rows.map(row => row.lead.id), ['q1','q2'], 'real rank overrides recency and relation merging');
  assert.equal(sections[0].rows[0].lead.hubView.line, 'Build bot · Choose the channel');
  assert.equal(workspace.hubCounts.choiceCount('needs_you'), 1, 'quiet rows stay visible without counting toward owner attention');
  assert.equal(workspace.hubCounts.choiceCount('in_progress'), 1, 'just-finished rows belong under this choice but never add to its count');
  assert.deepEqual(workspace.hubCounts.choices('all'), ['all','needs_you','in_progress','todo']);
  assert.equal(sections.find(s => s.key === 'todo').rows[0].lead.id, 'future', 'unknown future sections fall back per row');
  workspace.setHubChoice('in_progress');
  assert.deepEqual(workspace.sections().map(s => s.key), ['working','just_finished']);
  workspace.setSearch('One related reminder');
  assert.deepEqual(workspace.sections().map(s => s.key), ['needs_you'], 'search crosses section choices');
  workspace.setSearch('');
  assert.deepEqual(workspace.sections().map(s => s.key), ['working','just_finished']);
  assert.deepEqual(state.calls.map(call => [call.path, call.method]), [['/api/v1/todos?manager=work','GET']]);
  workspace.close();
});

test('old servers and malformed snapshots restore the existing list and never reuse counts from a previous owner', async () => {
  const raw = { todos: [todo('a', { view: { section: 'working', rank: 0, phase: 'working' } })], counts: countsFor({working:1}) };
  const modern = makeHarness({ features: ['todo_view'], request: () => structuredClone(raw) });
  const legacy = makeHarness({ manager: 'other', request: () => structuredClone(raw) });
  for (const h of [modern, legacy]) h.state.views.set(h.state.scope + ':todo-workspace', JSON.stringify({group:'hub',hubUnfolded:['can_wait']}));
  const workspace = new TodoWorkspace();
  await workspace.open(modern.client);
  assert.equal(workspace.effectiveGroup(), 'hub');
  assert.deepEqual(workspace.hubUnfolded, ['can_wait']);
  workspace.toggleHubFold('someday');
  await workspace.draftWrites;
  assert.deepEqual(JSON.parse(modern.state.views.get(modern.state.scope + ':todo-workspace')).hubUnfolded, ['can_wait','someday']);
  const slow = deferred();
  modern.state.request = () => slow.promise;
  const stale = workspace.refresh();
  await workspace.open(legacy.client);
  slow.resolve(raw);
  await stale;
  assert.equal(workspace.hubCounts, undefined);
  assert.equal(workspace.effectiveGroup(), 'status');
  assert.deepEqual(workspace.sections().map(s => s.key), ['other']);
  legacy.client.features.push('todo_view');
  legacy.state.request = () => ({ todos: [todo('x')], counts: { needs_you: 0 } });
  await workspace.refresh();
  assert.equal(workspace.effectiveGroup(), 'status');
  assert.equal(workspace.items[0].id, 'x');
  assert.deepEqual(legacy.state.calls.map(call => call.method), ['GET','GET']);
  workspace.close();
});

test('projects server lifecycle fields into the six native states with SOP priority', () => {
  const linked = [{ machine_id: 'm1', session_id: 's1' }];
  const state = (fields, sessionRows = []) => todoWorkspaceStatus(new TodoWorkspaceItem(todo('t', { agents: linked, ...fields }), 'work'), sessionRows);

  assert.equal(state({ done_at: '2026-11-11T09:00:00Z', start_error: 'node unavailable' }), 'needs_you');
  assert.equal(state({ done_at: '2026-11-11T09:00:00Z' }, [session('s1', 'working')]), 'done');
  assert.equal(state({ ask: { verdict: 'choose one', outcome: 'decide', asked_at: '2026-11-11T09:00:00Z' } }), 'needs_you');
  assert.equal(state({ supervision: { state: 'needs_user' } }), 'needs_you');
  assert.equal(state({ supervision: { state: 'user_check' }, delivery: { state: 'live' } }), 'needs_you');
  assert.equal(state({}, [session('s1', 'needs_you')] ), 'needs_you');
  assert.equal(state({ ask: { verdict: 'verify result', outcome: 'done', asked_at: '2026-11-11T09:00:00Z' } }), 'checking');
  assert.equal(state({ supervision: { state: 'pending_review' } }), 'checking');
  assert.equal(state({ supervision: { state: 'pending_review' } }, [session('s1', 'working')]), 'working');
  assert.equal(state({}, [session('s1', 'unread')]), 'checking');
  assert.equal(state({}, [session('s1', 'working')]), 'working');
  assert.equal(state({}, [session('s1', 'waiting')]), 'working');
  assert.equal(state({ ask: { verdict: 'verify', outcome: 'done', asked_at: '2026-11-11T09:00:00Z' } }, [session('s1', 'working')]), 'working');
  assert.equal(state({ supervision: { state: 'accepted', reviews: [{ verdict: 'accepted' }] } }), 'done');
  assert.equal(state({ supervision: { state: 'accepted', reviews: [{ verdict: 'rework' }] } }, [session('s1', 'offline')]), 'followup');
  assert.equal(state({ delivery: { state: 'live' } }), 'done');
  assert.equal(state({ pr_url: 'https://github.com/example/repo/pull/7', delivery: { state: 'pr' } }), 'followup');
  assert.equal(state({}, [session('s1', 'idle', { turn_error: 'rate limit' })]), 'needs_you');
  assert.equal(state({}, [session('s1', 'idle', { turn_error: 'usage limit', turn_error_kind: 'usage_limit' })]), 'checking');
  assert.equal(state({}, [session('s1', 'offline')]), 'followup');
  assert.equal(todoWorkspaceStatus(new TodoWorkspaceItem(todo('empty'), 'work'), []), 'not_started');
  const mixed = new TodoWorkspaceItem(todo('mixed', { agents: linked.concat([{ machine_id: 'm1', session_id: 's2' }]) }), 'work');
  assert.equal(todoWorkspaceStatus(mixed, [session('s1', 'working'), session('s2', 'unread')]), 'checking');
});

test('failed starts remain owner-actionable while related rows follow the server done_at boundary', () => {
  const failed = new TodoWorkspaceItem(todo('failed', { done_at: '2026-11-11T09:00:00Z', start_error: 'launch failed', plan_id: 'plan-a' }), 'work');
  const related = new TodoWorkspaceItem(todo('related', { plan_id: 'plan-a' }), 'work');
  const completed = new TodoWorkspaceItem(todo('completed', { done_at: '2026-11-11T09:00:00Z', plan_id: 'plan-a' }), 'work');
  assert.equal(failed.isDone(), true, 'the server stores a failed start as done_at plus start_error');
  assert.equal(todoWorkspaceStatus(failed, []), 'needs_you');
  assert.equal(relatedTodoMembers([failed, related], failed).map((item) => item.id).join(','), 'failed');
  assert.equal(relatedTodoMembers([failed, completed], failed).map((item) => item.id).join(','), 'failed,completed');
});

test('relationship keys normalize Unicode titles and PR URL suffixes like the shared triage contract', () => {
  const titleA = new TodoWorkspaceItem(todo('title-a', { title: '任务；确认更新！' }), 'work');
  const titleB = new TodoWorkspaceItem(todo('title-b', { title: '任务 确认更新' }), 'work');
  const prA = new TodoWorkspaceItem(todo('pr-a', { pr_url: 'https://example.test/pull/42/?utm_source=app#overview' }), 'work');
  const prB = new TodoWorkspaceItem(todo('pr-b', { pr_url: 'https://example.test/pull/42' }), 'work');
  assert.equal(relatedTodoMembers([titleA, titleB], titleA).length, 2);
  assert.equal(relatedTodoMembers([prA, prB], prA).length, 2);
});

test('search, filters, and status, project, and thread groupings use the selected values', async () => {
  const items = [
    todo('one', { title: 'Fix login', project: 'mobile', thread: { channel: 'lark', chat_id: 'room', thread_id: 'thread-a', title: 'Release' }, plan_id: 'p1' }),
    todo('two', { title: 'Fix login', project: 'mobile', thread: { channel: 'lark', chat_id: 'room', thread_id: 'thread-a', title: 'Release' }, plan_id: 'p1' }),
    todo('three', { title: 'Buy tea', project: 'home' })
  ];
  const { workspace } = await opened({ todos: items });
  assert.equal(workspace.sections().length, 1, 'related items share one status row');
  assert.equal(workspace.sections()[0].rows[0].related().length, 1);
  workspace.setGroupBy('project');
  assert.deepEqual(workspace.sections().map((section) => section.title), ['home', 'mobile']);
  workspace.setGroupBy('thread');
  assert.deepEqual(workspace.sections().map((section) => section.title), ['Release', 'todo_no_thread']);
  workspace.setSearch('tea');
  assert.equal(workspace.sections().flatMap((section) => section.rows).length, 1);
  workspace.setStatusFilter('done');
  assert.equal(workspace.sections().length, 0);
  workspace.close();
});

test('same-key Todo group bindings replace immutable values and deferred row actions use the current lead', async () => {
  const { workspace } = await opened({ todos: [todo('t1', { plan_id: 'plan-a' }), todo('t2', { plan_id: 'plan-a' })] });
  const owner = {};
  const initialGroup = workspace.sections()[0].rows[0];
  const initialBindings = reconcileBoundRows([], [initialGroup], (group) => group.key, undefined, owner, '', 'scope-a');
  const originalNativeKey = initialBindings[0].key;

  workspace.items = workspace.items.map((item) => item.id === 't1'
    ? new TodoWorkspaceItem({ ...item.data(), title: 'Renamed latest', updated_at: '2026-11-11T11:00:00Z' }, 'work')
    : item);
  const currentGroup = workspace.sections()[0].rows[0];
  const currentBindings = reconcileBoundRows(initialBindings, [currentGroup], (group) => group.key, owner, owner, 'scope-a', 'scope-a');
  let openedTitle = '';
  const deferredOpen = () => { openedTitle = currentBindings[0].value.lead.title; };
  deferredOpen();

  assert.strictEqual(currentBindings[0], initialBindings[0]);
  assert.equal(currentBindings[0].key, originalNativeKey);
  assert.equal(currentBindings[0].value.lead.title, 'Renamed latest');
  assert.equal(openedTitle, 'Renamed latest');

  const page = readFileSync(new URL('../entry/src/main/ets/components/TodoWorkspacePage.ets', import.meta.url), 'utf8');
  assert.match(page, /ForEach\(section\.rows, \(binding: BoundRow<TodoWorkspaceGroup>\) => this\.todoRow\(binding,section\),\s*\(binding: BoundRow<TodoWorkspaceGroup>\) => binding\.key\)/);
  assert.match(page, /this\.openTodoAt\(binding\.value\.lead,binding\.value\.phone\.canFileSeen,event\)/);
  assert.match(page, /this\.onOpenResource\(this\.workspace\.todoResource\(todo\)\)/);
  assert.match(page, /ForEach\(this\.lineageRowBindings/);
  assert.match(page, /ForEach\(this\.relatedTodoBindings/);
  assert.match(page, /ForEach\(this\.spaceBindings, \(binding: BoundRow<TodoWorkspaceSpace>\) => this\.spaceRow\(binding\)/);
  assert.match(page, /ForEach\(this\.activeAgentBindings, \(binding: BoundRow<TodoWorkspaceAgent>\) => this\.agentRow\(binding\)/);
  assert.match(page, /ForEach\(this\.previousAgentBindings, \(binding: BoundRow<TodoWorkspaceAgent>\) => this\.agentRow\(binding\)/);
  assert.match(page, /\.onClick\(\(\) => this\.confirmUnlinkAgent\(this\.workspace\.selected, binding\.value\)\)/);
  assert.match(page, /\.onClick\(\(\) => this\.confirmUnlinkSpace\(this\.workspace\.selected, binding\.value\)\)/);
  workspace.close();
});

test('create, edit, done, reopen, existing-Agent link, project link, and delete use exact Todo routes', async () => {
  const linked = todo('t1', { agents: [], spaces: [], thread: { channel: 'lark', chat_id: 'room', thread_id: 'thread-1', title: 'Release thread' } });
  const agent = session('s1', 'working', { cwd: '/repo', agent: 'claude', space_name: 'persona project' });
  const { workspace, state } = await opened({ todos: [linked], sessions: [agent], machines: [machine()] });
  await workspace.openRow(workspace.items[0]);
  workspace.setTitleDraft('  Updated title  ');
  workspace.setNotesDraft('Details');
  assert.equal(await workspace.save(), true);
  assert.deepEqual(state.calls.at(-1), {
    path: '/api/v1/todos/t1?manager=work', method: 'PATCH', body: { title: 'Updated title', notes: 'Details' }
  });
  assert.equal(await workspace.setDone(workspace.selected, true), true);
  assert.deepEqual(state.calls.at(-1), { path: '/api/v1/todos/t1?manager=work', method: 'PATCH', body: { done: true } });
  assert.equal(await workspace.setDone(workspace.selected, false), true);
  assert.deepEqual(state.calls.at(-1), { path: '/api/v1/todos/t1?manager=work', method: 'PATCH', body: { done: false } });

  assert.equal(await workspace.linkAgent(workspace.selected, agent), true);
  assert.deepEqual(state.calls.at(-1), { path: '/api/v1/todos/t1/agents/m1/s1?manager=work', method: 'PUT', body: {} });
  assert.equal(workspace.selected.activeAgents()[0].source, 'manual');
  assert.equal(state.calls.some((call) => pathOnly(call.path).includes('/messages')), false, 'linking never sends a message');
  assert.equal(await workspace.linkSpace(workspace.selected, new TodoWorkspaceSpaceChoice('m1', 'persona project', 'Computer m1 · persona project')), true);
  assert.deepEqual(state.calls.at(-1), {
    path: '/api/v1/todos/t1/spaces/m1/persona%20project?manager=work', method: 'PUT', body: {}
  });
  const active = workspace.selected.activeAgents()[0];
  assert.equal(await workspace.unlinkAgent(workspace.selected, active), true);
  assert.deepEqual(state.calls.at(-1), { path: '/api/v1/todos/t1/agents/m1/s1?manager=work', method: 'DELETE', body: {} });
  const space = new TodoWorkspaceSpace({ machine_id: 'm1', space: 'persona project', linked_at: '2026-11-11T09:00:00Z' });
  assert.equal(await workspace.unlinkSpace(workspace.selected, space), true);
  assert.deepEqual(state.calls.at(-1), { path: '/api/v1/todos/t1/spaces/m1/persona%20project?manager=work', method: 'DELETE', body: {} });
  assert.equal(await workspace.remove(workspace.selected), true);
  assert.deepEqual(state.calls.at(-1), { path: '/api/v1/todos/t1?manager=work', method: 'DELETE', body: {} });
  assert.equal(state.calls.filter((call) => call.method === 'POST' && pathOnly(call.path) === '/api/v1/todos').length, 0);
  workspace.close();
});

test('calendar cells observe the current date and mode even when the native hour rows are reused',()=>{
  const page=readFileSync(new URL('../entry/src/main/ets/components/TodoWorkspacePage.ets', import.meta.url),'utf8');
  assert.match(page,/@Computed get calendarWeekDays\(\)[\s\S]*?todoCalendarDays\('week',this\.workspace\.calendarAnchor/);
  assert.match(page,/@Computed get calendarPageDays\(\)[\s\S]*?todoCalendarDays\(this\.workspace\.calendarMode,this\.workspace\.calendarAnchor/);
  assert.match(page,/calendarStrip\(month:boolean=false\)[\s\S]*?ForEach\(month \? this\.calendarPageDays : this\.calendarWeekDays/);
  assert.match(page,/calendarTimeline\(\)[\s\S]*?ForEach\(this\.calendarPageDays,/);
});
test('calendar periods honor local month boundaries and the configured first weekday', () => {
  const today=new Date(2026, 1, 1, 12).getTime();
  const sunday=todoCalendarDays('month',today,0);
  assert.equal(sunday.length,28);
  assert.deepEqual([todoDayKey(new Date(sunday[0])),todoDayKey(new Date(sunday.at(-1)))],['2026-02-01','2026-02-28']);
  const week=todoCalendarDays('week',today,1);
  assert.deepEqual([new Date(week[0]).getDay(), week.length, todoDayKey(new Date(week.at(-1)))],[1,7,'2026-02-01']);
  assert.equal(todoDayKey(new Date(todoCalendarDays('day',today,1)[0])),'2026-02-01');
});

test('new drafts are scoped to group and manager, and successful create needs no follow-up read', async () => {
  const { workspace, state } = await opened({ todos: [] });
  const key = 'hub:work:todo:draft:["profile-1","work","new"]';
  state.views.set(key, JSON.stringify({ title: 'Recovered task', notes: 'Recovered notes' }));
  workspace.startCreate();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(workspace.titleDraft, 'Recovered task');
  assert.equal(workspace.notesDraft, 'Recovered notes');
  state.calls.length = 0;
  assert.equal(await workspace.save(), true);
  assert.deepEqual(state.calls, [{ path: '/api/v1/todos?manager=work', method: 'POST', body: { title: 'Recovered task', notes: 'Recovered notes' } }]);
  workspace.close();
});

test('a to-do created in a calendar slot is due on that local date or at that time and does not reuse the undated draft', async () => {
  for (const [date,time,key] of [['2027-03-14','','due_date'], ['','2027-03-14T18:30:00Z','due_at']]) {
    const { workspace,state }=await opened({todos:[]});
    state.views.set('hub:work:todo:draft:["profile-1","work","new"]',JSON.stringify({title:'Unrelated list draft'}));
    workspace.startCreate(date,time);
    await new Promise(resolve=>setImmediate(resolve));
    assert.equal(workspace.titleDraft,'');
    workspace.setTitleDraft('Review'); state.calls.length=0;
    assert.equal(await workspace.save(),true);
    assert.deepEqual(state.calls[0].body,{title:'Review',notes:'',[key]:date || time});
    workspace.close();
  }
});

test('existing edits restore and persist only the matching group, manager, and Todo draft', async () => {
  const draftKey = 'hub:work:todo:draft:["profile-1","work","t1"]';
  const { workspace, state } = await opened({ todos: [todo('t1')] });
  state.views.set(draftKey, JSON.stringify({ title: 'Local title', notes: 'Local notes' }));
  await workspace.openRow(workspace.items[0]);
  assert.equal(workspace.titleDraft, 'Local title');
  assert.equal(workspace.notesDraft, 'Local notes');
  workspace.setTitleDraft('New local title');
  workspace.setNotesDraft('New local notes');
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(JSON.parse(state.views.get(draftKey)), { title: 'New local title', notes: 'New local notes' });
  workspace.close();
});

test('offline and untrusted computers cannot be linked, and an active association is removed only after ACK', async () => {
  const item = todo('t1');
  const agent = session('s1', 'working');
  const { workspace, state } = await opened({ todos: [item], sessions: [agent], machines: [machine('m1', { online: true, trust: 'pending' })] });
  assert.equal(workspace.candidateSessions().length, 0);
  assert.equal(await workspace.linkAgent(workspace.items[0], agent), false);
  assert.equal(state.calls.filter((call) => call.method === 'PUT').length, 0);
  workspace.close();
});

test('unknown and pending writes stay blocked without a replacement request', async () => {
  for (const failure of [new HttpFailure(0, 'result_unknown', true, false), new HttpFailure(202, 'result_unknown', true, true)]) {
    const { workspace, state } = await opened({ todos: [todo('t1')], request: async (call, fixture) => {
      if (call.method === 'GET') return { todos: fixture.todos };
      fixture.blocked.add(call.path);
      throw failure;
    } });
    await workspace.openRow(workspace.items[0]);
    assert.equal(await workspace.setDone(workspace.selected, true), false);
    assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, 1);
    assert.ok(['todo_write_unknown', 'todo_write_pending'].includes(workspace.error));
    assert.equal(await workspace.setDone(workspace.selected, true), false);
    assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, 1);
    workspace.close();
  }
});

test('a parent list refresh shows independently completed, renamed, and deleted detail mutations', async () => {
  const harness = makeHarness({ todos: [todo('t1')] });
  const parent = new TodoWorkspace(), detail = new TodoWorkspace();
  await parent.open(harness.client);
  const row = new ResourceRow('t1', 'Todo t1', 'todo');
  row.manager = 'work';
  await detail.open(harness.client, row, false);
  parent.setSearch('kept search');
  parent.setStatusFilter('all');
  parent.setGroupBy('project');
  parent.startCreate();
  parent.setTitleDraft('Unsubmitted parent draft');
  parent.setNotesDraft('Keep this text');

  assert.equal(await detail.setDone(detail.selected, true), true);
  assert.equal(parent.items[0].isDone(), false, 'the parent has an independent list snapshot before returning');
  await parent.refresh();
  assert.equal(parent.items[0].isDone(), true);

  await detail.openRow(detail.selected);
  detail.setTitleDraft('Renamed in detail');
  assert.equal(await detail.save(), true);
  await parent.refresh();
  assert.equal(parent.items[0].title, 'Renamed in detail');

  assert.equal(await detail.remove(detail.selected), true);
  await parent.refresh();
  assert.equal(parent.items.length, 0);
  assert.equal(parent.search, 'kept search');
  assert.equal(parent.statusFilter, 'all');
  assert.equal(parent.groupBy, 'project');
  assert.equal(parent.editorOpen, true);
  assert.equal(parent.titleDraft, 'Unsubmitted parent draft');
  assert.equal(parent.notesDraft, 'Keep this text');
  parent.close();
  detail.close();
});

for (const editorOpen of [false, true]) {
  test(`a deleted Todo detail stays selected, keeps its draft, and blocks writes when editorOpen=${editorOpen}`, async () => {
    const linked = todo('t1', {
      agents: [{ machine_id: 'm1', session_id: 's1', title: 'Linked agent', linked_at: '2026-11-11T10:00:00Z' }],
      spaces: [{ machine_id: 'm1', space: 'space-old', linked_at: '2026-11-11T10:00:00Z' }]
    });
    const harness = makeHarness({ todos: [linked], sessions: [session('s2', 'working')] });
    const workspace = new TodoWorkspace(), row = new ResourceRow('t1', 'Todo t1', 'todo');
    row.manager = 'work';
    await workspace.open(harness.client, row, false);
    workspace.setTitleDraft('Keep title draft');
    workspace.setNotesDraft('Keep notes draft');
    await workspace.draftWrites;
    workspace.editorOpen = editorOpen;
    harness.state.todos = [];

    await workspace.refresh();
    const selected = workspace.selected;
    assert.equal(workspace.targetGone, true);
    assert.equal(workspace.error, 'todo_gone');
    assert.equal(selected.id, 't1');
    assert.equal(workspace.titleDraft, 'Keep title draft');
    assert.equal(workspace.notesDraft, 'Keep notes draft');
    assert.equal(workspace.editorOpen, editorOpen);
    assert.equal(await workspace.save(), false);
    assert.equal(await workspace.setDone(selected, true), false);
    assert.equal(await workspace.remove(selected), false);
    assert.equal(await workspace.linkAgent(selected, session('s2', 'working')), false);
    assert.equal(await workspace.unlinkAgent(selected, selected.agents[0]), false);
    assert.equal(await workspace.linkSpace(selected, new TodoWorkspaceSpaceChoice('m1', 'space-new', 'Computer m1 · space-new')), false);
    assert.equal(await workspace.unlinkSpace(selected, selected.spaces[0]), false);
    assert.equal(harness.state.calls.filter((call) => call.method !== 'GET').length, 0);
    assert.equal(workspace.selected.id, 't1');
    assert.equal(workspace.titleDraft, 'Keep title draft');
    assert.equal(workspace.notesDraft, 'Keep notes draft');
    workspace.close();
  });
}

test('only the latest Todo list request replaces rows or clears the loading state', async () => {
  const requests = [];
  const harness = makeHarness({ request: async (call) => {
    if (call.method !== 'GET') return { ok: true };
    const pending = deferred();
    requests.push(pending);
    return pending.promise;
  } });
  const workspace = new TodoWorkspace();
  const opening = workspace.open(harness.client);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(requests.length, 1);

  const secondRefresh = workspace.refresh();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(requests.length, 2);
  requests[1].resolve({ todos: [todo('second', { title: 'Second result' })] });
  await secondRefresh;
  assert.equal(workspace.items[0].title, 'Second result');

  const thirdRefresh = workspace.refresh();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(requests.length, 3);
  assert.equal(workspace.loading, true);
  requests[0].resolve({ todos: [todo('stale', { title: 'Stale result' })] });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(workspace.items[0].title, 'Second result');
  assert.equal(workspace.loading, true, 'a stale read cannot clear the newer request spinner');

  requests[2].resolve({ todos: [todo('third', { title: 'Third result' })] });
  await Promise.all([thirdRefresh, opening]);
  assert.equal(workspace.items[0].title, 'Third result');
  assert.equal(workspace.loading, false);
  workspace.close();
});

test('an initial detail open waits for the newest list read before restoring its target', async () => {
  const firstRead = deferred(), secondRead = deferred();
  let getCount = 0;
  const harness = makeHarness({ request: async (call) => {
    if (call.method !== 'GET') return { ok: true };
    getCount++;
    return getCount === 1 ? firstRead.promise : secondRead.promise;
  } });
  const workspace = new TodoWorkspace(), row = new ResourceRow('t1', 'Todo t1', 'todo');
  row.manager = 'work';
  const opening = workspace.open(harness.client, row, false);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(getCount, 1);
  const newestRead = workspace.refresh();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(getCount, 2);

  firstRead.resolve({ todos: [] });
  await new Promise((resolve) => setImmediate(resolve));
  assert.notEqual(workspace.error, 'todo_gone', 'a replaced read cannot report that the detail target is missing');
  assert.equal(workspace.selected.id, '');

  secondRead.resolve({ todos: [todo('t1')] });
  await Promise.all([opening, newestRead]);
  assert.equal(workspace.selected.id, 't1');
  assert.equal(workspace.draftReady, true);
  assert.equal(workspace.error, '');
  workspace.close();
});

test('a successful Todo write invalidates an older read without rolling back its row or spinner', async () => {
  const staleRead = deferred();
  let holdRead = false, held = false;
  const request = async (call, fixture) => {
    if (call.method === 'GET') {
      if (holdRead && !held) { held = true; return staleRead.promise; }
      return { todos: structuredClone(fixture.todos) };
    }
    if (call.method === 'PATCH') {
      const id = decodeURIComponent(call.path.split('/')[4].split('?')[0]);
      const index = fixture.todos.findIndex((item) => item.id === id);
      const updated = { ...fixture.todos[index], ...call.body, updated_at: '2026-11-11T10:01:00Z' };
      if (call.body.done === true) { updated.done_at = '2026-11-11T10:01:00Z'; updated.start_error = ''; }
      fixture.todos[index] = updated;
      return { todo: structuredClone(updated) };
    }
    return { ok: true };
  };
  const { workspace } = await opened({ todos: [todo('t1')], request });
  await workspace.openRow(workspace.items[0]);
  holdRead = true;
  const read = workspace.refresh();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(held, true);
  assert.equal(workspace.loading, true);

  assert.equal(await workspace.setDone(workspace.selected, true), true);
  assert.equal(workspace.items[0].isDone(), true);
  assert.equal(workspace.loading, false, 'the acknowledged write ends the superseded spinner');
  staleRead.resolve({ todos: [todo('t1', { title: 'Stale row' })] });
  await read;
  assert.equal(workspace.items[0].isDone(), true);
  assert.equal(workspace.selected.isDone(), true);
  assert.equal(workspace.loading, false);
  workspace.close();
});

test('a list read from a replaced root source cannot replace the new source rows', async () => {
  const oldRead = deferred();
  const oldHarness = makeHarness({ manager: 'parent', request: async (call) => {
    if (call.method === 'GET') return oldRead.promise;
    return { ok: true };
  } });
  const newHarness = makeHarness({ manager: 'detail', todos: [todo('detail-source', { title: 'Detail source', manager: 'detail' })] });
  const workspace = new TodoWorkspace();
  const oldOpen = workspace.open(oldHarness.client);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(oldHarness.state.calls.length, 1);

  await workspace.open(newHarness.client);
  assert.equal(workspace.items[0].title, 'Detail source');
  oldRead.resolve({ todos: [todo('old-source', { title: 'Old parent source', manager: 'parent' })] });
  await oldOpen;
  assert.equal(workspace.items[0].title, 'Detail source');
  assert.equal(workspace.items[0].manager, 'detail');
  assert.equal(workspace.loading, false);
  workspace.close();
});

test('a delayed mutation response from a previous owner cannot update the current workspace', async () => {
  const response = deferred();
  const { workspace, state } = await opened({ todos: [todo('t1')], request: async (call, fixture) => {
    if (call.method === 'GET') return { todos: fixture.todos };
    return response.promise;
  } });
  await workspace.openRow(workspace.items[0]);
  const write = workspace.setDone(workspace.selected, true);
  assert.equal(state.calls.at(-1).method, 'PATCH');
  state.scope = 'hub:other';
  state.generation++;
  response.resolve({ todo: todo('t1', { done_at: '2026-11-11T10:00:00Z' }) });
  assert.equal(await write, false);
  assert.equal(workspace.items[0].isDone(), false);
  assert.equal(state.calls.filter((call) => call.method === 'PATCH').length, 1);
  workspace.close();
});

test('a delayed draft restore from a previous owner cannot replace the new target draft', async () => {
  const restore = deferred();
  const { workspace, state } = await opened({ todos: [todo('t1')], viewLoad: async (key, fixture) => {
    if (key.startsWith('todo:draft:')) return restore.promise;
    return fixture.views.get(fixture.scope + ':' + key) || '';
  } });
  const opening = workspace.openRow(workspace.items[0]);
  state.scope = 'hub:other';
  state.generation++;
  restore.resolve(JSON.stringify({ title: 'stale owner title', notes: 'stale' }));
  await opening;
  assert.equal(workspace.titleDraft, 'Todo t1');
  assert.equal(workspace.notesDraft, '');
  workspace.close();
});

test('source conversation, chat thread, projects, and lineage preserve their owning identifiers', () => {
  const workspace = new TodoWorkspace();
  workspace.managerId = 'work';
  workspace.conversations = [ResourceRow.from({ id: 'origin-1', manager: 'work', title: 'Chat' }, 'conversation')];
  const item = new TodoWorkspaceItem(todo('t1', { origin: 'origin-1', project: 'persona',
    thread: { channel: 'lark', chat_id: 'chat-1', thread_id: 'thread-1', title: 'Build thread' },
    source: { channel: 'lark', chat_id: 'external-1', message_id: 'message-9', title: 'Source chat' },
    lineage: [{ todo_id: 't0', title: 'Earlier task', why: 'continued' }] }), 'work');
  assert.equal(workspace.sourceResource(item).kind, 'conversation');
  assert.equal(workspace.sourceResource(item).id, 'origin-1');
  assert.equal(workspace.threadResource(item).kind, 'todo_thread');
  assert.equal(workspace.threadResource(item).session, 'thread-1');
  assert.equal(workspace.projectResource(item).path, 'projects');
  assert.deepEqual(workspace.lineageResources(item).map((row) => [row.kind, row.id, row.detail]), [['todo', 't0', 'continued']]);
  const sourced = new TodoWorkspaceItem(todo('t2', { source: { channel: 'lark', chat_id: 'external-1', message_id: 'message-9' } }), 'work');
  assert.equal(workspace.sourceResource(sourced).kind, 'todo_source');
  assert.equal(workspace.sourceResource(sourced).session, 'message-9');
});

test('native page exposes parent navigation, Markdown links, and Todo-aware Agent creation callbacks', () => {
  const page = readFileSync(new URL('../entry/src/main/ets/components/TodoWorkspacePage.ets', import.meta.url), 'utf8');
  assert.match(page, /@Param row: ResourceRow \| undefined/);
  assert.match(page, /@Param list: boolean/);
  assert.match(page, /@Param visible: boolean/);
  assert.match(page, /@Event onOpen: \(row: ResourceRow\) => void/);
  assert.match(page, /@Event onOpenResource: \(row: ResourceRow\) => void/);
  assert.match(page, /@Event onLink: \(address: string\) => void/);
  assert.match(page, /@Event onCreateAgent: \(todo: ResourceRow\) => void/);
  assert.match(page, /@Event onTitle: \(title: string\) => void/);
  assert.match(page, /@Monitor\('workspace\.selected'\) selectedChanged\(\): void/);
  assert.match(page, /this\.onTitle\(selected\.title\)/);
  assert.match(page, /this\.onCreateAgent\(this\.workspace\.todoResource\(this\.workspace\.selected\)\)/);
  assert.match(page, /row\.id !== selected\.id/);
  assert.match(page, /@Monitor\('visible'\) visibilityChanged\(\): void/);
  assert.match(page, /if \(becameVisible && this\.pageAvailable\) \{\s*this\.publishTitle\(\);\s*this\.refreshWhenReady\(\);/);
  assert.match(page, /if \(this\.model\.connected\) this\.refreshWhenReady\(true\)/);
  assert.match(page, /@Monitor\('workspace\.selected'\) selectedChanged\(\): void/);
  assert.match(page, /@Monitor\('workspace\.items', 'workspace\.search', 'workspace\.statusFilter', 'workspace\.groupBy',[\s\S]*'workspace\.projects', 'workspace\.sessions', 'workspace\.machines'/);
  assert.match(page, /!this\.workspace\.targetGone/);
});


test('Todo Agent association excludes Persona and Health assistant conversations',async()=>{
 const {workspace}=await opened({todos:[todo('t1')],sessions:[session('agent','idle'),session('main','idle',{assistant:true}),session('health','idle',{assistant:true,space:'health'})],machines:[machine()]});
 assert.deepEqual(workspace.candidateSessions().map(row=>row.id),['agent']);workspace.close();
});


test('a Todo detail restored before the first connection binds after the owner becomes available',async()=>{
 const harness=makeHarness({todos:[todo('t1')]}),workspace=new TodoWorkspace(),row=new ResourceRow('t1','Task','todo');row.manager='work';harness.state.connected=false;
 await workspace.open(harness.client,row,false);assert.equal(workspace.error,'offline');harness.state.connected=true;harness.state.generation++;await workspace.refresh();assert.equal(workspace.error,'');assert.equal(workspace.selected.id,'t1');workspace.close();
});

test('a same-owner reconnect rebinds reads while retaining and saving an offline Todo editor draft',async()=>{
 const {workspace,client,state}=await opened({todos:[todo('t1')]});await workspace.openRow(workspace.items[0]);workspace.editorOpen=true;state.connected=false;state.generation++;workspace.setTitleDraft('Offline draft');workspace.setNotesDraft('Keep after reconnect');await workspace.draftWrites;
 assert.ok([...state.views.values()].some(value=>value.includes('Offline draft')));state.connected=true;await workspace.refresh();assert.equal(workspace.error,'');assert.equal(workspace.editorOpen,true);assert.equal(workspace.titleDraft,'Offline draft');assert.equal(workspace.notesDraft,'Keep after reconnect');workspace.close();
});

test('the list checkbox completes and reopens a row without selecting it or replacing a different editor draft',async()=>{
  const {workspace,state}=await opened({todos:[todo('first'),todo('second')]});
  await workspace.openRow(workspace.items[1]);workspace.notesDraft='keep draft';
  let target=workspace.items[0];
  assert.equal(await workspace.setDone(target,true,true),true);assert.equal(workspace.selected.id,'second');
  assert.equal(workspace.notesDraft,'keep draft');assert.equal(workspace.items.find(x=>x.id==='first').isDone(),true);
  assert.equal(await workspace.setDone(target,true,true),false);
  target=workspace.items.find(x=>x.id==='first');
  assert.equal(await workspace.setDone(target,false,true),true);
  assert.equal(state.calls.filter(x=>x.method==='PATCH').length,2);assert.equal(workspace.items.find(x=>x.id==='first').isDone(),false);
});

test('a listed row that disappeared during a completion cannot be resurrected by its delayed reply',async()=>{
  let resolve,entered;
  const arrived=new Promise(r=>{entered=r});
  const {workspace,state}=await opened({todos:[todo('target')]});
  state.request=async call=>{
    assert.equal(call.method,'PATCH');
    entered();return new Promise(r=>{resolve=r});
  };
  const source=workspace.items[0];const outbound=workspace.setDone(source,true,true);await arrived;
  workspace.items=[];
  resolve({todo:todo('target',{done_at:'2026-11-20T12:00:00Z'})});assert.equal(await outbound,false);
  assert.deepEqual(workspace.items,[]);assert.equal(workspace.saving,false);
});

test('due filters use local calendar days, end-of-day deadlines and the chosen first weekday',()=>{
  const make=(id,extra)=>new TodoWorkspaceItem(todo(id,extra),'work');
  const now=new Date(2026,10,18,12).getTime();
  const same=make('day',{due_date:'2026-11-18'}), past=make('old',{due_date:'2026-11-07'}), future=make('next',{due_date:'2026-11-22'});
  const completed=make('done',{due_date:'2026-11-07',done_at:'2026-11-06T14:00:00Z'});
  assert.ok(same.due < now && same.dueBy > now);
  assert.equal(todoMatchesDue(past,'today',now,1),true);assert.equal(todoMatchesDue(completed,'today',now,1),false);
  assert.equal(todoMatchesDue(future,'week',now,1),true);assert.equal(todoMatchesDue(future,'week',now,0),false);
  assert.equal(todoMatchesDue(make('invalid',{due_date:'2026-02-31'}),'no_due',now,1),true);
});

test('date and priority sort use the earliest date and highest priority of the visible merged members',async()=>{
  const {workspace}=await opened({todos:[todo('separate',{due_date:'2026-11-23',priority:'low'}),
    todo('lead',{plan_id:'topic',due_date:'2026-11-25',updated_at:'2026-11-18T00:00:00Z'}),
    todo('early',{plan_id:'topic',due_date:'2026-11-20',priority:'high'})]});
  for(const order of ['due','priority']){
    workspace.setOrder(order);
    const rows=workspace.sections().flatMap(s=>s.rows);
    assert.deepEqual(rows.map(r=>r.members.length),[2,1]);
  }
});

test('the due warning counts hidden owner actions once per manager and respects the active search', () => {
  const due = new TodoWorkspace();
  const now = new Date(2026,10,21,12).getTime();
  const item = (id, manager, dates = {}) => new TodoWorkspaceItem(todo(id,{manager,title:'Widget '+manager,
    delivery:{state:'pr',unblock:'user'},...dates}),manager);
  due.items = [
    item('same','home'), item('same','home'), item('same','office'),
    item('today','home',{due_date:'2026-11-21'}), item('later','office',{delivery:{state:'pr',unblock:'agent'}})
  ];
  due.dueFilter='today';
  assert.equal(due.hiddenWaiting(now,1),2);
  due.search='Widget home';assert.equal(due.hiddenWaiting(now,1),1);
  due.dueFilter='all';assert.equal(due.hiddenWaiting(now,1),0);
});

test('the phone status list uses the seven iPhone sections; owner-gated delivery, unseen turns and shelved work file independently',()=>{
  const ws=new TodoWorkspace(), now=Date.parse('2026-11-22T12:00:00Z'), recent='2026-11-22T10:00:00Z', older='2026-11-17T10:00:00Z';
  const item=(id,fields={})=>new TodoWorkspaceItem(todo(id,{title:id,created_at:recent,updated_at:recent,...fields}),'work');
  const link=id=>[{machine_id:'m1',session_id:id}];
  ws.sessions=[session('reading','unread'),session('doing','working'),session('needs','needs_you')];
  ws.items=[item('need',{agents:link('needs'),due_date:'2026-11-22'}),item('deployment-gate',{delivery:{state:'merged',unblock:'user'}}),
    item('running',{agents:link('doing'),due_date:'2026-11-22'}),item('pr',{pr_url:'https://example.test/pull/456'}),
    item('plain',{due_date:'2026-11-22'}),item('finished-turn',{agents:link('reading'),due_date:'2026-11-22'}),
    item('shelved',{origin:'c1',created_at:older,updated_at:older}),item('closed',{done_at:older})];
  assert.deepEqual(ws.sections(now,1).map(s=>s.key),['needs_you','working','pr','other','just_done','shelved','done']);
  ws.setDueFilter('today');
  assert.equal(ws.hiddenWaiting(now,1),1);
  assert.deepEqual(ws.sections(now,1).map(s=>[s.key,s.rows[0].lead.id]),[['needs_you','need'],['working','running'],['other','plain'],['just_done','finished-turn']]);
});

test('a failed start whose finished sibling is filtered out never re-enters Needs You, and open concerns remain separate rows',()=>{
  const ws=new TodoWorkspace(),recent='2026-11-22T10:00:00Z',now=Date.parse('2026-11-22T11:00:00Z');
  const make=(id,fields)=>new TodoWorkspaceItem(todo(id,{title:id,updated_at:recent,...fields}),'work');
  ws.items=[make('failed',{start_error:'unavailable',done_at:recent,plan_id:'p'}),make('completed',{done_at:recent,plan_id:'p',due_date:'2026-11-22'}),
    make('open-concern',{kind:'concern',plan_id:'shared'}),make('unrelated',{plan_id:'shared'})];
  assert.equal(ws.sections(now).find(s=>s.key==='other').rows.length,2);
  ws.setDueFilter('today'); assert.equal(ws.hiddenWaiting(now,1),0);
  assert.deepEqual(ws.sections(now).map(s=>s.key),['done']);
});

test('ordinary completions file immediately while unaccepted work and recent unseen concern reports retain their sections', () => {
  const workspace = new TodoWorkspace();
  const now = Date.parse('2026-11-23T10:00:00Z');
  const recent = '2026-11-23T09:00:00Z', older = '2026-11-19T09:00:00Z';
  const item = (id, values) => new TodoWorkspaceItem(todo(id, { title:id, created_at:older, updated_at:recent, ...values }), 'work');
  const closed = item('closed', { done_at:recent, done_by:'auto' });
  const accepted = item('accepted', { supervision:{state:'accepted',reviews:[{at:recent,verdict:'accepted'}]} });
  const live = item('live', { seen_at:recent, delivery:{state:'live',at:older} });
  const reviewed = item('review-needed', { seen_at:recent, supervision:{state:'pending_review',supervisor:'assistant'} });
  const report = item('reported-concern', { kind:'concern', done_at:recent });
  const section = id => workspace.sections(now).find(current => current.key === id);
  workspace.items = [closed, accepted, live, reviewed];
  assert.deepEqual(section('done').rows.map(row => row.lead.id), ['accepted','closed']);
  assert.deepEqual(section('just_done').rows.map(row => row.lead.id), ['live','review-needed']);
  assert.equal(section('just_done').title, 'phone_todo_section_delivered');
  workspace.items = [...workspace.items, report];
  assert.equal(section('just_done').title, 'phone_todo_section_recent');
  assert.ok(section('just_done').rows.some(row => row.lead.id === report.id));
  workspace.items = [item('read-concern', {kind:'concern',done_at:recent,seen_at:recent}), item('old-concern', {kind:'concern',done_at:older})];
  assert.deepEqual(workspace.sections(now).map(s => s.key), ['done']);
  const moved = {progress:{at:recent}};
  workspace.items = [item('b', {updated_at:recent,supervision:moved}), item('a', {updated_at:older,supervision:moved})];
  assert.deepEqual(section('other').rows.map(row => row.lead.id), ['a','b']);
});


test('every live, legacy and unknown linked Agent state resolves to a localized detail label',()=>{
  const expected={unread:'agent_task_state_unread',blocked:'agent_task_state_needs_you',done:'agent_task_state_unread',
    idle:'agent_task_state_idle',offline:'agent_task_state_offline',working:'agent_task_state_working',needs_you:'agent_task_state_needs_you',followup:'todo_state_followup',future:'agent_task_state_unknown'};
  for (const locale of ['base','en_US','zh_CN','ja_JP']) {
    const folder=new URL('../entry/src/main/resources/'+locale+'/element/',import.meta.url);
    const strings=new Map(['string.json','agent_task_groups.json'].flatMap(file=>JSON.parse(readFileSync(new URL(file,folder),'utf8')).string.map(e=>[e.name,e.value])));
    for (const [state,key] of Object.entries(expected)) {assert.equal(todoAgentStateLabel(state),key);assert.ok(strings.get(key));}
  }
});

const {PhoneTodoDetailEditor,PhoneTodoPlan,todoDetailPatchAccepted}=await import('../entry/src/main/ets/models/PhoneTodoDetailEditor.ets');
test('the detail title commits once on submit/blur/back, discards an empty edit and keeps another device’s untouched rename', async()=>{
  const env=await opened({todos:[todo('entry',{notes:'preserve me'})]});await env.workspace.openRow(env.workspace.items[0]);
  const edit=new PhoneTodoDetailEditor();edit.sync(env.workspace);
  edit.focus(env.workspace);edit.type('  ');edit.commitTitle(env.workspace);await edit.flush(env.workspace);
  assert.equal(edit.title,'Todo entry');assert.equal(env.state.calls.filter(c=>c.method==='PATCH').length,0);
  edit.focus(env.workspace);env.workspace.selected.title='Changed elsewhere';edit.commitTitle(env.workspace);await edit.flush(env.workspace);assert.equal(edit.title,'Changed elsewhere');
  edit.focus(env.workspace);edit.type('  New title  ');edit.commitTitle(env.workspace);edit.commitTitle(env.workspace);await edit.finish(env.workspace);
  const patches=env.state.calls.filter(c=>c.method==='PATCH');assert.deepEqual(patches,[{method:'PATCH',path:'/api/v1/todos/entry?manager=work',body:{title:'New title'}}]);
  assert.equal(env.workspace.selected.notes,'preserve me');assert.equal(env.workspace.selected.title,'New title');
});
test('the native plan coalesces edits, clears a due date or reminder with null and saves the KB project separately',async()=>{
  const env=await opened({todos:[todo('one',{due_at:'2026-11-25T10:00:00Z'})]});await env.workspace.openRow(env.workspace.items[0]);
  const edit=new PhoneTodoDetailEditor();edit.sync(env.workspace);
  env.state.request=({path,method,body},state)=>{
    assert.equal(method,'PATCH');assert.equal(path,'/api/v1/todos/one?manager=work');const x=state.todos[0];
    if ('due_date' in body) delete x.due_at;if ('due_at' in body) delete x.due_date;Object.assign(x,body);return {todo:structuredClone(x)};
  };
  const date=edit.plan.copy();date.allDay=true;edit.change(env.workspace,date,'due');const p=edit.plan.copy();p.hasReminder=true;p.priority='high';edit.change(env.workspace,p,'remind');edit.change(env.workspace,p,'priority');
  const kb=edit.plan.copy();kb.project='release';edit.change(env.workspace,kb,'project');await edit.flush(env.workspace);
  let writes=env.state.calls.filter(c=>c.method==='PATCH');assert.equal(writes.length,2);assert.deepEqual(Object.keys(writes[0].body).sort(),['due_date','priority','remind_at']);
  assert.deepEqual(writes[1].body,{project:'release'});assert.equal(env.workspace.selected.dueAt,'');assert.equal(env.workspace.selected.project,'release');
  const clear=edit.plan.copy();clear.hasDue=false;clear.hasReminder=false;edit.change(env.workspace,clear,'due');edit.change(env.workspace,clear,'remind');await edit.finish(env.workspace);
  writes=env.state.calls.filter(c=>c.method==='PATCH');assert.deepEqual(writes[2].body,{due_at:null,remind_at:null});
  const unchanged=PhoneTodoPlan.from(env.workspace.selected);assert.deepEqual(unchanged.changeFrom(edit.plan,['due','remind','priority','project']),{});
  assert.ok(todoDetailPatchAccepted({remind_at:'2026-11-25T10:00:00.000Z'},{remind_at:'2026-11-25T18:00:00+08:00'}));
});
test('an older Hub ignoring a plan field rolls it back, a read-only or retired owner cannot write, and wrong-task responses never replace the open card',async()=>{
  const env=await opened({todos:[todo('first'),todo('other')]});await env.workspace.openRow(env.workspace.items[0]);
  const e=new PhoneTodoDetailEditor();e.sync(env.workspace);
  env.state.request=()=>({todo:structuredClone(env.state.todos[0])});
  let p=e.plan.copy();p.priority='high';e.change(env.workspace,p,'priority');await e.flush(env.workspace);
  assert.equal(e.plan.priority,'');assert.equal(env.workspace.error,'todo_detail_unsupported');assert.deepEqual(e.unavailable,['priority']);
  env.state.request=()=>({todo:structuredClone(env.state.todos[1])});
  e.focus(env.workspace);e.type('new');await e.finish(env.workspace);assert.equal(env.workspace.error,'request_updated');assert.equal(env.workspace.selected.id,'first');
  env.state.request=()=>{throw new HttpFailure(403,'forbidden')};p=e.plan.copy();p.project='test';e.change(env.workspace,p,'project');await e.flush(env.workspace);
  assert.equal(env.workspace.error,'permission_denied');let n=env.state.calls.length;assert.equal(env.workspace.detailEditable(),false);
  p=e.plan.copy();p.hasDue=true;e.change(env.workspace,p,'due');await e.flush(env.workspace);assert.equal(env.state.calls.length,n);
  env.state.generation++;env.workspace.detailReadOnly=false;e.focus(env.workspace);e.type('late');await e.finish(env.workspace);assert.equal(env.state.calls.length,n);
});

test('the ordinary failed task binds the visible Hub and manager and retires its recovery before another task or root can send',async()=>{
 const {workspace,client,state}=await opened({todos:[todo('failed',{origin:'origin',plan_id:'launch',start_error:'Waiting',done_at:'then'}),todo('other')]});
 await workspace.openRow(workspace.items[0]);
 let current,canWrite,get,owner; const probe={close(){},openTodo(client,selected,visible,can){owner=client;get=selected;current=visible;canWrite=can;}};
 workspace.openTaskStart(probe,()=>true);
 assert.equal(owner,client);assert.equal(get().id,'failed');assert.equal(current(),true);assert.equal(canWrite(),true);
 workspace.detailReadOnly=true;assert.equal(canWrite(),false);workspace.detailReadOnly=false;
 await workspace.openRow(workspace.items.find(row=>row.id==='other'));assert.equal(current(),false);
 assert.equal(state.calls.filter(c=>c.method==='POST').length,0);
});

test('a related disclosure in the phone lead toggles its own group and leaves the surrounding row available', () => {
  const page = readFileSync(new URL('../entry/src/main/ets/components/TodoWorkspacePage.ets', import.meta.url), 'utf8');
  const block = (start,end) => page.slice(page.indexOf(start), page.indexOf(end,page.indexOf(start)));
  const layout = block('@Builder phoneRowWords(', '@Builder todoRow(');
  assert.match(layout, /if \(binding\.value\.members\.length>1\) Button/);
  assert.match(layout, /height\(this\.phoneMeasure\(44,57\.7\)\)/);
  assert.match(layout, /onClick\(\(\) => this\.toggleGroup\(binding\.value\.key\)\)/);
  const methods = block('private toggleGroup(', 'private completionShown(') + block('private openLeadAt(', '@Builder phoneStatusWords(');
  const Harness = new Function('return ' + stripTypeScriptTypes('class TodoList {'+methods+'}'))();
  const list = new Harness();
  const id = 'original', target = {id, manager:'work'};
  const opened = [];
  list.phoneList = true;
  list.hubMode = () => false;
  list.expandedGroups = ['untouched'];
  list.workspace = {todoResource:item => item, supportsSeen:() => false};
  list.onOpenResource = item => opened.push(item);
  const group = 'owner/studio/plan';
  const binding = {value:{lead:target, members:[target, {id:'member'}], phone:{canFileSeen:false}}};
  for (const [left, top, width, height] of [[92,349.3,65,44],[105.7,453.7,91.7,57.7]]) {
    list.windowFrame = key => key === 'todos.related.'+id ? {left,top,width,height} : undefined;
    list.toggleGroup(group);
    list.openLeadAt(binding,{windowX:left + width / 2,windowY:top+height/2});
    assert.deepEqual(list.expandedGroups,['untouched',group]);
    assert.deepEqual(opened,[]);
    list.toggleGroup(group);
    assert.deepEqual(list.expandedGroups,['untouched']);
    list.openLeadAt(binding,{windowX:left+width,windowY:top+height/2});
    assert.equal(opened.pop(),target);
    list.openTodoAt(target, false, {windowX:left,windowY:top});
    assert.equal(opened.pop(),target,'a standalone or expanded member still opens its own task');
  }
});

test('the phone related count uses proportional numerals wherever its translation places the count', () => {
  const page=readFileSync(new URL('../entry/src/main/ets/components/TodoWorkspacePage.ets',import.meta.url),'utf8');
  const label=page.slice(page.indexOf('@Builder phoneRelatedLabel('),page.indexOf('@Builder phoneRowWords('));
  assert.match(label,/Span\(parts\[0\]\)[\s\S]*Span\(String\(count\)\)\.fontFamily\('Noto Sans'\)[\s\S]*Span\(parts\.slice\(1\)\.join\('\{count\}'\)\)/);
  assert.match(label,/fontFeature\('pnum 1'\)/);assert.doesNotMatch(label,/\.(?:width|maxWidth)\(/);
  for (const dir of ['base','en_US','ja_JP']) {
    const resource=new URL('../entry/src/main/resources/'+dir+'/element/phone_todo_list.json',import.meta.url);
    const data=JSON.parse(readFileSync(resource,'utf8')).string;
    const format=data.find(row=>row.name==='phone_todo_related_count').value;
    const parts=format.split('{count}');
    assert.equal(parts.length,2);
    for(const count of [1,5,12,137])assert.equal(parts[0]+String(count)+parts[1],format.replace('{count}',String(count)));
  }
});
