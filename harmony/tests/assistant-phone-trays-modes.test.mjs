import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier==='@kit.ArkTS') return {url:'phone:arkts',shortCircuit:true};
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
    if (url==='phone:arkts') return {format:'module',source:'export const util={generateRandomUUID:()=>crypto.randomUUID()}',shortCircuit:true};
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

const { assistantBoardRows, assistantTrayRows, TrayRequest }=await import('../entry/src/main/ets/models/AssistantTrays.ets');
const { ResourceRow }=await import('../entry/src/main/ets/models/Wire.ets');
const { composerModelName, validComposerModelID, SessionComposerModes }=await import('../entry/src/main/ets/models/SessionComposerModes.ets');

test('an expanded pending plan scrolls once and pins only its own current authorized actions', async () => {
  const cards=readFileSync(new URL('../entry/src/main/ets/components/AssistantCards.ets',import.meta.url),'utf8');
  const names=['pinnedTrayPlan','alignOpenedTrayPlan','activateTray','trayPlanAction','trayHeight'];
  const members=names.map(name=>{
    const match=cards.match(new RegExp('^  (?:private |@Computed get )'+name+'\\([^]*?^  }','m'));
    assert.ok(match,name);
    return match[0].replace('@Computed get ','get ');
  }).join('\n');
  const {Wire}=await import('../entry/src/main/ets/models/Wire.ets');
  const Harness=new Function('Wire','return '+stripTypeScriptTypes('class Tray {'+members+'}'))(Wire);
  const tray=new Harness(),reads=[],writes=[],scrolls=[];
  const plan={id:'p',kind:'plan',bucket:'needs',data:{id:'p',state:'open',conversation:'main',tasks:[{title:'First'}]},key:()=>'plan:p'};
  let scope='hub-a/owner/main',open=true,authorized=true,fresh=false;
  Object.assign(tray,{active:true,trayOpen:true,trayBucket:'needs',trayOwner:scope,trayScope:()=>scope,trayRows:[plan],expandedEntry:'',
    trayScroller:{scrollTo:position=>scrolls.push(position)},planId:item=>item.id,planActionsHeight:60,dockRoom:350,composerHeight:74,actionBarHeight:44,
    trayContentHeight:700,feed:{planState:()=>open?'open':'started',planIsFresh:()=>fresh,planLoading:()=>false,refreshPlan:id=>{reads.push(id);fresh=true;},
      planCanEdit:()=>authorized,planCanStart:()=>authorized,planCanDismiss:()=>authorized,rememberPlan:()=>false,
      editPlan:id=>writes.push(['edit',id]),startPlan:(...args)=>writes.push(['start',...args]),dismissPlan:(...args)=>writes.push(['dismiss',...args])}});
  assert.equal(tray.pinnedTrayPlan,undefined);
  tray.activateTray(plan);
  assert.equal(tray.pinnedTrayPlan,plan); assert.deepEqual(reads,['p']); assert.deepEqual(writes,[]);
  tray.alignOpenedTrayPlan(plan,{position:{y:133},height:430});
  tray.alignOpenedTrayPlan(plan,{position:{y:83},height:430});
  assert.deepEqual(scrolls,[{xOffset:0,yOffset:133}],'keyboard or manual scrolling must not recenter an already-open row');
  assert.equal(tray.trayHeight(),102);tray.trayContentHeight=48;assert.equal(tray.trayHeight(),48);
  tray.composerHeight=250;assert.equal(tray.trayHeight(),0,'keep the real footer accessible when the input consumes the available space');
  const original=tray.trayOwner;
  tray.trayPlanAction(plan,original,'edit');assert.deepEqual(writes,[['edit','p']]);
  authorized=false;
  for(const action of ['edit','start','dismiss']) tray.trayPlanAction(plan,original,action);
  authorized=true;scope='hub-b/owner/main';tray.trayOwner=scope;
  for(const action of ['edit','start','dismiss']) tray.trayPlanAction(plan,original,action);
  tray.trayPlanAction({...plan,data:{...plan.data,conversation:'another'}},scope,'start');
  open=false;assert.equal(tray.pinnedTrayPlan,undefined);tray.trayPlanAction(plan,scope,'dismiss');
  assert.equal(writes.length,1);
  open=true;tray.trayPlanAction(plan,scope,'start');tray.trayPlanAction(plan,scope,'dismiss');
  assert.deepEqual(writes.slice(1),[['start','p',false,undefined,JSON.stringify(plan.data)],['dismiss','p',JSON.stringify(plan.data)]]);
  tray.activateTray(plan);assert.equal(tray.pinnedTrayPlan,undefined);assert.equal(tray.planScrollKey,'');
  assert.match(cards,/Scroll\(this\.trayScroller\)[^]*?\.id\('action-tray\.rows'\)\s*if \(this\.pinnedTrayPlan\) this\.pinnedPlanActions/);
  assert.match(cards,/if \(!actionsPinned\) Row/);
});
function session(id,state='idle',details={}) {
  const row=ResourceRow.from({machine_id:'machine',session_id:id,manager:'owner',agent:'claude',launcher:'claude',pane_id:'pane',status:'idle',state,model:'claude-opus-5-5',effort:'high',...details},'session');
  row.group='group';return row;
}
test('main conversation combines manual and handed-out tasks, plans, draft replies, and independent agents; each direct request has one home',()=>{
  const now=Date.parse('2026-06-01T10:00:00Z'), time=(hours)=>new Date(now+hours*3600e3).toISOString();
  const linked={machine_id:'machine',session_id:'waiting'};
  const todos=[
    {id:'card',title:'Origin card',origin:'main',agents:[linked],created_at:time(-2)},
    {id:'manual',title:'Explicit home',agents:[linked],created_at:time(-1)},
    {id:'held',origin:'other',delivery:{state:'pr',unblock:'user'},created_at:time(-1)},
    {id:'recent',origin:'other',done_at:time(-23)}, {id:'old',origin:'other',done_at:time(-25)},
    {id:'local-done',done_at:time(-1)},{id:'other-manager',manager:'foreign',created_at:time(-1)}
  ];
  const sessions=[session('waiting','needs_you',{todo:{id:'manual'}}),session('standalone','working'),session('assistant','working',{assistant:true}),
    session('far','working',{manager:'foreign'}),session('dropped','working',{todo:{id:'unknown'}})];
  const alien=session('same','needs_you');alien.group='different';sessions.push(alien);
  const feed=[{id:'feed-plan',kind:'plan',ref:'plan'},{id:'reply',kind:'message',ref:'inbox',body:{sender:'Human',draft:'Reply'}},
    {id:'sent',kind:'message',ref:'inbox',body:{sent_at:time(-1)}}], plans={plan:{id:'plan',state:'open',conversation:'main',tasks:[{title:'Plan'}]}};
  const rows=assistantTrayRows(todos,sessions,feed,plans,[new TrayRequest('machine','waiting','ask','Question',now,true)],'owner','group','main','main',now);
  assert.deepEqual(rows.filter(r=>r.bucket==='needs').map(r=>r.id),['manual','plan','reply','held']);
  assert.deepEqual(rows.filter(r=>r.bucket==='done').map(r=>r.id),['recent']);
  assert.deepEqual(rows.filter(r=>r.bucket==='progress').map(r=>r.kind),['todo','agent']);
  assert.equal(rows.find(r=>r.id==='recent').resource.group,'group');
  assert.equal(rows.find(r=>r.id==='manual').status,'needs_you');assert.equal(rows.find(r=>r.id==='card').status,'working');
  assert.deepEqual(assistantTrayRows(todos,sessions,[],{},[],'owner','group','other','main',now).map(r=>r.id),['held','recent']);
});
test('Main lists normal open plans and direct user decisions while routing delegated questions, ordinary permissions, failed starts and supervisor reviews', async()=>{
  const {directUserRequest}=await import('../entry/src/main/ets/models/InteractionDecision.ets');
  const decision=(kind,raw={})=>directUserRequest({kind,raw:JSON.stringify({interaction:raw})});
  assert.equal(decision('question'),true);
  assert.equal(decision('question',{delegating:true}),false);
  assert.equal(decision('elicitation'),true);
  assert.equal(decision('permission'),false);
  assert.equal(decision('permission',{owner_proposal:true}),true);
  assert.equal(decision('plan',{owner_proposal:true}),false);
  const now=Date.parse('2026-06-01T10:00:00Z'), earlier='2026-06-01T07:00:00Z';
  const base={origin:'ordinary',created_at:earlier};
  const todos=[{...base,id:'failure',start_error:'did not start'}, {...base,id:'supervisor',supervision:{state:'pending_review',pending_since:earlier}},
    {...base,id:'owner',ask:{verdict:'blocked',asked_at:earlier,outcome:'decide'}},
    {...base,id:'handoff',ask:{verdict:'blocked',asked_at:earlier,outcome:'cannot',why_owner:'Only the owner can select it'}}];
  const sessions=[session('agent','needs_you'),session('named','needs_you')];
  const reqs=[new TrayRequest('machine','agent','perm','Permission',now,false),new TrayRequest('machine','named','ask','Question',now,true)];
  const plans={p:{id:'p',conversation:'ordinary',state:'open',tasks:[{title:'Open plan'}]}},feed=[{id:'plan',kind:'plan',ref:'p'}];
  const main=assistantTrayRows(todos,sessions,feed,plans,reqs,'owner','group','main','main',now);
  assert.deepEqual(new Set(main.filter(r=>r.bucket==='needs').map(r=>r.id)),new Set(['owner','handoff','p',JSON.stringify(['machine','named'])]));
  assert.ok(!main.some(r=>['failure','supervisor',JSON.stringify(['machine','agent'])].includes(r.id)));
  const ordinary=assistantTrayRows(todos,sessions,feed,plans,reqs,'owner','group','ordinary','main',now);
  assert.deepEqual(new Set(ordinary.filter(r=>r.bucket==='needs').map(r=>r.id)),new Set(['failure','supervisor','owner','handoff','p']));
});
test('open plan rows come from the scoped plan inventory without needing a local feed item',()=>{
  const task=(id,conversation,extra={})=>({id,conversation,state:'open',tasks:[{title:id+' title',prompt:id+' prompt'}],...extra});
  const plans={local:task('local','main'),domain:task('domain','research',{space:'design'}),medical:task('medical','clinic',{space:'health'}),
    legacy:task('legacy','ordinary'),done:task('done','ordinary',{state:'started'}),other:task('other','ordinary',{manager:'elsewhere'}),
    sameHub:task('sameHub','main',{group:'foreign'}),invalidKey:task('another-id','main')};
  const items=[{id:'first',kind:'plan',ref:'local'},{id:'duplicate',kind:'plan',ref:'local'}];
  const tray=(id,main='main')=>assistantTrayRows([],[],items,plans,[],'owner','group',id,main,0);
  assert.deepEqual(tray('main').map(r=>r.id),['domain','legacy','local']);
  assert.deepEqual(tray('ordinary').map(r=>r.id),['legacy']);
  assert.deepEqual(tray('clinic').map(r=>r.id),['medical']);
  assert.deepEqual(tray('research').map(r=>r.id),['domain']);
  assert.deepEqual(tray('main','').map(r=>r.id),['local']);
  assert.equal(tray('main')[0].data,plans.domain);
  assert.equal(tray('main')[0].title,'domain title');
});

