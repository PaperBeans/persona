import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
registerHooks({
  resolve(name,context,next){
    if(context.parentURL?.endsWith('.ets') && ['./ClientModel','../platform/HubTransport'].includes(name))return {url:'usage-test:'+name,shortCircuit:true};
    if(context.parentURL?.endsWith('.ets') && name.startsWith('.'))return {url:new URL(name+'.ets',context.parentURL).href,shortCircuit:true};
    return next(name,context);
  },
  load(url,context,next){
    if(url.startsWith('usage-test:'))return {shortCircuit:true,format:'module',source:'export class ClientModel{};export class HttpFailure extends Error{constructor(status){super();this.status=status;}}'};
    if(!url.endsWith('.ets'))return next(url,context);
    return {shortCircuit:true,format:'module',source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*|@Trace\s+/g,''))};
  }
});
const {MachineSubscriptionUsage,SubscriptionAccount,subscriptionGroups,usageTimelinePosition,usageProviderTitle}=await import('../entry/src/main/ets/models/MachineSubscriptionUsage.ets');
const {HttpFailure}=await import('usage-test:../platform/HubTransport');
const now=Date.parse('2026-11-23T12:00:00Z'), hour=3600000;
const win=(label,hours,extra={})=>({label,used_percent:50,window_minutes:0,resets_at:new Date(now+hours*hour).toISOString(),...extra});
const account=(name,provider,windows,extra={})=>new SubscriptionAccount({id:name,name,provider,sampled_at:new Date(now).toISOString(),windows,...extra},'hub');
test('provider and account order, model-specific windows, expired samples and stale pace match the Swift card rules',()=>{
  const later=account('A','claude',[win('Fable',10,{model:'Fable'}),win('7d',62),win('5h',2,{protect_percent:99})]);
  const first=account('Z','CLAUDE',[win('7d',30)]), noWeek=account('B','claude',[win('5h',2)]), codex=account('Pro','codex',[win('7d',50)]);
  const old=account('old','unused',[win('5h',3)],{sampled_at:new Date(now-73*hour).toISOString()});const expired=account('expired','unused',[win('5h',-1)]);
  const groups=subscriptionGroups([codex,later,old,noWeek,first,expired],now);
  assert.deepEqual(groups.map(g=>g.provider),['codex','claude']);assert.deepEqual(groups[1].accounts.map(a=>a.name),['Z','A','B']);
  assert.deepEqual(later.liveWindows(now).map(w=>w.label),['5h','7d','Fable']);const sample=later.liveWindows(now)[0];assert.equal(sample.protection,99);assert.equal(sample.pace(now,false),60);assert.ok(Number.isNaN(sample.pace(now,true)));
  assert.equal(usageTimelinePosition(24),.45);assert.equal(usageTimelinePosition(200),1);assert.equal(usageTimelinePosition(-1),0);assert.equal(usageProviderTitle('xai','Other'),'xAI');
});
const ticks=()=>new Promise(resolve=>setImmediate(resolve));
function create(id,response){let gen=1;let saved='';let reads=0;let calls=0;let blocked=false;const scope=()=>id+'/current';const o={profile:{id},connected:true,isAppInForeground:()=>!blocked,scope,connectionGeneration:()=>gen,owns:(g,s)=>gen===g&&s===scope(),repo:()=>({call:async path=>{assert.equal(path,'/api/v1/usage/windows');calls++;return response();}}),directoryOwners:()=>[o],loadView:async key=>{assert.equal(key,'usage.hiddenProviders');reads++;return saved;},saveView:async(key,body)=>{assert.equal(key,'usage.hiddenProviders');saved=body;},version:()=>{gen++;},foreground:value=>{blocked=!value},callCount:()=>calls,readCount:()=>reads,persisted:()=>saved};return o;}
test('cache is per connected hub, never crosses ownership, a 404 empties it, and the filter only writes a device preference',async()=>{
  let status=200;const sample={id:'id',provider:'codex',name:'Member',windows:[win('7d',24)]};const first=create('one',()=>{if(status!==200)throw new HttpFailure(status);return {accounts:[sample]};});const second=create('two',()=>({accounts:[sample]}));first.directoryOwners=()=>[first,second];const m=new MachineSubscriptionUsage();await m.refresh(first,true);await ticks();assert.equal(m.accounts.length,2);assert.notEqual(m.accounts[0].id,m.accounts[1].id);await m.refresh(first,false);assert.equal(first.callCount(),1);m.setShown(first,'codex',false);await ticks();assert.deepEqual(JSON.parse(first.persisted()),{hidden:['codex']});assert.equal(first.callCount(),1);status=503;await m.refresh(first,true);assert.equal(m.accounts.length,2);status=404;await m.refresh(first,true);assert.deepEqual(m.accounts.map(a=>a.id),[JSON.stringify(['two','id'])]);first.directoryOwners=()=>[first];await m.refresh(first,true);assert.deepEqual(m.accounts,[]);first.foreground(false);const count=first.callCount();await m.refresh(first,true);assert.equal(first.callCount(),count);
});
test('a late payload from a previous connection cannot repopulate the visible account list',async()=>{
  let finish;const owner=create('old',()=>new Promise(resolve=>{finish=resolve;}));const m=new MachineSubscriptionUsage();const loading=m.refresh(owner,true);await ticks();owner.version();finish({accounts:[{id:'lost',provider:'other',windows:[win('5h',1)]}]});await loading;assert.deepEqual(m.accounts,[]);
});
