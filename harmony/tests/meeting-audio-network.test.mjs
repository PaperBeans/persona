import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';

const kinds={BEARER_WIFI:1,BEARER_ETHERNET:2,BEARER_CELLULAR:3,BEARER_VPN:4};
const state={bearers:[1],metered:false,net:123,bind:0,pref:'false'};
globalThis.__meetingNetwork=state;
registerHooks({
 resolve(spec,ctx,next){
  const stub={
   '@kit.AbilityKit':'export const common={};',
   '@kit.NetworkKit':'const s=globalThis.__meetingNetwork;export const socket={};export const connection={getDefaultNetSync:()=>({netId:s.net,bindSocket:async()=>{s.bind++;}}),getNetCapabilitiesSync:()=>({bearerTypes:s.bearers}),isDefaultNetMeteredSync:()=>s.metered,NetBearType:'+JSON.stringify(kinds)+'};',
   './LocalStore':'export class LocalStore {async open(){} async get(){return globalThis.__meetingNetwork.pref;} async put(_,value){globalThis.__meetingNetwork.pref=value;}}'
  }[spec];
  if(stub)return {url:'data:text/javascript,'+encodeURIComponent(stub),shortCircuit:true};
  return next(spec,ctx);
 },
 load(url,ctx,next){if(url.endsWith('.ets'))return {shortCircuit:true,format:'module',source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s*/g,''))};return next(url,ctx);}
});
const {MeetingAudioNetwork}=await import('../entry/src/main/ets/platform/MeetingAudioNetwork.ets');
test('the actual network policy binds only validated unmetered Wi-Fi or Ethernet, and checks consent and identity before every write',async()=>{
 const n=new MeetingAudioNetwork();assert.equal(n.key(false),'');await n.initialize({});
 assert.equal(n.key(false),'any:123');assert.equal(n.key(true),'wifi:123');
 let owns=true;const rule=n.binding(n.key(true),true,()=>owns);await rule.bind({});assert.equal(state.bind,1);assert.ok(rule.directOnly&&rule.current());
 owns=false;assert.equal(rule.current(),false);await assert.rejects(rule.bind({}),/network_changed/);owns=true;
 for(const [bearers,metered] of [[[kinds.BEARER_CELLULAR],false],[[kinds.BEARER_WIFI,kinds.BEARER_CELLULAR],false],[[kinds.BEARER_VPN,kinds.BEARER_WIFI],false],[[kinds.BEARER_WIFI],true],[[],false]]){
  state.bearers=bearers;state.metered=metered;assert.equal(n.key(true),'');assert.equal(rule.current(),false);
 }
 state.bearers=[kinds.BEARER_ETHERNET];state.metered=false;assert.equal(n.key(true),'wifi:123');
 state.net=124;assert.equal(rule.current(),false);state.net=0;assert.equal(n.key(false),'');state.net=123;
 await n.setWifiOnly(true);assert.equal(n.key(false),'wifi:123');
 const fresh=new MeetingAudioNetwork();await fresh.initialize({});assert.equal(fresh.key(false),'wifi:123');
 await fresh.setWifiOnly(false);assert.equal(fresh.key(false),'any:123');assert.equal(fresh.key(true),'wifi:123');
});