test('a held review card and a failed supervised start do not ask the person; a newly explicit handoff does',()=>{
  const machine='machine', supervisor={supervisor:'supervising-chat'};
  const todos=[{id:'linked',origin:'ordinary',supervision:supervisor,agents:[{machine_id:machine,session_id:'waiting'}]},
    {id:'failed',origin:'ordinary',start_error:'failed',supervision:supervisor}];
  const waiting=session('waiting','needs_you'),hold=new TrayRequest(machine,'waiting','held','Held by supervisor',2,true,true);
  const needs=(reqs,conv='ordinary')=>assistantTrayRows(todos,[waiting],[],{},reqs,'owner','group',conv,'main',3).filter(r=>r.bucket==='needs');
  assert.deepEqual(needs([hold]),[]);
  assert.deepEqual(needs([hold],'main'),[]);
  const handoff=new TrayRequest(machine,'waiting','handed-out','Ask the owner',4,true);
  assert.deepEqual(needs([hold,handoff]).map(r=>[r.id,r.note]),[['linked','Ask the owner']]);
  assert.deepEqual(needs([hold,handoff],'main').map(r=>r.id),['linked']);
});

test('the conversation board retains its complete history, own tasks, and unclaimed main Agents',()=>{
  const todos=[
    {id:'active',origin:'main',title:'Assigned here',created_at:'2026-06-01T10:00:00Z',agents:[{machine_id:'machine',session_id:'worker'}]},
    {id:'old',origin:'main',title:'Old completed task',created_at:'2026-01-01T10:00:00Z',done_at:'2026-01-02T10:00:00Z'},
    {id:'failed',origin:'main',title:'Launch failure',start_error:'not started'},
    {id:'elsewhere',origin:'other',title:'Other chat',agents:[{machine_id:'machine',session_id:'other-agent'}]},
    {id:'foreign',manager:'foreign',origin:'main',title:'Other manager'}];
  const sessions=[session('worker','working'),session('unclaimed','needs_you'),session('other-agent','working')];
  const board=assistantBoardRows(todos,sessions,'owner','group','main','main');
  assert.deepEqual(board.map(row=>row.title),['Assigned here','Old completed task','Launch failure',sessions[1].title]);
  assert.deepEqual(board.map(row=>row.bucket),['working','done','failed','needs']);
  assert.equal(board[0].resource.group,'group');
  assert.deepEqual(assistantBoardRows(todos,sessions,'owner','group','other','main').map(row=>row.id),['elsewhere']);
  const now=Date.parse('2026-06-01T11:00:00Z'); sessions[0].state='needs_you';
  const rows=assistantTrayRows(todos,sessions,[{id:'m',ref:'mail',kind:'message',body:{text:'Incoming question\nquoted text',draft:'Private draft'}}],{},
    [new TrayRequest('machine','worker','ix','Original user question',now,true)],'owner','group','main','main',now);
  assert.equal(rows.find(row=>row.id==='active').note,'Original user question');
  assert.equal(rows.find(row=>row.id==='m').note,'Incoming question');
});

