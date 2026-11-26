import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';

registerHooks({
  resolve(specifier,context,next){
    if(context.parentURL?.endsWith('.ets') && specifier.startsWith('.')) return {url:new URL(specifier+'.ets',context.parentURL).href,shortCircuit:true};
    return next(specifier,context);
  },
  load(url,context,next){
    if(!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const {PhoneKnowledgeSelectionModel,validSelection,selectionNumber,MESSAGE_RULES,DOC_RULES}=await import('../entry/src/main/ets/models/PhoneKnowledgeSelection.ets');

const clone=structuredClone;
function defaults(){
  const group=(names)=>({order:[[names[0],names[1]],...names.slice(2).map(k=>[k])],
    rules:Object.fromEntries(names.map(k=>[k,{on:true,days:null}]))});
  const value={version:1,messages:group(MESSAGE_RULES),docs:group(DOC_RULES),items:[],history:{chat:{back_days:23}}};
  value.messages.rules.mine.context=50;
  return value;
}
function fixture(){
  const state={document:defaults(),calls:[],manager:'m',generation:1,blocked:false,topic:true};
  const repo={async call(path,method='GET',body={},_op='',admit,_,ack){
    if(admit && !admit()) throw Error('scope_changed');
    const url=new URL(path,'https://example.test');
    assert.equal(url.searchParams.get('manager'),state.manager);
    const event={path:url.pathname,method,body:clone(body)};
    state.calls.push(event);
    const custom=await state.handle?.(event);
    if(custom!==undefined)return custom;
    if(url.pathname==='/api/v1/assistant'){ if(method==='PUT') state.topic=body.kb_topics;return {kb_topics:state.topic}; }
    if(url.pathname==='/api/v1/kb/selection'){
      if(method==='PUT'){
        assert.equal(ack(body),true);
        state.document=clone(body);
      }
      return clone(state.document);
    }
    throw Error('Unexpected request '+url.pathname);
  }};
  const client={connected:true,profile:{manager:'m'},scope:()=>state.manager,connectionGeneration:()=>state.generation,
    owns:(generation,scope)=>generation===state.generation&&scope===state.manager,repo:()=>repo,observeEvents:()=>()=>{},
    operationBlocked:()=>state.blocked};
  return {state,client,form:new PhoneKnowledgeSelectionModel()};
}
test('selection validates version, nullable fields, level membership, modes and retention bounds before editing',()=>{
  assert.equal(validSelection(defaults()),true);
  for(const edit of [d=>d.version++,d=>d.future=null,d=>d.messages.rules.mine.future=null,
    d=>d.messages.order[0].push('dm'),d=>d.docs.rules.mentions.attachment_days=3,d=>d.messages.rules.mine.context=201,
    d=>d.docs.rules.viewed.days=3.5,d=>d.items=[{source:'a',kind:'chat',id:'x',read:false,store:true}],
    d=>d.history.chat.back_days=0]){
    const data=defaults();edit(data);assert.equal(validSelection(data),false);
  }
  for(const [raw,result] of [[' ',-1],['-1',-2],['2.5',-2],['３',-2],['36501',-2],['1',1],[' 36500 ',36500]])
    assert.equal(selectionNumber(raw,1,36500),result);
  assert.equal(selectionNumber('0',0,200),0);
});
test('a rule save rebases only the changed fields and never overwrites a same-field concurrent edit',async()=>{
  const {state,client,form}=fixture();await form.open(client);
  const original=clone(form.rule('messages','mine'));
  state.document.messages.rules.mine.on=false;
  state.document.history.chat.back_days=31;
  assert.equal(await form.saveRule('messages','mine',original,{...original,context:12}),true);
  assert.deepEqual(state.document.messages.rules.mine,{on:false,days:null,context:12});
  assert.equal(state.document.history.chat.back_days,31);
  const before=clone(form.rule('messages','mine'));
  state.document.messages.rules.mine.context=55;
  assert.equal(await form.saveRule('messages','mine',before,{...before,context:15}),false);
  assert.equal(form.problem,'ks_changed');
  assert.equal(form.rule('messages','mine').context,55);
  assert.equal(state.calls.filter(r=>r.path.endsWith('/selection')&&r.method==='PUT').length,1);
  state.document.extra=null;
  assert.equal(await form.saveRule('messages','mine',clone(form.rule('messages','mine')),{...form.rule('messages','mine'),context:56}),false);
  assert.equal(form.problem,'ks_future');
  assert.equal(state.document.extra,null);
});
test('rules reorder by stable name and per-item overrides use the source identity from real entry details',async()=>{
  const {state,client,form}=fixture();await form.open(client);
  assert.equal(await form.reorder('messages','mine','split'),true);
  assert.deepEqual(form.levels('messages').slice(0,2),[['mentions'],['mine']]);
  assert.equal(await form.reorder('messages','mine','join'),true);
  assert.deepEqual(form.levels('messages')[0],['mentions','mine']);
  assert.equal(await form.reorder('messages','dm','up'),true);
  assert.deepEqual(form.levels('messages')[0],['dm']);
  state.handle=({path})=>path==='/api/v1/kb/chats'?{items:[{id:'view id',name:'Conversation'}]}:
    path==='/api/v1/kb/chats/view%20id'?{detail:{meta:{source:'connector',source_id:'stable-id',kind:'group'}}}:undefined;
  await form.listEntries('chat');
  const item=await form.pick('chat',form.entries[0]);
  assert.deepEqual(item,{source:'connector',kind:'chat',id:'stable-id',read:true,store:false,name:'Conversation'});
  assert.equal(await form.saveItem(undefined,{...item,store:true,days:30}),true);
  state.document.items[0].name='Renamed elsewhere';
  const before=clone(form.items()[0]);
  assert.equal(await form.saveItem(before,{...before,days:60}),true);
  assert.equal(form.items()[0].name,'Renamed elsewhere');
  const removed=clone(form.items()[0]);
  assert.equal(await form.removeItem(removed),true);
  assert.equal(await form.saveItem(undefined,removed),true);
  assert.deepEqual(form.items(),[removed]);
  state.handle=({path})=>path==='/api/v1/kb/chats/view%20id'?{detail:{meta:{source:'mail',source_id:'stable',kind:'email'}}}:undefined;
  assert.equal(await form.pick('chat',form.entries[0]),undefined);
  assert.equal(form.pickerError,'ks_not_selectable');
});
test('an unknown saved write or a superseded settings owner never dispatches another PUT or redraws a closed page',async()=>{
  const {state,client,form}=fixture();await form.open(client);
  state.blocked=true;
  let before=clone(form.rule('docs','viewed'));
  assert.equal(await form.saveRule('docs','viewed',before,{...before,on:false}),false);
  assert.equal(state.calls.filter(call=>call.method==='PUT').length,0);
  state.blocked=false;
  const gate=Promise.withResolvers(),arrived=Promise.withResolvers();
  state.handle=async ({method})=>{ if(method==='PUT'){arrived.resolve();return gate.promise;} };
  const pending=form.saveRule('docs','viewed',before,{...before,on:false});
  await arrived.promise;
  form.close();gate.resolve(defaults());
  assert.equal(await pending,false);
  assert.deepEqual(form.data,{});
});

test('map prefetch stays within the current manager, reads at most six details and retains partial lists',async()=>{
  const {state,client,form}=fixture();
  const list=Array.from({length:11},(_,i)=>({id:'entry-'+i,name:'Entry '+i}));
  let active=0,peak=0;
  state.handle=async({path,method})=>{
    assert.equal(method,'GET');
    if(path==='/api/v1/kb/topics')return {items:list};
    if(path.startsWith('/api/v1/kb/topics/')){
      peak=Math.max(peak,++active);
      await new Promise(resolve=>setImmediate(resolve));
      active--;
      if(path.endsWith('/entry-4'))throw Error('404');
      if(path.endsWith('/entry-8'))return {};
      return {detail:{points:[]}};
    }
  };
  await form.open(client,true);
  const read=await form.readOutline('topics');
  assert.equal(peak,6);assert.deepEqual(read.items,list);
  assert.equal(read.failed,2);assert.equal(read.details.size,9);assert.equal(read.curating,true);
});

test('a map response from a retired owner cannot supply another assistant’s outline',async()=>{
  const {state,client,form}=fixture();let release,entered;
  const waiting=new Promise(resolve=>{entered=resolve;});
  state.handle=async({path})=>{
    if(path==='/api/v1/kb/projects')return {items:[{id:'secret',name:'First owner'}]};
    if(path==='/api/v1/kb/projects/secret'){entered();return new Promise(resolve=>{release=resolve;});}
  };
  await form.open(client,true);
  const pending=form.readOutline('projects');await waiting;
  state.manager='new-owner';state.generation++;form.close();
  release({detail:{summary:'old-scoped-data'}});
  assert.equal(await pending,undefined);
});
