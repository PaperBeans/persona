import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks, stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(name,ctx,next) {
    if (ctx.parentURL?.endsWith('.ets') && ['../platform/HubTransport','./HubRepository','./ClientModel'].includes(name))
      return {url:'group-test:'+name,shortCircuit:true};
    if (ctx.parentURL?.endsWith('.ets') && name.startsWith('.')) return {url:new URL(name+'.ets',ctx.parentURL).href,shortCircuit:true};
    return next(name,ctx);
  },
  load(url,ctx,next) {
    if (url.startsWith('group-test:')) return {format:'module',shortCircuit:true,source:
      'export class HttpFailure extends Error {constructor(status){super(String(status));this.status=status}}; export class HubRepository {}; export class ClientModel {}; export const segment=encodeURIComponent;'};
    if (!url.endsWith('.ets')) return next(url,ctx);
    return {format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*|@Trace\s+/g,''))};
  }
});
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const {HttpFailure}=await import('group-test:../platform/HubTransport');
const {AgentTaskGrouping}=await import('../entry/src/main/ets/models/AgentTaskGrouping.ets');
const {todoAgentSections,directionAgentSections}=await import('../entry/src/main/ets/models/AgentTaskSections.ets');
const off=()=>false;
function row(kind,id,data={},group='home',manager='assistant') {
  const r=ResourceRow.from({id,title:id,...data},kind);r.group=group;r.manager=manager;return r;
}
const session=(id,data={},group)=>row('session',id,{session_id:id,machine_id:'studio',state:'idle',...data},group);
const task=(id,agents=[],data={})=>row('todo',id,{agents,...data});
const linked=(id,data={})=>({machine_id:'studio',session_id:id,...data});
const find=(sections,key)=>sections.find(s=>s.key===key);
const ids=section=>section.lines.map(s=>s.row.id);
const lists=(tasks,shown,filtered=false,muted=off)=>todoAgentSections(tasks,shown,'home','assistant',filtered,muted);
const dirs=(tree,shown,known=shown,filtered=false,conversations=[])=>directionAgentSections(tree,shown,known,conversations,'home','assistant',filtered,off);
function client(owner='assistant') {
  const c={profile:{id:'home',manager:owner},connected:true,demo:false,gen:1,todos:[],scope(){return JSON.stringify([this.profile.id,this.profile.manager])},connectionGeneration(){return this.gen},owns(gen,scope){return this.gen===gen&&this.scope()===scope}};
  const repo={isDefaultManager:()=>c.profile.manager==='assistant',todos:async()=>[],call:async path=>{assert.equal(path,'/api/v1/assistant/directions/tree');return {directions:[]}}};
  c.repo=()=>repo;return {c,repo};
}