function owner() {
  const row=session('chat'), calls=[], trace={scope:'hub-A',generation:1,target:'chat',compose:true,blocked:[],pending:'',afterJournal:undefined};
  const repo={mutations:{blocked:()=>trace.blocked},async call(path,method='GET',body={},op='',admit){
    if(method==='GET') {calls.push({path,method}); return {models:[{id:'default',default:true,resolved:'claude-opus-5-5'},{id:'opus',name:'Opus 5.5',resolved:'claude-opus-5-5'},{id:'sonnet',name:'Sonnet'}]};}
    trace.blocked.push({path,id:op});trace.afterJournal?.();
    if(!admit()) {trace.blocked=trace.blocked.filter(r=>r.id!==op);throw Error('request_updated');}
    calls.push({method,path,body,op});trace.blocked=[];
    return trace.pending ? {pending_effort:trace.pending} : {};
  }};
  const client={selected:row,sessions:[row],connected:true,draft:'untouched draft',decision:undefined,scope:()=>trace.scope,connectionGeneration:()=>trace.generation,
    profile:{manager:'owner',id:'group'},target:()=>trace.target,repo:()=>repo,canCompose:()=>trace.compose,operationBlocked:p=>trace.blocked.some(r=>r.path===p),refresh:async()=>{}};
  return {client,trace,calls,row,model:new SessionComposerModes()};
}
test('focused composer reads live model, offers server models, queues explicit effort and leaves the draft and message endpoint untouched',async()=>{
  const {client,trace,calls,model}=owner();await model.load(client);
  assert.equal(composerModelName('claude-opus-5-5'),'Opus 5.5');assert.equal(composerModelName('other/any'),'other/any');
  assert.equal(model.modelName(client),'Opus 5.5');assert.equal(model.chosenModel(client),'opus');assert.equal(model.currentEffort(client),'high');
  assert.equal(await model.change(client,'model','opus',()=>true),false);
  assert.equal(await model.change(client,'model','unknown',()=>true),false);
  assert.equal(await model.change(client,'model','sonnet',()=>true),true);
  trace.pending='low';assert.equal(await model.change(client,'effort','low',()=>true),true);
  assert.deepEqual(calls.map(c=>[c.method,c.path]),[['GET','/api/v1/machines/machine/launchers/claude/models'],['POST','/api/v1/sessions/machine/chat/modes'],['POST','/api/v1/sessions/machine/chat/modes']]);
  assert.deepEqual(calls[1].body,{model:'sonnet'});assert.deepEqual(calls[2].body,{effort:'low',queue_effort:true});assert.equal(model.pendingEffort(client),'low');
  assert.equal(client.draft,'untouched draft');
});
test('mode writes recheck owner, selected target, capabilities, active form, existing journal and agent activity at storage admission',async()=>{
  for(const change of ['scope','target','capability','decision','pending','view','working']) {
    const {client,trace,calls,row,model}=owner();let visible=true; await model.load(client);
    trace.afterJournal=()=>{
      if(change==='scope') trace.scope='other';
      if(change==='target') trace.target='other';
      if(change==='capability') trace.compose=false;
      if(change==='decision') client.decision={};
      if(change==='pending') trace.blocked.push({path:'/api/v1/sessions/machine/chat/modes',id:'another'});
      if(change==='view') visible=false;
      if(change==='working') row.state='working';
    };
    assert.equal(await model.change(client,'model','sonnet',()=>visible),false,change);
    assert.equal(calls.filter(c=>c.method==='POST').length,0);assert.equal(client.draft,'untouched draft');
  }
});
test('effort-only runtime honors exact advertised levels, suppresses foreign-session collisions and explicit refusal of model changes',async()=>{
  const {client,model,calls}=owner(), codex=session('chat','idle',{agent:'codex',launcher:'codex',efforts:['medium','ultra'],capabilities:{modes:false,model:false}});
  const foreign=session('chat','idle',{manager:'someone-else',model:'private'});client.sessions=[foreign,codex];client.selected=codex;
  await model.load(client);assert.deepEqual(model.efforts(client),['medium','ultra']);assert.deepEqual(model.modeValues(client),[]);assert.equal(model.acceptsModel(client),false);
  assert.equal(calls.length,0);assert.equal(await model.change(client,'effort','max',()=>true),false);assert.equal(await model.change(client,'effort','ultra',()=>true),true);
  client.sessions=[foreign];assert.equal(model.visible(client),false);assert.equal(await model.change(client,'effort','medium',()=>true),false);
});

