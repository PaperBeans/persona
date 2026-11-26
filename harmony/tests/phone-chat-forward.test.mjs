import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import {existsSync,readFileSync} from 'node:fs';
import {randomUUID} from 'node:crypto';

const platform = new Map([
 ['@kit.ArkTS','export const util={generateRandomUUID:()=>crypto.randomUUID()}'],
 ['./ChatAttachments','export class ChatAttachments {static async forwardImage(){return {mime:"image/png",data:"AAEC"}}}'],
 ['./MessageImage','export async function loadMessageImage(model,id,path){return model.readPhoto(id,path)}'],
 ['./HubRepository','export const segment=encodeURIComponent'],
 ['../design/Strings','export function tr(k){return ({chat_forward_me:"Me",chat_forward_from:"Forwarded from {1}",chat_forward_cut:"CUT",chat_forward_file:"File {1}",chat_forward_computer_file:"File on {1}: {2}"})[k]||k}']
]);
registerHooks({resolve(spec,ctx,next){
 if(ctx.parentURL?.endsWith('.ets')){
  if(platform.has(spec))return {url:'forward-test:'+spec,shortCircuit:true};
  if(spec.startsWith('.')){const path=new URL(spec,ctx.parentURL);return {url:path.href+(existsSync(new URL(path.href+'.ts'))?'.ts':'.ets'),shortCircuit:true};}
 }
 return next(spec,ctx);
},load(url,ctx,next){
 if(url.startsWith('forward-test:'))return {format:'module',source:platform.get(url.slice(13)),shortCircuit:true};
 if(url.endsWith('.ets'))return {format:'module',source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,''),{mode:'transform'}),shortCircuit:true};
 return next(url,ctx);
}});
const {PhoneChatForward}=await import('../entry/src/main/ets/models/PhoneChatForward.ets');
const {ChatMessage,ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const {ChatDraftStore}=await import('../entry/src/main/ets/models/ChatDraftStore.ets');
const {forwardOrigin,forwardedBody}=await import('../entry/src/main/ets/core/ChatForwardWire.ts');
const {parseMessageContent}=await import('../entry/src/main/ets/core/MessageContent.ts');

function row(id,machine,kind,title=id,fields={}){
 const r=ResourceRow.from({session_id:id,machine_id:machine,title,space_name:'weather',slot_name:'tests',agent:'claude',...fields},kind);
 r.group='hub-A';r.manager='manager';return r;
}
function fixture(){
 const prefs=new Map(),reads=[],writes=[],received=[];
 const source=row('source','mac','session','Network review'),main=row('main','assistant','conversation','Main'),first=row('blog','other','session','Blog'),foreign=row('foreign','mac','session');foreign.group='hub-B';
 const m=row('mac','mac','machine','MacBook',{online:true}),other=row('other','other','machine','Studio',{online:true});
 const items=[['q1','user','First?'],['q2','user','Second?\n<attached_images>\n/photos/user.png\n</attached_images>'],['a2','assistant','cut'],['q3','user','Third?'],['a3','assistant','Third response']];
 const raw=items.map(([id,role,text],seq)=>({id,seq,kind:'text',role,text,ts:'2026-11-11T10:24:00+08:00',...(id==='a2'?{full_size:130}: {})}));
 const target=()=>JSON.stringify(['hub-A',source.machine,source.id]);
 const messages=raw.map(i=>{const v=new ChatMessage(i.role==='assistant'?'visible-turn-'+i.id:i.id,i.role,parseMessageContent(i.text).text);v.itemId=i.id;v.ownerTarget=target();return v;});
 const model={profile:{id:'hub-A',hubId:'uuid-A',manager:'manager'},connected:true,epoch:1,messages,raw,sessions:[source,first,foreign],conversations:[main],machines:[m,other],selected:source,mainId:'main',mainMachineId:'assistant',features:['persona_contracts_v1'],
  connectionGeneration(){return this.epoch;},scope(){return 'scope-A';},owns(gen,scope){return this.connected&&gen===this.epoch&&scope==='scope-A';},target,
  transcriptItems(){return this.raw.slice();},forwardDirectory(){return this;},
  canForwardTo(r){return r.group===this.profile.id && r!==this.selected;},
  async loadView(k){return prefs.get(k)||'';},async saveView(k,v){prefs.set(k,v);},
  repo(){return {call:async path=>{reads.push(path);return {text:'Complete original full text'};}};},
  async readPhoto(id,path){reads.push([id,path]);return {data:new Uint8Array([0,1,2]).buffer};},
  async receiveForward(target,refs,images,transfer,admit){assert.ok(admit());received.push({target,refs,images,transfer});},
 };
 return {model,source,main,first,foreign,raw,messages,reads,writes,received,prefs};
}
test('a single forward fetches its original item id and full body only after choosing an eligible same-Hub recipient',async()=>{
 const {model,messages,first,foreign,reads,received}=fixture(),state=new PhoneChatForward();
 state.startForward(model,messages[2]);assert.ok(state.isChoosing(model));assert.deepEqual(reads,[]);
 assert.equal(state.previews(model)[0].text,'cut');
 assert.equal(state.sections(model).flatMap(s=>s.rows).some(r=>r===foreign),false);
 assert.equal(await state.choose(model,foreign),false);assert.deepEqual(reads,[]);
 assert.equal(await state.choose(model,first),true);assert.equal(received.length,1);
 const ref=received[0].refs[0];assert.equal(ref.text,'Complete original full text');assert.equal(ref.msg,'a2');
 assert.deepEqual(forwardOrigin(ref),{machine:'mac',session:'source',item:'a2'});
 assert.deepEqual([ref.author,ref.role,ref.chat,ref.project,ref.machine],['Claude','agent','Network review','weather › tests','MacBook']);
 assert.deepEqual(reads,['/api/v1/sessions/mac/source/items/a2/full']);assert.equal(state.isChoosing(model),false);
 assert.equal(JSON.parse(await model.loadView('forwardTargets')).length,1);
});
test('explicit selections retain chronology, attached questions are optional, and closing the picker never changes the draft',async()=>{
 const {model,messages,first,reads,received}=fixture(),state=new PhoneChatForward();
 state.startPicking(model,messages[4]);state.toggle(model,messages[2]);
 assert.equal(state.picked.length,2);assert.deepEqual(state.selected(model).map(m=>m.itemId),['q1','q2','a2','q3','a3']);
 assert.ok(state.isBrought(model,messages[1]));state.setInclude(model,false);
 assert.deepEqual(state.selected(model).map(m=>m.itemId),['a2','a3']);state.forwardPicks(model);state.cancelPicker();
 assert.deepEqual(state.picked,['a3','a2']);assert.ok(state.isPicking(model));assert.equal(state.include,false);assert.deepEqual(received,[]);assert.deepEqual(reads,[]);
 state.setInclude(model,true);state.forwardPicks(model);assert.equal(await state.choose(model,first),true);
 assert.deepEqual(received[0].refs.map(r=>r.msg),['q1','q2','a2','q3','a3']);assert.deepEqual(received[0].images.map(f=>[f.item,f.mime,f.data]),[['q2','image/png','AAEC']]);
 assert.equal(received[0].refs[1].text,'Second?');assert.equal(received[0].refs[1].role,'user');assert.equal(received[0].refs[1].author,'Me');
});
test('a cancellation or owner switch during a full-body read cannot write, navigate, or repeat a delivery',async()=>{
 for(const action of ['cancel','retire']){
  const {model,messages,first,received}=fixture(),state=new PhoneChatForward();let resolve;
  model.repo=()=>({call:()=>new Promise(done=>resolve=done)});
  state.startForward(model,messages[2]);const waiting=state.choose(model,first);
  assert.equal(await state.choose(model,first),false);
  if(action==='cancel')state.cancelPicker();else model.epoch++;
  resolve({text:'late'});assert.equal(await waiting,false);assert.deepEqual(received,[]);
 }
});
test('a merged draft atomically preserves typed text, existing context and private photo bytes through restore, retry, and acknowledgement',async()=>{
 const values=new Map(),local={revision:()=>0,accepts:()=>true,get:async k=>values.get(k)||'',put:async(k,v)=>values.set(k,v),remove:async k=>values.delete(k),
  updateRecords:async(k,e,changes,remove,valid)=>{if(!valid())throw Error('request_updated');for(const [key,v] of changes)values.set(key,v);for(const key of remove)values.delete(key);}};
 const store=new ChatDraftStore(local),target='same hub / existing session',foreign='other hub / same session';
 const raw=JSON.stringify({kind:'quote',title:'Original',text:'Keep this'});
 await store.save(target,{text:'Note already typed',context:raw,revision:'old'});
 const transfer=randomUUID(),photo={id:randomUUID(),mime:'image/png',data:'ab'.repeat(100000),item:'i'},ref={kind:'forward',url:'persona://session/m/s?at=i',msg:'i',title:'From',text:'Original'};
 const first=await store.appendForwards(target,[ref],[photo],transfer,()=>true);assert.equal(first.text,'Note already typed');
 assert.deepEqual(JSON.parse(first.context).contexts.map(r=>r.kind),['quote','forward']);assert.equal(JSON.parse(first.context).contexts[1].local_images[0].chunks,2);
 const retry=await store.appendForwards(target,[ref],[photo],transfer,()=>true);assert.equal(first.context,retry.context);
 const restored=await new ChatDraftStore(local).read(target);
 assert.deepEqual(await store.forwardedImages(target,restored.context,4,()=>true),[{mime:'image/png',data:photo.data}]);
 assert.equal((await store.read(foreign)).context,'');assert.equal(await store.acknowledge(target,'older'),false);
 assert.ok([...values.keys()].some(k=>k.startsWith('draftmedia:')));assert.equal(await store.acknowledge(target,transfer),true);
 assert.equal([...values.keys()].some(k=>k.startsWith('draftmedia:')),false);
 await assert.rejects(store.appendForwards(target,[ref],[],randomUUID(),()=>false),/request_updated/);
});
test('only typed file blocks become named references, nested source markers disappear and graphemes keep the wire limit',()=>{
 const body=s=>forwardedBody(s,'Mac',32,'CUT',(n,c)=>'File on '+c+': '+n,parseMessageContent);
 assert.equal(body('See /tmp/example/notes.txt'), 'See /tmp/example/notes.txt');
 assert.equal(body('<persona-context kind="message-source">{"author":"user"}</persona-context>hello\n<persona-file kind="file" path="/tmp/own.txt" name="my.txt" size="4"/>'),'hello\n\nFile on Mac: my.txt');
 assert.equal(body('<persona-context kind="quote" title="Ann">words</persona-context>Body'),'> Ann\n> words\n\nBody');
 assert.equal(body('👩‍💻'.repeat(33)),'👩‍💻'.repeat(32)+'\n\nCUT');
 assert.equal(forwardOrigin({url:'https://somewhere/session/a/b?at=c'}),undefined);
});

test('named and numbered Hub tabs survive in source attribution and an absent tab stays absent',()=>{
 const {model,messages}=fixture(),state=new PhoneChatForward();
 for(const [props,expected] of [[{slot_name:'review',slot_index:2},'weather › review'],[{slot_name:'2',slot_index:2},'weather › #2'],[{slot_index:3},'weather › #3'],[{},'weather']]){
  model.selected.raw=JSON.stringify({space_name:'weather',...props});state.startForward(model,messages[2]);
  assert.equal(state.previews(model)[0].project,expected);
 }
});
