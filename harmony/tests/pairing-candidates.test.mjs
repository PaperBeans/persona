import test from 'node:test';import assert from 'node:assert/strict';import {registerHooks,stripTypeScriptTypes} from 'node:module';import {readFileSync} from 'node:fs';
globalThis.pairingCandidateState={calls:[],responses:new Map()};
registerHooks({resolve(s,c,n){if(c.parentURL?.endsWith('/Pairing.ets')){if(s==='../core/WireCompatibility'||s==='../core/HubAccess')return{url:new URL(s+'.ts',c.parentURL).href,shortCircuit:true};return{url:'pairing-candidate:'+s,shortCircuit:true};}return n(s,c);},load(u,c,n){
if(u==='pairing-candidate:@kit.ArkTS')return{format:'module',shortCircuit:true,source:`class URLValue extends URL{static parseURL(value){return new URLValue(value);}get params(){return this.searchParams;}}export const url={URL:URLValue};`};
if(u==='pairing-candidate:./HubTransport')return{format:'module',shortCircuit:true,source:`export function hubCredentialOrigin(value){const u=new URL(value);return u.protocol+'//'+u.hostname+':'+(u.port||(u.protocol==='https:'?'443':'80'));}export function normalizeBaseUrl(value){const url=new URL(value);if(!['http:','https:'].includes(url.protocol)||url.username||url.password||url.hash||url.search)throw Error('invalid_endpoint');return url.href.replace(/\\/+$/,'')+'/';}export class EndpointConfig{constructor(baseUrl,hubId,token,pin,managerId='',alternateUrls=[],proxy,access){Object.assign(this,{baseUrl:normalizeBaseUrl(baseUrl),hubId,token,pin,managerId,alternateUrls,proxy,access});}}export class HubTransport{constructor(config){this.config=config;}async request(path,method='GET',body=''){const s=globalThis.pairingCandidateState;s.calls.push({config:this.config,path,method,body});const value=s.responses.get(this.config.baseUrl);if(value instanceof Error)throw value;if(path==='/api/v1/auth/tokens')return JSON.stringify({id:'device-id',token:'synthetic-device-token'});if(typeof value==='function')return value(this.config);return JSON.stringify(value);}close(){}}`};
if(u.endsWith('.ets')||u.endsWith('.ts'))return{format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(u),'utf8'))};return n(u,c);}});
const {Pairing}=await import('../entry/src/main/ets/platform/Pairing.ets');
const state=globalThis.pairingCandidateState,primary='https://primary.invalid:18662/',secondary='https://secondary.invalid:18662/',pin='A'.repeat(43);
function reset(){state.calls=[];state.responses=new Map([[primary,new Error('offline')],[secondary,{ok:true,hub_id:'same-hub',hub_name:'Synthetic Hub'}]]);}
function link(extra=[]){const query=new URLSearchParams({url:'http://primary.invalid:18660',token:'synthetic-pair-code',hub:'same-hub',fp:pin,tls:'18662'});query.append('alt',secondary);for(const value of extra)query.append('alt',value);return 'persona://pair?'+query;}

test('previewing a scanned code shows its destination before contacting the hub', () => {
  reset();
  const parsed = Pairing.parse(link());
  assert.equal(parsed.initialAddress, primary);
  assert.deepEqual(state.calls, []);
});
test('alternate pairing probes are anonymous and only the confirmed verified endpoint receives the code',async()=>{reset();const pair=Pairing.parse(link()),target=await pair.inspect();assert.equal(target.baseUrl,secondary);assert.ok(state.calls.every(call=>call.config.token===''));await assert.rejects(pair.redeem({...target,baseUrl:primary}),/not_confirmed/);const config=await pair.redeem(target);assert.equal(config.token,'synthetic-device-token');assert.deepEqual(config.alternateUrls,[primary]);const secretCalls=state.calls.filter(call=>call.config.token);assert.equal(secretCalls.length,1);assert.equal(secretCalls[0].config.baseUrl,secondary);await assert.rejects(pair.redeem(target),/not_confirmed/);});
test('reconnection alternatives must pass the same Hub identity and pin checks before returning an address',async()=>{reset();state.responses.set(primary,{ok:true,hub_id:'wrong-hub'});assert.equal(await Pairing.reachable(primary,[secondary],'same-hub',pin),secondary);assert.ok(state.calls.every(call=>call.config.token===''));state.responses.set(secondary,()=>{throw Error('pin_mismatch');});await assert.rejects(Pairing.reachable(primary,[secondary],'same-hub',pin),/pin_mismatch/);});
test('untrusted alternatives, excess addresses, and stale reconnections fail before credential transfer',async()=>{reset();assert.throws(()=>Pairing.parse(link(['http://untrusted.invalid/'])),/invalid_pairing/);assert.throws(()=>Pairing.parse(link(Array.from({length:8},(_,i)=>`https://alt${i}.invalid/`))),/invalid_pairing/);await assert.rejects(Pairing.reachable(primary,[secondary],'same-hub',pin,()=>false),/request_updated/);assert.equal(state.calls.length,0);assert.equal(await Pairing.reachable(primary,[secondary],'same-hub',''),primary);assert.equal(state.calls.length,0);});

test('a cancelled revalidation and a concurrent redemption cannot exchange the code',async()=>{reset();state.responses.set(primary,{ok:true,hub_id:'same-hub',hub_name:'Synthetic Hub'});const pair=Pairing.parse(link()),target=await pair.inspect();let release,started,allowed=true;const gate=new Promise(r=>release=r),entered=new Promise(r=>started=r);state.responses.set(primary,async()=>{started();await gate;return JSON.stringify({ok:true,hub_id:'same-hub',hub_name:'Synthetic Hub'});});const old=pair.redeem(target,'Synthetic',()=>allowed);await entered;await assert.rejects(pair.redeem(target),/not_confirmed/);allowed=false;release();await assert.rejects(old,/request_updated/);assert.equal(state.calls.some(call=>call.path==='/api/v1/auth/tokens'),false);});