test('typed launcher models accept the Hub ID grammar, never send the typed string as a message, and honor explicit model support',async()=>{
  for (const id of ['~latest','provider/family:model[1m]','a'.repeat(256)]) assert.ok(validComposerModelID(id),id);
  for (const id of ['~','/foo','bad model','model\nother','非ASCII','a'.repeat(257)]) assert.equal(validComposerModelID(id),false,id);
  const {client,model,calls,row}=owner();await model.load(client);
  assert.equal(await model.change(client,'custom_model','bad model',()=>true),false);
  assert.equal(await model.change(client,'custom_model','vendor/new-id',()=>true),true);
  assert.deepEqual(calls.at(-1).body,{model:'vendor/new-id'});
  row.raw=JSON.stringify({...row.data(),capabilities:{model:false}});
  assert.equal(await model.change(client,'custom_model','vendor/other',()=>true),false);
  assert.equal(calls.filter(c=>c.method==='POST').length,1);assert.equal(client.draft,'untouched draft');
});

test('the Agent header distinguishes a shown permission mode from an allowed switch on the exact session',async()=>{
  const {SessionHeaderPermissions:Permissions,headerPermissionRow}=await import('../entry/src/main/ets/models/SessionHeaderPermissions.ets');
  const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
  const row=(fields={})=>{const r=ResourceRow.from({machine_id:'same-host',session_id:'same-id',manager:'m',state:'idle',...fields},'session');r.group='g';return r;};
  const old=new Permissions(row({agent:'claude',permission_mode:'default',pane_id:'p1'}));
  assert.equal(old.visible,true);assert.equal(old.restarts,true);assert.equal(old.confirmation('bypassPermissions'),'enter');
  const noMode=new Permissions(row({agent:'codex',capabilities:{send:true,modes:false}}));assert.equal(noMode.visible,false);
  const readOnly=new Permissions(row({agent:'codex',state:'offline',permission_mode:'plan',capabilities:{send:false,modes:false}}));
  assert.equal(readOnly.visible,true);assert.equal(readOnly.permits('default'),false);
  const listed=new Permissions(row({agent:'codex',capabilities:{send:true,modes:false,permission_modes:['default','bypassPermissions']}}));
  assert.deepEqual(listed.choices,['default','bypassPermissions']);assert.equal(listed.current,'default');
  assert.equal(listed.nameKey('default'),'header_permission_launcher');assert.equal(listed.confirmation('bypassPermissions'),'enter');
  assert.equal(listed.permits('plan'),false);
  const trusted=new Permissions(row({agent:'agy',permission_mode:'default',capabilities:{send:true,modes:true}}));
  assert.equal(trusted.permits('bypassPermissions'),true);assert.equal(trusted.confirmation('bypassPermissions'),'');
  const legacy=new Permissions(row({permission_mode:'bypassPermissions',agent:'claude',capabilities:{send:true,modes:true}}));
  assert.equal(legacy.confirmation('plan'),'leave');
  const pending=new Permissions(row({permission_mode:'default',agent:'claude',bypass_pending:true}));assert.equal(pending.permits('default'),true);
  assert.equal(new Permissions(row({assistant:true,permission_mode:'default'})).visible,false);
  const source=row({permission_mode:'default'}),other=row({permission_mode:'plan'});other.group='other';
  assert.equal(headerPermissionRow(source,[other],'g','m'),source);assert.equal(headerPermissionRow(other,[source],'g','m').kind,'');
});

