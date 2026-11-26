import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';

registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.')) {
      return {url:new URL(specifier+'.ets',context.parentURL).href,shortCircuit:true};
    }
    return next(specifier,context);
  },
  load(url, context, next) {
    if (!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const {AssistantFeedModel}=await import('../entry/src/main/ets/models/AssistantFeed.ets');
const {briefTake}=await import('../entry/src/main/ets/models/BriefRowActions.ets');
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const round='2026-09-02T06:13:40Z';
const visible=()=>true;
function resource(id,kind,group,manager,data={}) {
  const r=new ResourceRow(id,id,kind);r.group=group;r.manager=manager;r.raw=JSON.stringify(data);return r;
}
async function scenario(response,opts={}) {
  const group='private-hub',manager=opts.manager??'work';
  const session=resource('assistant-chat','conversation',group,manager);
  const row={id:'todo/a',title:'Synthetic task',asked_at:round,...opts.row};
  const later={id:'question/b',title:'Synthetic question'};
  const item={id:'brief-1',kind:'brief',conversation:session.id,manager,body:{accept:[row],later:[later]}};
  const calls=[];const blocked=new Set();const control={generation:7};
  const repo={async call(path,method='GET',body={},op='',admit,done,ack,emptyBody){
    if (path.startsWith('/api/v1/todos?'))return {todos:[]};
    if (admit && !admit())throw Object.assign(new Error('request_updated'),{code:'request_updated'});
    calls.push({path,method,body,emptyBody,admit});
    const result=await response({path,method,body,admit});
    if(ack && !ack(result))throw Object.assign(new Error('result_unknown'),{resultUnknown:true});
    return result;
  }};
  const client={profile:{id:group,manager},selected:session,todos:[],connected:true,repo:()=>repo,
    scope(){return this.profile.id+'/'+this.profile.manager;},connectionGeneration:()=>control.generation,
    owns(g,s){return g===control.generation && this.scope()===s;},target(r=this.selected){return this.scope()+'/'+r.id;},
    observeEvents:()=>()=>{},operationBlocked:path=>blocked.has(path)};
  const feed=new AssistantFeedModel();await feed.open(client,session,false);feed.items=[item];
  return {client,repo,control,feed,row,later,item,calls,blocked,manager,group,session,
    act:(action,admit=visible)=>feed.performBriefAction(item,action==='handled'?'later':'accept',action==='handled'?later:row,action,admit)};
}
test('only actions offered by the live briefing and its exact account and round may write',async()=>{
  assert.equal(briefTake({}),'accept');assert.equal(briefTake({outcome:'new-verdict'}),'got_it');
  assert.equal(briefTake({outcome:'decide'}),'');assert.equal(briefTake({outcome:' decide ',recommend:' Yes '}),'recommendation');
  const s=await scenario(()=>({ok:true}),{row:{outcome:'decide',recommend:'Yes'}});
  for(const changed of [{asked_at:'old'},{outcome:'done'},{recommend:'No'},{id:'other'}])
    assert.equal(await s.feed.performBriefAction(s.item,'accept',{...s.row,...changed},'accept',visible),false);
  assert.equal(await s.act('accept',()=>false),false);
  s.item.manager='another';assert.equal(await s.act('accept'),false);s.item.manager=s.manager;
  s.item.body.accept[0].read_only=true;assert.equal(await s.act('accept'),false);delete s.item.body.accept[0].read_only;
  s.client.selected.raw=JSON.stringify({can_write:false});assert.equal(await s.act('handled'),false);
  assert.equal(s.calls.length,0);
  const missing=await scenario(()=>({ok:true}),{row:{asked_at:'',outcome:'decide'}});
  assert.equal(await missing.act('accept'),false);assert.equal(await missing.act('complete'),false);
});
test('a decision and a repeated copy of its round share one in-flight and one resulting action',async()=>{
  let release;const gate=new Promise(r=>{release=r});
  const s=await scenario(async()=>{await gate;return {ok:true}},{row:{outcome:'decide',recommend:'Yes'}});
  const copy={...s.item,id:'brief-2'};s.feed.items.push(copy);
  const task=s.act('accept');assert.equal(s.feed.briefOutcome('accept',s.row),'busy');
  assert.equal(await s.feed.performBriefAction(copy,'accept',s.row,'accept',visible),false);
  release();assert.equal(await task,true);
  assert.equal(s.calls.length,1);assert.deepEqual(s.calls[0].body,{asked_at:round});assert.equal(s.calls[0].path,'/api/v1/todos/todo%2Fa/accept?manager=work');
  assert.equal(s.feed.briefOutcome('accept',s.row),'recommended');
  const reopened=new AssistantFeedModel();await reopened.open(s.client,s.session,false);reopened.items=[copy];
  assert.equal(reopened.briefOutcome('accept',s.row),'recommended');assert.equal(await reopened.performBriefAction(copy,'accept',s.row,'complete',visible),false);
  assert.equal(reopened.briefOutcome('accept',{...s.row,asked_at:'next-round'}),'');
});
test('handled names the briefing manager, has no request body, and never sends a chat message',async()=>{
  const s=await scenario(()=>({ok:true}),{manager:'team / α'});assert.equal(await s.act('handled'),true);
  assert.equal(s.calls[0].method,'POST');assert.equal(s.calls[0].emptyBody,true);
  assert.equal(s.calls[0].path,'/api/v1/later/question%2Fb/handled?manager=team%20%2F%20%CE%B1');
  assert.equal(s.feed.briefOutcome('later',s.later),'handled');
  const old=await scenario(()=>({ok:true}),{manager:''});assert.equal(await old.act('handled'),true);assert.equal(old.calls[0].path,'/api/v1/later/question%2Fb/handled');
});
test('direct completion verifies a current writable decision and writes only done=true to its own task',async()=>{
  const todo={id:'todo/a',manager:'work',ask:{outcome:'decide',asked_at:round}};
  const s=await scenario(({method})=>({todo:method==='GET'?todo:{...todo,done_at:round}}),{row:{outcome:'decide'}});
  assert.equal(s.feed.canBriefAction(s.item,'accept',s.row,'accept'),false);
  assert.equal(await s.act('complete'),true);assert.deepEqual(s.calls.map(c=>[c.method,c.path,c.body]),[['GET','/api/v1/todos/todo%2Fa?manager=work',{}],['PATCH','/api/v1/todos/todo%2Fa?manager=work',{done:true}]]);
  assert.equal(s.feed.briefOutcome('accept',s.row),'completed');
  for(const changed of [{id:'wrong'},{manager:'other'},{can_write:false},{done_at:round},{done:true},{ask:{outcome:'decide',asked_at:'new'}},{ask:{outcome:'done',asked_at:round}}]){
    const x=await scenario(()=>({todo:{...todo,...changed}}),{row:{outcome:'decide'}});assert.equal(await x.act('complete'),false);
    assert.equal(x.calls.length,1);assert.equal(x.calls[0].method,'GET');
  }
  const plain=await scenario(()=>({ok:true}));assert.equal(await plain.act('complete'),false);assert.equal(plain.calls.length,0);
});
test('409 and an old route replace only their own round; known refusals retry and uncertain deliveries cannot replay',async()=>{
  for(const [status,extra,outcome,error] of [[409,{},'already',''],[404,{},'unsupported',''],[403,{},'','permission_denied'],[502,{},'','brief_row_accept_failed'],[503,{},'','brief_row_accept_failed'],[423,{},'','brief_row_accept_failed'],[502,{code:'send_in_doubt'},'unknown',''],[0,{resultPending:true},'pending','']]){
    let fail=true;const s=await scenario(()=>{if(fail)throw Object.assign(new Error('fixture'),{status,...extra});return {ok:true}});
    assert.equal(await s.act('accept'),false);assert.equal(s.feed.briefOutcome('accept',s.row),outcome);assert.equal(s.feed.briefError('accept',s.row),error);
    fail=false;assert.equal(await s.act('accept'),!outcome);
  }
});
test('a changed account or route after the user taps cannot dispatch or publish to a different manager',async()=>{
  let next;const gate=new Promise(r=>{next=r});
  const s=await scenario(async({admit})=>{await gate;if(!admit())throw Object.assign(new Error('request_updated'),{code:'request_updated'});return {ok:true}});
  const send=s.act('accept');await Promise.resolve();await Promise.resolve();s.control.generation++;s.client.profile.manager='other';next();assert.equal(await send,false);
  const newSession=resource('other-chat','conversation',s.group,'other');s.client.selected=newSession;await s.feed.open(s.client,newSession,false);
  assert.equal(s.feed.briefOutcome('accept',s.row),'');assert.equal(s.feed.briefError('accept',s.row),'');
});
test('a known task on the same Hub uses its real manager repository; an unverified manager field is rejected',async()=>{
  const s=await scenario(()=>{throw Error('wrong repository')});
  s.client.todos=[resource(s.row.id,'todo',s.group,'other')];const written=[];
  const remote={async call(path,method,body,op,allow){assert.ok(allow());written.push([path,method,body]);return {ok:true}}};
  const foreign={profile:{id:s.group,manager:'other'},connected:true,connectionGeneration:()=>1,scope:()=>s.group+'/other',owns:(e,k)=>e===1 && k===s.group+'/other',repo:()=>remote,operationBlocked:()=>false};
  s.client.viewFor=async row=>{assert.equal(row.manager,'other');return foreign};
  assert.equal(await s.act('accept'),true);assert.equal(written[0][0],'/api/v1/todos/todo%2Fa/accept?manager=other');
  const hostile=await scenario(()=>({ok:true}),{row:{manager:'foreign'}});assert.equal(await hostile.act('accept'),false);assert.equal(hostile.calls.length,0);
});

test('a typed backend refusal exposes its public reason and permits a deliberate retry',async()=>{
  let allowed=false;
  const s=await scenario(()=>{if(!allowed)throw Object.assign(new Error('dialog_open'),{status:423,code:'dialog_open',userMessage:'Close the fixture approval dialog first.'});return {ok:true}});
  assert.equal(await s.act('accept'),false);
  assert.equal(s.feed.briefFailureDetail('accept',s.row),'Close the fixture approval dialog first.');
  allowed=true;assert.equal(await s.act('accept'),true);assert.equal(s.feed.briefFailureDetail('accept',s.row),'');
});

test('a receipt belongs to the task owner even when two managers use the same id and round',async()=>{
  const s=await scenario(()=>({ok:true}));const first=resource(s.row.id,'todo',s.group,'first'),second=resource(s.row.id,'todo',s.group,'second');
  const written=[];
  s.client.viewFor=async target=>({profile:{id:s.group,manager:target.manager},connected:true,connectionGeneration:()=>1,
    scope:()=>s.group+'/'+target.manager,owns:()=>true,repo:()=>repos[target.manager],operationBlocked:()=>false});
  const repoFor=manager=>({async call(path){written.push([manager,path]);return {ok:true}}});
  const repos={first:repoFor('first'),second:repoFor('second')};
  s.client.todos=[first];assert.equal(await s.act('accept'),true);
  s.client.todos=[first,second];assert.equal(await s.act('accept'),false);
  s.client.todos=[second];assert.equal(s.feed.briefOutcome('accept',s.row),'');assert.equal(await s.act('accept'),true);
  s.client.todos=[first];assert.equal(s.feed.briefOutcome('accept',s.row),'accepted');assert.equal(await s.act('accept'),false);
  assert.deepEqual(written.map(x=>x[0]),['first','second']);
});
