import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';
const state={ready:true,foregrounded:0,background:0,activated:0,recordingStops:0,dictationStops:0,lookUnbinds:0,localeAnnouncements:0,language:'default',notifications:[],query:Promise.resolve(),init:Promise.resolve()};
globalThis.__personaAbilityTest=state;
registerHooks({
 resolve(specifier,context,next){
  if(context.parentURL?.endsWith('/EntryAbility.ets')){
   if(specifier==='../core/WireCompatibility')return {url:new URL(specifier+'.ts',context.parentURL).href,shortCircuit:true};
   if(specifier==='../models/Wire')return {url:new URL(specifier+'.ets',context.parentURL).href,shortCircuit:true};
   return {url:'ability-test:'+specifier,shortCircuit:true};
  }
  return next(specifier,context);
 },
 load(url,context,next){
  if(url.startsWith('ability-test:'))return {format:'module',shortCircuit:true,source:`const s=globalThis.__personaAbilityTest;
   export const phoneTextSize={attach(){},refresh(scale){s.fontScale=scale;},detach(){}};
   export const phonePasswordLogin={receive(uri){if(!s.expectPasswordReturn || uri!==s.expectPasswordReturn)return false;s.receivedPasswordReturn=uri;return true;}};
   export const AbilityConstant={},ConfigurationConstant={ColorMode:{COLOR_MODE_LIGHT:1,COLOR_MODE_DARK:2,COLOR_MODE_NOT_SET:0}},Want={};
   export class UIAbility {context={config:{fontSizeScale:1},resourceManager:{},getApplicationContext:()=>({setColorMode(){}})}}
   export const window={};export const Strings={bind(){},refresh(){}};
   export const nativeMeetingServices={async initialize(){s.meetingRefreshes=(s.meetingRefreshes||0)+1},setRecordingEvent(){},
     receive(parameters){if(parameters.personaMeetingType==='persona.meeting.reminder' && parameters.occurrence)s.meetingOpens=(s.meetingOpens||0)+1}};
   export const NOTIFICATION_ACTION='persona.notification.OPEN';export const nativeNotifications={refresh:()=>s.query};
   export const deviceFeatures={async background(){},async foreground(){}};
   export const NativeDictation={cancelActive(){s.dictationStops++;}};
   export const KnowledgeRecording={async backgroundAll(){s.recordingStops++;}};
   export const deviceLook={async initialize(){},async bind(owner){if(owner===undefined)s.lookUnbinds++;}};
   export const nativeEnvironment={update(){},bindWindow(){},unbindWindow(){}};
   export const client={get ready(){return s.ready;},set ready(value){s.ready=value;},appearance:0,get language(){return s.language;},announceLanguage(){s.localeAnnouncements++;},error:'',initialize:()=>s.init,activateForeground(){s.activated++;},async foregrounded(){s.foregrounded++;},async background(){s.background++;},async shutdown(){s.background++;},async openNotification(target){s.notifications.push(target);}};
  `};
  if(url.endsWith('.ets')||url.endsWith('.ts'))return {format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,''))};
  return next(url,context);
 }
});
const {default:EntryAbility}=await import('../entry/src/main/ets/entryability/EntryAbility.ets');
const turn=()=>new Promise(resolve=>setTimeout(resolve,0));
function reset(){Object.assign(state,{ready:true,foregrounded:0,background:0,activated:0,recordingStops:0,dictationStops:0,lookUnbinds:0,localeAnnouncements:0,language:'default',notifications:[],query:Promise.resolve(),init:Promise.resolve()});}
test('a foreground permission query cannot reopen a connection after background or destroy',async()=>{
 for(const stop of ['onBackground','onDestroy']){
  reset();let release;state.query=new Promise(resolve=>release=resolve);const ability=new EntryAbility();ability.onForeground();ability[stop]();release();await turn();
  assert.equal(state.foregrounded,0);assert.equal(state.background,1);
  assert.equal(state.recordingStops,1);assert.equal(state.dictationStops,1);assert.equal(state.lookUnbinds,1);
 }
});
test('a notification arriving during cold initialization is retained and dispatched once ready',async()=>{
 reset();state.ready=false;let release;state.init=new Promise(resolve=>release=resolve);const ability=new EntryAbility();ability.onCreate({},{});
 const target={profile:'hub',manager:'personal',machine:'machine',session:'session'};ability.onNewWant({action:'persona.notification.OPEN',parameters:target},{});assert.equal(state.notifications.length,0);
 state.ready=true;release();await turn();assert.deepEqual(state.notifications,[target]);
});

test('matching system language reannounces live Hub locale after a configuration change',()=>{reset();const ability=new EntryAbility();ability.onConfigurationUpdate({});assert.equal(state.localeAnnouncements,1);state.language='ja-JP';ability.onConfigurationUpdate({});assert.equal(state.localeAnnouncements,1);});

test('a meeting reminder opens the recording page intent without starting a microphone or publishing a transcript',()=>{
 const a=new EntryAbility();state.meetingOpens=0;
 a.onNewWant({parameters:{personaMeetingType:'persona.meeting.reminder',occurrence:'synthetic-1'}},{});
 assert.equal(state.meetingOpens,1);
 a.onNewWant({parameters:{personaMeetingType:'not-a-reminder',occurrence:'synthetic-2'}},{});
 assert.equal(state.meetingOpens,1);
});

test('a matching system-browser return reaches the running password flow before pairing or navigation', () => {
 reset();
 const ability = new EntryAbility();
 const callback = 'persona://password-login?state=fixture';
 state.expectPasswordReturn = callback;
 try {
  ability.onNewWant({uri: callback}, {});
  assert.equal(state.receivedPasswordReturn, callback);
  assert.notEqual(state.pairingLink, callback);
 } finally {
  delete state.expectPasswordReturn;
  delete state.receivedPasswordReturn;
 }
});

test('a native text-size configuration is forwarded to the local display preference',()=>{reset();const ability=new EntryAbility();ability.onConfigurationUpdate({fontSizeScale:1.45});assert.equal(state.fontScale,1.45);});