test('the board shows the supervising conversation’s actual ask, work and machine, keeps finished and failed distinct, and leaves the draft alone',()=>{
  const now=Date.now();
  const question=new TrayRequest('machine','own','ask','Use suite A?\nOr suite B?',now,true);
  const foreignQ=new TrayRequest('other','own','foreign','Unrelated',now-1,true);
  const details={requests:[foreignQ,question],machineName:id=>({machine:'Mac',elsewhere:'Desk'})[id]||id,
    orphanedLine:reason=>'Orphaned '+reason,plans:{p:{id:'p',conversation:'main',tasks:[{todo_id:'failed',machine:'machine',cwd:'/source/pkg',error:'machine refused'}]}}};
  const shell=(id,title,overrides={})=>({id,title,origin:'main',manager:'owner',notes:'unrelated private notes',...overrides});
  const link={machine_id:'machine',session_id:'own',cwd:'/src/lab'};
  const active=session('own','needs_you',{last_text:'old words',cwd:'/src/lab'});
  const other=session('manual','working',{agent:'codex',cwd:'/src/side',last_text:'shipping'});
  const warm=session('working','working',{agent:'claude',cwd:'/src/lab',last_text:'last',activity:'testing'});
  const tasks=[shell('asked','Asked',{agents:[link]}),shell('working','Working',{agents:[{...link,session_id:'working'}]}),
    shell('failed','Start failed',{plan_id:'p',start_error:'machine refused',done_at:'2026-01-01T10:00:00Z'}),
    shell('done','Completed',{done_at:'2026-01-01T10:00:00Z'}),shell('orphan','No owner',{orphaned:{reason:'closed'}}),
    shell('scope','Other space',{origin:'other',agents:[{...link,session_id:'unrelated'}]})];
  const rows=assistantBoardRows(tasks,[active,other,warm],'owner','group','main','main',details);
  const find=id=>rows.find(v=>v.id===id), actual=(id,group,text)=>{assert.equal(find(id).bucket,group);assert.equal(find(id).note,text);};
  actual('asked','needs','Mac · lab · Claude · Use suite A? Or suite B?');
  actual('working','working','Mac · lab · Claude · testing');
  actual('failed','failed','Mac · pkg · machine refused');actual('done','done','');actual('orphan','working','Orphaned closed');
  assert.equal(find('scope'),undefined);assert.equal(rows.find(r=>r.kind==='agent').note,'Mac · side · Codex · shipping');
  assert.equal(tasks[0].notes,'unrelated private notes');
});

test('expanding the current ordinary plan obtains its authorization once and never dispatches it',()=>{
  const source=readFileSync(new URL('../entry/src/main/ets/components/AssistantCards.ets',import.meta.url),'utf8');
  const method=source.slice(source.indexOf('  private activateTray('),source.indexOf('  private trayLabel('));
  const View=new Function(stripTypeScriptTypes('class View { '+method+' }')+';return View')();
  const view=new View(), seen=[], entry={kind:'plan',key:()=> 'plan:one',data:{ref:'one'}};
  let loading=false,fresh=false;view.active=true;view.trayOpen=true;view.trayOwner='hub|regular';view.trayScope=()=> 'hub|regular';view.trayRows=[entry];view.expandedEntry='';
  view.planId=item=>item.ref;view.feed={planIsFresh:()=>fresh,planLoading:()=>loading,refreshPlan:id=>{seen.push(id);loading=true;}};
  view.activateTray(entry);assert.deepEqual(seen,['one']);assert.equal(view.expandedEntry,entry.key());
  view.activateTray(entry);assert.equal(view.expandedEntry,'');view.activateTray(entry);assert.deepEqual(seen,['one']);
  view.activateTray(entry);loading=false;fresh=true;view.activateTray(entry);assert.deepEqual(seen,['one']);
  view.activateTray(entry);view.trayOwner='another';fresh=false;view.activateTray(entry);assert.equal(view.expandedEntry,'');assert.deepEqual(seen,['one']);
});