test('pairing binds the service token to the entered origin across fallback and requires both form values', async () => {
  reset();
  const pairing = Pairing.parse(link());
  assert.throws(() => pairing.setCloudflareAccess('client', ''), /cloudflare_access_incomplete/);
  assert.throws(() => pairing.setCloudflareAccess('client', 'first\r\nAnother: bad'), /cloudflare_access_invalid/);
  pairing.setCloudflareAccess(' client ', ' example-secret ');
  const target = await pairing.inspect();
  assert.throws(() => pairing.setCloudflareAccess('new', 'different'), /pairing_target_changed/);
  const endpoint = await pairing.redeem(target);
  const expected = {'CF-Access-Client-Id':'client','CF-Access-Client-Secret':'example-secret'};
  for (const {config} of state.calls) {
    const headers = {};
    const current = new URL(config.baseUrl);
    const origin = current.protocol + '//' + current.host;
    config.access.apply(origin, headers);
    assert.deepEqual(headers, config.baseUrl === primary ? expected : {});
  }
  const headers = {};
  assert.equal(endpoint.access.apply(new URL(primary).origin, headers), true);
  assert.deepEqual(headers, expected);
  assert.ok(!JSON.stringify(endpoint.access).includes('example-secret'));
});

test('one confirmed SSH route carries both identity checks and the pairing exchange, preserving Cloudflare', async () => {
  reset(); state.responses.set(primary,{ok:true,hub_id:'same-hub',hub_name:'Synthetic Hub'});
  const pair=Pairing.parse(link()); pair.setCloudflareAccess('signed-access','test-secret');
  const lease={isCurrent:()=>true}; const opens=[];
  const route={async open(address,spki){opens.push([address,spki]);return lease;},close(){}};
  pair.setSshRoute(route,''); const target=await pair.inspect(); await pair.redeem(target);
  assert.deepEqual(opens,[[primary,pin]]);
  assert.deepEqual(state.calls.map(row=>[row.path,row.config.baseUrl,row.config.proxy===lease,!!row.config.access,row.config.token]),[
    ['/api/v1/health',primary,true,true,''],['/api/v1/health',primary,true,true,''],['/api/v1/auth/tokens',primary,true,true,'synthetic-pair-code']
  ]);
});
test('neither a conflicting Hub pin, a failed SSH route, nor cancellation can spend the code on an alternate', async () => {
  reset(); const first=Pairing.parse(link());let closed=false;
  const fail={async open(){throw Error('ssh_host_key_mismatch');},close(){closed=true;}};
  assert.throws(()=>first.setSshRoute(fail,'B'.repeat(43)),/invalid_pin/);
  first.setSshRoute(fail,'');await assert.rejects(first.inspect(),/ssh_host_key_mismatch/);assert.equal(closed,true);
  assert.equal(state.calls.length,0);
  const second=Pairing.manual(primary,'synthetic-pair-code');let active=true;
  second.setSshRoute({async open(){return {isCurrent:()=>active};},close(){active=false;}},pin);
  state.responses.set(primary,{ok:true,hub_id:'same-hub',hub_name:'Synthetic Hub'});
  const target=await second.inspect();active=false;await assert.rejects(second.redeem(target),/ssh_cancelled/);
  assert.ok(state.calls.every(c=>c.config.token==='' && c.config.baseUrl===primary));
});

test('direct-first pairing spends its code only on the verified route and closes the captured permission', async () => {
  for (const direct of [true, false]) {
    reset();
    const lease = { isCurrent: () => true };
    let accepted = false, opens = 0, active = true;
    const route = {
      open: async () => { opens++; return lease; },
      close: () => { active = false; },
      admitDirect: (address, actualPin) => { assert.equal(address, primary); assert.equal(actualPin, pin); accepted = true; },
      directCurrent: () => accepted && active
    };
    state.responses.set(primary, config => {
      assert.equal(config.pin, pin);
      if (!direct && !config.proxy) throw Error('unreachable');
      return JSON.stringify({ok:true,hub_id:'same-hub',hub_name:'Synthetic Hub'});
    });
    const pair = Pairing.parse(link()); pair.setSshRoute(route,'');
    const selected = await pair.inspect();
    assert.equal(accepted,direct); assert.equal(opens,direct ? 0 : 1);
    await pair.redeem(selected);
    const writes = state.calls.filter(item => item.path.endsWith('/auth/tokens'));
    assert.equal(writes.length,1); assert.equal(writes[0].config.proxy,direct ? undefined : lease);
    assert.ok(state.calls.every(item => item.config.baseUrl === primary));
  }
  reset();let live = true;
  state.responses.set(primary,{ok:true,hub_id:'same-hub',hub_name:'Synthetic Hub'});
  const pair = Pairing.parse(link());
  pair.setSshRoute({open:async()=>{throw Error('unexpected SSH');},admitDirect:()=>{},directCurrent:()=>live,close:()=>{live=false;}},'');
  const selected = await pair.inspect();live=false;
  await assert.rejects(pair.redeem(selected),/ssh_cancelled/);
  assert.equal(state.calls.filter(item=>item.config.token).length,0);
});
test('a stored fallback tunnel checks even a single direct address against the paired Hub', async () => {
  reset();state.responses.set(primary,{ok:true,hub_id:'another-hub'});
  await assert.rejects(Pairing.reachable(primary,[],'same-hub',pin,()=>true,undefined,true),/hub_identity_mismatch/);
  assert.ok(state.calls.every(c=>!c.config.token && c.config.pin===pin));
});
