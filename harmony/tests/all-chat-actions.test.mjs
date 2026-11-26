import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(spec,ctx,next) {
    if(spec==='./Wire'&&ctx.parentURL?.endsWith('AllChatActions.ets'))return{url:new URL('./Wire.ets',ctx.parentURL).href,shortCircuit:true};
    return next(spec,ctx);
  },
  load(url,ctx,next) {
    if(!url.endsWith('.ets'))return next(url,ctx);
    return {format:'module',source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*|@Trace\s+/g,'')),shortCircuit:true};
  }
});
const {AllChatActions,ChatActionTarget}=await import('../entry/src/main/ets/models/AllChatActions.ets');
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
function conversation(id,machine='studio',extra={}) {
  const r=ResourceRow.from({session_id:id,machine_id:machine,title:id,...extra},'conversation');r.group='group';r.manager='assistant';return r;
}
const tick=()=>new Promise(done=>setImmediate(done));
async function setup(change,features=['direction_sessions']) {
  const rows=['main','background','inbox-item','lead'].map(id=>conversation(id));let gen=1;
  const list=[{id:'lead',name:'Design'},{id:'main',name:'Main',secretary:true}];const calls=[];
  const client={connected:true,demo:false,features,profile:{id:'group',manager:'assistant'},mainId:'main',mainMachineId:'studio',
    sidebarConfig:JSON.stringify({background_supervisor:'background',leaders:['lead']}),conversations:rows,sessions:[],todos:[],
    scope:()=> 'group/assistant',connectionGeneration:()=>gen,owns:(n,s)=>n===gen&&s==='group/assistant',
    operationBlocked:()=>false,refresh:async()=>{},repo:()=>({call:async(path,method='GET',body={},token,admit)=>{
      if(method==='GET')return{directions:list};
      if(admit&&!admit())throw new Error('request_updated');
      calls.push({path,method,body});return change?change(path,body):{ok:true};
    }})};
  const model=new AllChatActions();model.bind(client);await tick();
  return {model,client,rows,calls,target:(i=2)=>new ChatActionTarget(client,rows[i]),retire:()=>{gen++;model.bind(client)}};
}
test('scope, built-in supervisors, leaders and supervised ancestry decide actual row menus',async()=>{
  const {client,model,rows,target}=await setup();
  assert.deepEqual(model.choices(target()).map(d=>d.name),['Design']);
  assert.equal(model.canChangeChat(target(0)),false);assert.equal(model.canChangeChat(target(1)),false);
  assert.equal(model.choices(target(3)).length,0);
  rows[2].group='foreign';assert.equal(model.canChangeChat(target()),false);rows[2].group='group';
  rows[2].manager='other';assert.equal(model.choices(target()).length,0);rows[2].manager='assistant';
  client.todos=[conversation('todo','studio',{supervision:{supervisor:'lead'},agents:[{machine_id:'studio',session_id:'parent'}]})];
  client.sessions=[conversation('inbox-item','studio',{parent_session_id:'parent'})];assert.equal(model.choices(target()).length,0);
  client.todos=[];client.conversations.push(conversation('inbox-item','other'));assert.equal(model.canChangeChat(target()),false);assert.equal(model.choices(target()).length,1);
  const older=await setup(undefined,[]);assert.equal(older.model.choices(older.target()).length,0);assert.equal(older.model.canChangeChat(older.target()),true);
});
test('archiving hides only its scoped row and restores it on rejection; stale confirmations never dispatch',async()=>{
  const gate=Promise.withResolvers();const s=await setup(()=>gate.promise);const t=s.target();
  const attempt=s.model.archive(t);await s.model.archive(t);assert.deepEqual(s.model.hidden,[t.key]);assert.equal(s.calls.length,1);
  gate.reject(Object.assign(new Error('forbidden'),{code:'forbidden',status:403}));await attempt;
  assert.deepEqual(s.model.hidden,[]);assert.equal(s.client.conversations.length,4);assert.equal(s.model.notice,'permission_denied');
  s.retire();await s.model.pin(t,true);assert.equal(s.calls.length,1);
  const ok=await setup();await ok.model.archive(ok.target());assert.equal(ok.client.conversations.length,3);
  assert.equal(ok.calls[0].path,'/api/v1/assistant/conversations/inbox-item/archive');
});
test('different targets are never confused by pin and file, same-target changes serialize, and undo sends the previous null',async()=>{
  const s=await setup((path,body)=>path.endsWith('/direction')?{changed:true,direction:body.direction,previous:null}:{ok:true});
  const t=s.target();await s.model.pin(t,true);await s.model.pin(t,true);
  assert.equal(s.model.pinned(t),true);assert.equal(s.model.choices(t).length,0);await s.model.pin(t,false);
  const design=s.model.choices(t)[0];await s.model.file(t,design);assert.equal(s.model.notice,'phone_chat_moved');
  await s.model.undoMove();assert.deepEqual(s.calls.map(c=>[c.method,c.path,c.body]),[
    ['POST','/api/v1/assistant/directions/inbox-item/make',{}],['POST','/api/v1/assistant/directions/inbox-item/end',{}],
    ['PUT','/api/v1/assistant/sessions/studio/inbox-item/direction',{direction:'lead'}],
    ['PUT','/api/v1/assistant/sessions/studio/inbox-item/direction',{direction:null}]]);
  await s.model.file(t,undefined);assert.deepEqual(s.calls.at(-1).body,{direction:''});
});
test('a missing manager route hides only filing; ordinary not-found refusals and uncertain responses remain visible',async()=>{
  let fail={code:'direction_not_found',status:404};const s=await setup(()=>{throw Object.assign(new Error(fail.code),fail)});
  await s.model.file(s.target(),s.model.directions[0]);assert.equal(s.model.choices(s.target()).length,1);
  fail={code:'direction_full',status:409,userMessage:'The selected direction is full.'};await s.model.file(s.target(),s.model.directions[0]);
  assert.equal(s.model.notice,'from_hub');assert.equal(s.model.detail,'The selected direction is full.');
  fail={code:'route_not_found',status:404};await s.model.file(s.target(),s.model.directions[0]);assert.equal(s.model.notice,'phone_chat_direction_unavailable');assert.equal(s.model.choices(s.target()).length,0);
  assert.equal(s.model.canChangeChat(s.target()),true);
  fail={code:'result_unknown',status:502,resultUnknown:true};await s.model.pin(s.target(),true);assert.equal(s.model.notice,'result_unknown');assert.equal(s.model.pinned(s.target()),false);
});