const {TaskStartRecovery}=await import('../entry/src/main/ets/models/TaskStartRecovery.ets');
function recoveryCase() {
  const row=ResourceRow.from({id:'failed',title:'Alignment',origin:'main',manager:'owner'},'todo');row.group='group';row.manager='owner';
  const task={todo_id:'failed',machine:'mac',cwd:'/lab',launcher:'claude',error:'offline',retry_with:['codex']};
  const plan={id:'plan',conversation:'main',state:'started',tasks:[task]};
  const todo={id:'failed',origin:'main',manager:'owner',plan_id:'plan',title:'Alignment',done_at:'yesterday',start_error:'offline',agents:[]};
  const calls=[],ctx={online:true,role:true,active:true,blocked:false,epoch:1,changed:undefined,fail:false,unknown:false,commit:()=>true,nextRead:undefined};
  const feed={owner:'owner',conversationId:'main',todosReady:true,plans:{plan},todo:id=>id===todo.id?todo:{},refresh:async()=>{}};
  const repo={call:async(path,method='GET',body={},op,admit,completed,ack,empty=false)=>{
    calls.push({path,method,body,empty});
    if(method==='GET'){
      if(ctx.nextRead){const wait=ctx.nextRead;ctx.nextRead=undefined;await wait;}
      return path.includes('task-plans')?{plan:structuredClone(plan)}:{todo:structuredClone(ctx.changed||todo)};
    }
    if(!admit())throw Object.assign(new Error('updated'),{status:409});
    if(ctx.unknown)throw Object.assign(new Error('response lost'),{resultUnknown:true});
    if(ctx.fail)throw Object.assign(new Error('refused'),{status:409});
    const response=method==='DELETE'?{ok:true}:{todo:{...todo,start_error:'',done_at:null}};
    if (!ack(response))throw Error('failed acknowledgement'); return response;
  }};
  const client={connected:true,profile:{manager:'owner',id:'group'},features:['task_retry_launcher'],repo:()=>repo,scope:()=> 'group:owner',
    connectionGeneration:()=>ctx.epoch,owns:n=>n===ctx.epoch,canApprove:()=>ctx.role,operationBlocked:()=>ctx.blocked};
  const ctl=new TaskStartRecovery();ctl.open(client,feed,row,()=>ctx.active);
  return {ctl,ctx,client,feed,calls,todo,plan,task,row};
}
const settleRecovery=async ctl=>{for(let i=0;i<16&&ctl.busy;i++)await Promise.resolve();};
test('an owned task failure rereads the target and its plan, submits one bodyless Retry, and retains the ordinary to-do',async()=>{
 const {ctl,ctx,calls,todo}=recoveryCase();await settleRecovery(ctl);assert.equal(ctl.state,'ready');assert.equal(ctl.alternative,'codex');
 let next;ctx.nextRead=new Promise(r=>next=r);const p=ctl.act('retry');assert.equal(await ctl.act('retry'),false);next();assert.equal(await p,true);
 assert.deepEqual(calls.map(c=>[c.method,c.path]),[['GET','/api/v1/todos/failed?manager=owner'],['GET','/api/v1/task-plans/plan?manager=owner'],
 ['GET','/api/v1/todos/failed?manager=owner'],['GET','/api/v1/task-plans/plan?manager=owner'],['POST','/api/v1/todos/failed/retry?manager=owner']]);
 assert.equal(calls.at(-1).empty,true);assert.equal(ctl.state,'starting');assert.equal(todo.title,'Alignment');assert.equal(await ctl.act('retry'),false);
});
test('a different owner, failed round, revoked write authority or hidden sheet prevents retry and removal',async()=>{
 for(const alter of [
   v=>{v.ctx.role=false;},v=>{v.ctx.blocked=true;},v=>{v.ctx.epoch++;},v=>{v.ctx.active=false;},
   v=>{v.ctx.changed={...v.todo,origin:'elsewhere'};},v=>{v.ctx.changed={...v.todo,manager:'other'};},v=>{v.ctx.changed={...v.todo,manager:null};},v=>{v.ctx.changed={...v.todo,manager:{}};},v=>{v.ctx.changed={...v.todo,read_only:true};},v=>{v.ctx.changed={...v.todo,can_write:false};},
   v=>{v.ctx.changed={...v.todo,id:'substitute'};},v=>{v.ctx.changed={...v.todo,done_at:'newer'};},
   v=>{v.ctx.changed={...v.todo,start_error:''};v.task.error='';},v=>{v.plan.conversation='another';},v=>{v.plan.manager='wrong';},v=>{v.plan.tasks=[];}
 ]){
  const v=recoveryCase();await settleRecovery(v.ctl);alter(v);
  assert.equal(await v.ctl.act('retry'),false,String(alter));assert.equal(await v.ctl.act('remove'),false,String(alter));
  assert.equal(v.calls.filter(c=>c.method!=='GET').length,0);
 }
});
test('alternative retry requires the advertised launcher on the same attempt, and uncertain or denied retries never silently replay',async()=>{
 const v=recoveryCase();await settleRecovery(v.ctl);assert.equal(await v.ctl.act('retry','other'),false);v.client.features=[];
 assert.equal(await v.ctl.act('retry','codex'),false);assert.equal(v.calls.filter(c=>c.method==='POST').length,0);
 const alt=recoveryCase();await settleRecovery(alt.ctl);assert.equal(await alt.ctl.act('retry','codex'),true);
 assert.deepEqual(alt.calls.at(-1).body,{launcher:'codex'});assert.equal(alt.calls.at(-1).empty,false);
 const x=recoveryCase();await settleRecovery(x.ctl);x.ctx.unknown=true;assert.equal(await x.ctl.act('retry'),false);assert.equal(x.ctl.state,'unknown');
 await x.ctl.refresh();assert.equal(await x.ctl.act('retry'),false);assert.equal(x.calls.filter(c=>c.method==='POST').length,1);
 const y=recoveryCase();await settleRecovery(y.ctl);y.ctx.fail=true;assert.equal(await y.ctl.act('retry'),false);assert.equal(y.ctl.error,'request_updated');
 assert.equal(await y.ctl.act('retry'),false);const removed=recoveryCase();await settleRecovery(removed.ctl);assert.equal(await removed.ctl.act('remove'),true);
 assert.deepEqual(removed.calls.filter(c=>c.method==='DELETE').map(c=>c.path),['/api/v1/todos/failed?manager=owner']);
});

test('failed task removal asks for confirmation only if an Agent has previously run',async()=>{
 const plain=recoveryCase();await settleRecovery(plain.ctl);assert.equal(plain.ctl.confirmRemoval,false);
 const prior=recoveryCase();prior.todo.agents.push({machine_id:'mac',session_id:'former',released_at:'yesterday'});
 prior.ctl.open(prior.client,prior.feed,prior.row,()=>true);await settleRecovery(prior.ctl);assert.equal(prior.ctl.confirmRemoval,true);
 assert.equal(prior.calls.filter(entry=>entry.method==='DELETE').length,0);
});

const {TaskStartupQuestion}=await import('../entry/src/main/ets/models/TaskStartupQuestion.ets');
const offer={kind:'zsh_newuser',line:'Run setup?',choices:'y, n, q',screen:'(y/n/q)',default:'n',kept_until:'2099-01-01T00:00:00Z'};
async function shellCase(ordinary=false) {
  const v=recoveryCase(); await settleRecovery(v.ctl);
  v.client.features.push('todo_start_answer'); v.task.error_code='shell_waiting';v.task.error_args={...offer};
  v.task.start={attempt:1,pane:'w1:p1',state:'failed'};
  if (ordinary) v.ctl.openTodo(v.client,()=>v.todo,()=>v.ctx.active,()=>v.ctx.role,async()=>{});
  else v.ctl.open(v.client,v.feed,v.row,()=>v.ctx.active);
  await settleRecovery(v.ctl);return v;
}

