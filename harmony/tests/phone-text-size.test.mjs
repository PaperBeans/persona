import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import {phoneTextScale} from '../entry/src/main/ets/core/PhoneTextSize.ts';

registerHooks({
  resolve(spec,ctx,next) {
    if (ctx.parentURL?.endsWith('/PhoneTextSizePreference.ets')) {
      if (spec.startsWith('@kit.')) return {url:'phone-text:'+spec,shortCircuit:true};
      if (spec==='../core/PhoneTextSize') return {url:new URL(spec+'.ts',ctx.parentURL).href,shortCircuit:true};
    }
    return next(spec,ctx);
  },
  load(url,ctx,next) {
    if (url==='phone-text:@kit.ArkData') return {format:'module',shortCircuit:true,source:'export const preferences={getPreferencesSync:ctx=>ctx.store};'};
    if (url==='phone-text:@kit.BasicServicesKit') return {format:'module',shortCircuit:true,source:`export const settings={display:{FONT_SCALE:'font'},domainName:{USER_PROPERTY:'user'},getValueSync:ctx=>String(ctx.system),registerKeyObserver:(ctx,k,d,fn)=>{ctx.observe=fn;return true;},unregisterKeyObserver:ctx=>{ctx.observe=undefined;return true;}};`};
    if (url==='phone-text:@kit.AbilityKit') return {format:'module',shortCircuit:true,source:'export const common={};'};
    if (!url.endsWith('/PhoneTextSizePreference.ets')) return next(url,ctx);
    const text=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',shortCircuit:true,source:stripTypeScriptTypes(text)};
  }
});
const {PhoneTextSizePreference}=await import('../entry/src/main/ets/platform/PhoneTextSizePreference.ets');
function fixture(system=1, saved='standard') {
  const entry={choice:saved}, scales=[];
  const store={getSync:key=>entry[key],putSync:(key,v)=>entry[key]=v,async flush(){}};
  return {entry,store,scales,system,config:{fontSizeScale:system},getApplicationContext:()=>({setFontSizeScale:v=>scales.push(v)})};
}
test('the app moves one published native text category and clamps at the limits',()=>{
  for(const [base,small,large] of [[.85,.85,1],[1,.85,1.15],[1.45,1.3,1.75],[2,1.75,3.2],[3.2,2,3.2]]) {
    assert.equal(phoneTextScale(base,'small'),small);assert.equal(phoneTextScale(base,'large'),large);assert.equal(phoneTextScale(base,'standard'),base);
  }
  assert.equal(phoneTextScale(1.25,'large'),1.3);assert.equal(phoneTextScale(1.25,'small'),1.15);
});
test('standard leaves the system in control, a local nudge survives restart, restored standard tracks later changes',async()=>{
  const ctx=fixture(),state=new PhoneTextSizePreference();state.attach(ctx);state.refresh(1);
  assert.deepEqual(ctx.scales,[]);assert.equal(ctx.observe,undefined);
  assert.equal(await state.choose('large'),true);assert.equal(ctx.entry.choice,'large');assert.deepEqual(ctx.scales,[1.15]);
  ctx.system=1.45;ctx.observe();assert.equal(ctx.scales.at(-1),1.75);
  state.refresh(1.75);assert.equal(ctx.scales.length,2);
  assert.equal(await state.choose('standard'),true);assert.equal(ctx.scales.at(-1),1.45);
  ctx.system=3.2;ctx.observe();assert.equal(ctx.scales.at(-1),3.2);assert.equal(await state.choose('small'),true);assert.equal(ctx.scales.at(-1),2);
  state.detach();assert.equal(ctx.observe,undefined);const next=new PhoneTextSizePreference();next.attach(ctx);assert.equal(next.choice,'small');assert.equal(ctx.scales.at(-1),2);
});
test('a failed save leaves the active ratio and selection unchanged',async()=>{
  const ctx=fixture(),s=new PhoneTextSizePreference();s.attach(ctx);ctx.store.flush=async()=>{throw Error('full')};
  assert.equal(await s.choose('large'),false);assert.equal(s.choice,'standard');assert.equal(ctx.entry.choice,'standard');assert.deepEqual(ctx.scales,[]);
});