test('successful empty reads offer both modes, only a 404 withdraws them, and the newest live inventory supplies cards',async()=>{
  const g=new AgentTaskGrouping(),{c,repo}=client();
  await g.refresh(c);assert.deepEqual([g.supportsTodos,g.supportsDirections],[true,true]);
  const todo=task('one');repo.todos=async()=>[todo];repo.call=async()=>({directions:[{id:'lead'}]});await g.refresh(c);
  assert.deepEqual(g.currentTodos(c).map(t=>t.id),['one']);c.todos=[task('new')];assert.equal(g.currentTodos(c)[0].id,'new');
  const fail=status=>async()=>{throw new HttpFailure(status)};
  repo.todos=fail(503);repo.call=fail(502);await g.refresh(c);assert.deepEqual([g.supportsTodos,g.supportsDirections,g.directions[0].id],[true,true,'lead']);
  repo.todos=fail(404);repo.call=fail(404);await g.refresh(c);assert.deepEqual([g.supportsTodos,g.supportsDirections,g.directions.length],[false,false,0]);
  repo.todos=async()=>[];repo.call=async()=>({});await g.refresh(c);assert.deepEqual([g.supportsTodos,g.supportsDirections],[true,true]);
});
test('the built-in offline demo uses its existing local repository and routes its own task card',async()=>{
  const {c,repo}=client('demo'),g=new AgentTaskGrouping();c.demo=true;c.profile.id='demo';
  repo.todos=async()=>[row('todo','local',{agents:[linked('a')]},'demo','demo')];await g.refresh(c);
  const local=row('session','a',{machine_id:'studio',session_id:'a'},'demo','demo');
  const card=todoAgentSections(g.currentTodos(c),[local],'demo','demo',false,off)[0].todo;
  assert.deepEqual([g.supportsTodos,g.supportsDirections,card.id,card.group,card.manager],[true,false,'local','demo','demo']);
});
test('a manager switch and a reconnect discard superseded reads, a foreign manager never requests the default direction tree',async()=>{
  const g=new AgentTaskGrouping(),{c,repo}=client(),first=Promise.withResolvers();let todos=0,trees=0;
  repo.todos=async()=>{todos++;return first.promise};repo.call=async()=>{trees++;await first.promise;return {directions:[{id:'private'}]}};
  const prior=g.refresh(c),duplicate=g.refresh(c);c.profile.manager='other';c.gen++;
  repo.todos=async()=>[];await g.refresh(c);first.resolve([task('private')]);await Promise.all([prior,duplicate]);
  assert.deepEqual([g.supportsTodos,g.supportsDirections,g.directions.length,g.currentTodos(c).length,todos,trees],[true,false,0,0,1,1]);
  c.profile.manager='assistant';c.gen++;const stale=Promise.withResolvers();repo.call=async()=>stale.promise;
  const older=g.refresh(c);c.gen++;repo.call=async()=>({directions:[{id:'current'}]});await g.refresh(c);
  stale.resolve({directions:[{id:'superseded'}]});await older;assert.equal(g.directions[0].id,'current');
});
test('todo order, active and reverse links, scope, manual/automatic buckets and the fold rules follow the visible sessions',()=>{
  const urgent=session('u',{status:'blocked',state:undefined}), working=session('w',{state:'working'}),reverse=session('x',{todo:{id:'due'}}),
    manual=session('m',{space_kind:'herdr',started_by_jarvis:false}),automatic=session('a',{space_kind:'herdr',started_by_jarvis:true}),
    foreign=session('u',{state:'unread'},'remote'),other=session('o',{state:'offline'}),old=session('old',{state:'unread'});
  const todos=[task('late',[linked('w')],{due_date:'2030-05-01'}),task('urgent',[linked('u',{dismissed_at:'yesterday'}),linked('old',{released_at:'yesterday'})]),
    task('due',[linked('w')],{due_date:'2029-03-01'}),task('closed',[linked('old')],{done_at:'2026-11-21'})];
  const grouped=lists(todos,[manual,automatic,foreign,reverse,working,urgent,other,old]);
  assert.deepEqual(grouped.map(g=>g.key),['todo:urgent','todo:due','todo:manual','todo:none']);
  assert.deepEqual(ids(grouped[0]),['u']);assert.equal(grouped[0].state,'needs_you');
  assert.deepEqual(ids(grouped[1]),['w','x']);assert.equal(grouped[1].todo.group,'home');assert.equal(grouped[1].todo.manager,'assistant');
  assert.deepEqual(ids(grouped[2]),['m']);assert.equal(grouped[2].folded,true);assert.equal(grouped[3].folded,true);
  assert.ok(grouped[3].lines.some(l=>l.row===foreign));assert.ok(grouped[3].lines.some(l=>l.row===automatic));
  const empty=lists([todos[1]],[urgent]);assert.deepEqual(ids(find(empty,'todo:none')),[]);assert.ok(find(empty,'todo:manual').hint);
  assert.deepEqual(lists([todos[1]],[urgent],true).map(g=>g.key),['todo:urgent']);
  assert.equal(lists([],[manual])[0].folded,false);assert.deepEqual(lists(todos,[]),[]);
});
test('tree uses the Hub order, depth, state and targets without resurrecting filtered or pinned sessions or leaking to a different Hub',()=>{
  const parent=session('parent'),hidden=session('hidden'),foreign=session('parent',{state:'working'},'remote'),manual=session('manual',{space_kind:'herdr'});
  const branch=linked('parent',{children:[linked('parent@child',{agent:'codex',state:'working'}),linked('hidden')]});
  const tree=[{id:'empty',tasks:[]},{id:'lead',counts:{working:1,needs_you:2},tasks:[
    {id:'new',state:'future',agents:[branch]},{id:'another',agents:[branch]},{id:'idle',state:'planned',agents:[]}]},
    {id:'sec',secretary:true,tasks:[{id:'misc'}]}];
  const groups=dirs(tree,[parent,manual,foreign],[parent,hidden,manual,foreign],false,[row('conversation','lead',{title:'My direction'})]);
  assert.deepEqual(groups.map(g=>g.key),['direction:lead','direction:sec','todo:manual','todo:none']);
  const lead=groups[0];assert.equal(lead.title,'My direction');assert.equal(lead.running,true);assert.equal(lead.needs,2);
  assert.deepEqual(ids(lead),['new','parent','parent@child','another','idle']);
  assert.deepEqual(lead.lines.map(l=>[l.kind,l.depth]),[['task',0],['session',1],['tree',2],['task',0],['task',0]]);
  const first=lead.lines[0],child=lead.lines[2];assert.equal(first.state,'working');assert.deepEqual([child.row.manager,child.row.group,child.row.machine],['assistant','home','studio']);
  assert.equal(groups[1].titleKey,'agent_task_secretary');assert.equal(groups[2].folded,false);assert.equal(groups[3].folded,true);
  const narrow=dirs(tree,[parent],[parent,hidden],true);assert.deepEqual(narrow.map(g=>g.key),['direction:lead']);assert.deepEqual(ids(narrow[0]),['new','parent']);
  assert.deepEqual(dirs(tree,[],[parent,hidden],true),[]);
});