test('task startup answers use the exact manager-bound route and one enumerated choice on both the ordinary and assistant task paths',async()=>{
 for (const ordinary of [false,true]) {
  const v=await shellCase(ordinary), question=v.ctl.question;
  assert.ok(question?.open());assert.deepEqual(question.choices,['y','n','q']); assert.equal(question.label('q'),'task_start_not_now');
  for(const value of ['','as typed','q\n','cancel']) assert.equal(await v.ctl.act('answer',value,question.attempt),false);
  assert.equal(await v.ctl.act('answer','q','from a previous question'),false);
  const submitting=v.ctl.act('answer','q',question.attempt);
  assert.equal(await v.ctl.act('answer','q',question.attempt),false); assert.equal(await submitting,true);
  assert.equal(v.ctl.question,undefined);assert.equal(v.ctl.state,'starting');
  assert.deepEqual(v.calls.filter(c=>c.method!=='GET'),[{path:'/api/v1/todos/failed/start/answer?manager=owner',method:'POST',body:{choice:'q'},empty:false}]);
 }
});

test('the selected startup answer stays pending until its one confirmed request completes', async () => {
  const path = new URL('../entry/src/main/ets/components/TaskStartupQuestionCard.ets', import.meta.url);
  const source = readFileSync(path, 'utf8');
  const method = source.slice(source.indexOf('  private async pick('), source.indexOf('  private timer:'));
  const Card = new Function(stripTypeScriptTypes('class Card { ' + method + ' }') + ';return Card')();
  const card = new Card();
  const run = await shellCase();
  card.recovery = run.ctl;
  card.submitting = '';
  const attempt = run.ctl.question.attempt;
  const repo = run.client.repo();
  const original = repo.call;
  let count = 0;
  let resume;
  let entered;
  const seen = new Promise(resolve => { entered = resolve; });
  const waiting = new Promise(resolve => { resume = resolve; });
  repo.call = async function(path, ...args) {
    if (path.includes('/start/answer')) { count++; entered(); await waiting; }
    return original.call(this, path, ...args);
  };
  const first = card.pick('q', attempt);
  try {
    await seen;
    assert.equal(card.submitting, JSON.stringify([attempt, 'q']));
    await card.pick('n', attempt);
    assert.equal(card.submitting, JSON.stringify([attempt, 'q']));
    assert.equal(count, 1);
  } finally {
    resume();
    await first;
  }
  assert.equal(card.submitting, '');
  assert.equal(count, 1);
});

test('only a current owner, target, feature and still-open startup attempt can send; a lost reply never repeats the answer',async()=>{
 for (const change of [v=>v.client.features=[],v=>v.ctx.role=false,v=>v.ctx.active=false,v=>v.ctx.epoch++,
   v=>v.client.profile.manager='other',v=>v.client.profile.id='other',v=>v.ctx.blocked=true,
   v=>v.task.start={...v.task.start,attempt:2},v=>v.task.error_args={...offer,line:'A different question?'},
   v=>v.task.error_args={...offer,kept_until:'2020-01-01T00:00:00Z'},v=>v.task.start={state:'remedying',attempt:2},
   v=>v.ctx.changed={...v.todo,manager:'other'},v=>v.ctx.changed={...v.todo,read_only:true}]) {
  const v=await shellCase(), id=v.ctl.question.attempt;change(v);
  assert.equal(await v.ctl.act('answer','q',id),false,change.toString());assert.deepEqual(v.calls.filter(c=>c.method==='POST'),[]);
 }
 const lost=await shellCase();lost.ctx.unknown=true;const id=lost.ctl.question.attempt;
 assert.equal(await lost.ctl.act('answer','q',id),false);await lost.ctl.refresh();
 assert.equal(lost.ctl.state,'unknown');assert.equal(await lost.ctl.act('answer','q',id),false);assert.equal(await lost.ctl.act('retry'),false);
 assert.equal(lost.calls.filter(c=>c.method==='POST').length,1);
});

test('password and host-key questions only expose safe offered IDs; missing expiry stays open while expired choices and old hubs stay absent',async()=>{
 const question=(args)=>new TaskStartupQuestion({...offer,...args},'original');
 assert.deepEqual(question({secret:'true',choices:'y,cancel'}).choices,['cancel']);
 assert.deepEqual(question({kind:'ssh_host_key',choices:'yes,no,cancel'}).choices,['no','cancel']);
 assert.equal(question({kept_until:'invalid'}).open(),true);assert.equal(question({kept_until:'2000-01-01T00:00:00Z'}).open(),false);
 assert.equal(question({choices:' ,  ,'}).open(),false);assert.equal(question({line:''}).open(),false);
 const x=await shellCase();x.client.features=[];x.ctl.open(x.client,x.feed,x.row,()=>true);await settleRecovery(x.ctl);
 assert.equal(x.ctl.question,undefined);assert.equal(x.ctl.canAct('retry'),true);
 x.client.features=['todo_start_answer'];x.task.error_code='other';x.ctl.open(x.client,x.feed,x.row,()=>true);await settleRecovery(x.ctl);
 assert.equal(x.ctl.question,undefined);
});

test('an ordinary failed task without a plan ID resolves only the unique non-dismissed plan on its own conversation and reloads the original todo',async()=>{
 const v=await shellCase(true), original=v.client.repo().call;let changed=0,ambiguous=false;
 v.todo.plan_id=''; const copy={...v.plan,id:'similar'};
 const other={...v.plan,id:'wrong-conversation',conversation:'elsewhere'},gone={...v.plan,id:'gone',state:'dismissed'};
 v.client.repo().call=async function(path,...args){
  if (path.includes('/feed?')) {assert.equal(path,'/api/v1/assistant/conversations/main/feed?manager=owner');return {plans:{plan:v.plan,similar:ambiguous?copy:{},'wrong-conversation':other,gone}};}
  return original(path,...args);
 };
 const open=()=>v.ctl.openTodo(v.client,()=>v.todo,()=>v.ctx.active,()=>v.ctx.role,async()=>{changed++;});
 open();await settleRecovery(v.ctl);assert.ok(v.ctl.question);
 assert.equal(await v.ctl.act('answer','n',v.ctl.question.attempt),true);assert.equal(changed,1);
 ambiguous=true;open();await settleRecovery(v.ctl);assert.equal(v.ctl.question,undefined);assert.equal(v.ctl.canAct('answer'),false);
 assert.equal(v.calls.filter(c=>c.method==='POST').length,1);
});

test('a second confirmed task startup question replaces the first offer without reusing the first button',async()=>{
 const v=await shellCase();const first=v.ctl.question.attempt;assert.equal(await v.ctl.act('answer','n',first),true);
 v.ctl.observeNextAttempt(); assert.equal(v.ctl.state,'starting');assert.equal(v.ctl.question,undefined);
 v.task.start.attempt=2;v.task.error_args={...offer,line:'Retry the environment setup?'};
 v.ctl.observeNextAttempt();await settleRecovery(v.ctl);assert.equal(v.ctl.state,'ready');
 const second=v.ctl.question.attempt;assert.notEqual(second,first);
 assert.equal(await v.ctl.act('answer','n',first),false);assert.equal(v.calls.filter(c=>c.method==='POST').length,1);
 assert.equal(await v.ctl.act('answer','q',second),true);assert.equal(v.calls.filter(c=>c.method==='POST').length,2);
});

test('failed tasks enter the same native task detail from the board and the ordinary conversation, while ordinary editing remains available',()=>{
 const source=readFileSync(new URL('../entry/src/main/ets/components/AssistantCards.ets',import.meta.url),'utf8');
 const registered=(key)=>source.split('  private '+key+'(')[1].split('\n  }')[0];
 assert.match(registered('openBoardRow'),/openFailedTask\(row,true\)/);
 assert.match(registered('activateTray'),/openFailedTask\(actual.resource,false\)/);
 assert.match(registered('activateTray'),/onOpenTodo\(actual.resource\)/);
 const full=readFileSync(new URL('../entry/src/main/ets/components/TaskStartFailureSheet.ets',import.meta.url),'utf8');
 assert.match(full,/TaskStartupQuestionCard\(/);assert.match(full,/this\.onEdit\(/);
 const close=full.slice(full.indexOf("PhoneOutlineIcon({name:'navigationClose'"),full.indexOf(".id('task.failure.close')"));
 assert.match(close,/iconScale:12\/17[\s\S]*\.width\(12\)\.height\(12\)\s*\.scale\(\{x:ReadinessStyle\.metric\(12,17\)\/12,y:ReadinessStyle\.metric\(12,17\)\/12\}\)/);
 assert.match(close,/width\(ReadinessStyle\.metric\(56,66\)\)\.height\(ReadinessStyle\.metric\(46,56\)\)[\s\S]*this\.onClose\(\)/);
 const form=readFileSync(new URL('../entry/src/main/ets/components/PhoneTodoDetailForm.ets',import.meta.url),'utf8');
 assert.match(form,/PhoneTodoStartSection\(\{workspace:this\.workspace/);
});

test('the two-line recovery headline uses the native first break and preserves the full task name', () => {
 const source=readFileSync(new URL('../entry/src/main/ets/components/TaskStartFailureSheet.ets',import.meta.url),'utf8');
 const method=source.slice(source.indexOf('  @Computed get displayedHeading'),source.indexOf('  private remove')).replace('@Computed ','');
 const measured={lines:2,second:8};
 class Paragraph {
   pushStyle(){} addText(){} popStyle(){}
   build(){return {layoutSync:w=>{measured.width=w;},getLineCount:()=>measured.lines,getLineMetrics:()=>({startIndex:measured.second})};}
 }
 const Face={ParagraphBuilder:Paragraph,FontWeight:{W600:600},BreakStrategy:{BALANCED:2},WordBreak:{BREAK_WORD:2},FontCollection:{getGlobalInstance:()=>({})}};
 const Class=new Function('text','ReadinessStyle','Tokens','return '+stripTypeScriptTypes('class Header {'+method+'}'))(Face,{font:()=> '17vp'},{body:17});
 const row=new Class();row.headingWidth=202;row.fullHeading='Prepare the demo workspace';row.getUIContext=()=>({vp2px:v=>v*3});
 assert.equal(row.displayedHeading,row.fullHeading);assert.equal(measured.width,606);
 row.headingWidth=177;measured.lines=3;assert.equal(row.displayedHeading,'Prepare\nthe demo workspace');
 row.fullHeading='修复👩‍💻 编译失败后再试';measured.second=row.fullHeading.indexOf('编译');
 assert.equal(row.displayedHeading,'修复👩‍💻\n编译失败后再试');
 row.fullHeading='Save\n\nthis title';assert.equal(row.displayedHeading,row.fullHeading,'a real hard break stays intact');
 assert.match(source,/wordBreak\(this\.displayedHeading===this\.fullHeading \? WordBreak\.BREAK_WORD : WordBreak\.BREAK_ALL\)/);
 assert.match(source,/accessibilityText\(this\.fullHeading\)/);
 const question=readFileSync(new URL('../entry/src/main/ets/components/TaskStartupQuestionCard.ets',import.meta.url),'utf8');
 assert.match(question,/@Param secondaryText:ResourceColor=Colors\.secondary/);
 assert.match(source,/question:this\.recovery\.question!,secondaryText:this\.sheetSecondary/);
});
