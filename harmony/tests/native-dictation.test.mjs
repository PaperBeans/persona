import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks, stripTypeScriptTypes} from 'node:module';
import {readFileSync} from 'node:fs';

const stubs = new Map([
  ['@kit.AudioKit', 'export const audio = globalThis.__phoneAudio'],
  ['@kit.CoreSpeechKit', 'export const speechRecognizer = {createEngine: options => globalThis.__phoneSpeech(options)}'],
  ['@kit.ArkTS', "export const util = {generateRandomUUID: () => 'test-' + (++globalThis.__phoneSequence)}"],
  ['./NativeRecording', 'export const requestRecordingMicrophone = context => globalThis.__phonePermission(context)']
]);
registerHooks({
  resolve(spec, ctx, next) {
    return stubs.has(spec) ? {url:'dictation:' + spec,shortCircuit:true} : next(spec, ctx);
  },
  load(url, ctx, next) {
    if (url.startsWith('dictation:')) return {format:'module',source:stubs.get(url.slice(10)),shortCircuit:true};
    if (url.endsWith('/PhoneDictationControl.ets')) {
      // Run the actual control handlers with an inert host context. Rendering uses the SDK and is covered by HVD.
      const component=readFileSync(new URL(url),'utf8').split('  @Builder panel()')[0]
        .replace(/^import .+;$/gm,'').replace(/@Monitor\([^)]*\)\s*/g,'')
        .replace(/@(ComponentV2|Require|Param|Event|Local)\s*/g,'').replace('export struct','export class')+'\n}';
      const native=new URL('../platform/NativeDictation.ets',url).href;
      const imports='import {NativeDictation} from '+JSON.stringify(native)+'; const Colors={ink:"#19191d"}; const i18n={getSystemLanguage:()=>"zh-CN"};\n';
      return {format:'module',shortCircuit:true,source:imports+stripTypeScriptTypes(component)};
    }
    if (url.endsWith('/NativeDictation.ets')) return {format:'module',shortCircuit:true,
      source:stripTypeScriptTypes(readFileSync(new URL(url), 'utf8'))};
    return next(url, ctx);
  }
});
globalThis.__phoneSequence = 0;
let createInput;
globalThis.__phoneAudio = {
  AudioSamplingRate: {SAMPLE_RATE_16000:16000}, AudioChannel: {CHANNEL_1:1},
  AudioSampleFormat: {SAMPLE_FORMAT_S16LE:16}, AudioEncodingType: {ENCODING_TYPE_RAW:0},
  SourceType:{SOURCE_TYPE_MIC:0}, createAudioCapturer: options => createInput(options)
};
const {NativeDictation} = await import('../entry/src/main/ets/platform/NativeDictation.ets');
const {PhoneDictationControl}=await import('../entry/src/main/ets/components/PhoneDictationControl.ets');
const tick = () => new Promise(resolve => setImmediate(resolve));

function setup() {
  const frames = [], events = [], input = {released:0, async start(){}, async release(){ this.released++; },
    on(_, listener) {this.emit = listener;}, off(){ this.emit = undefined; }};
  const engine = {
    finishCount:0, shutdownCount:0, cancelCount:0,
    setListener(listener){this.listener = listener;},
    startListening(args){this.session = args.sessionId; this.listener.onStart(this.session,'');},
    writeAudio(id, bytes){assert.equal(id,this.session); frames.push([...bytes]);},
    finish(){this.finishCount++;}, cancel(){this.cancelCount++;}, shutdown(){this.shutdownCount++;}
  };
  globalThis.__phonePermission = async () => {};
  globalThis.__phoneSpeech = async options => {assert.equal(options.online,1);return engine;};
  createInput = async options => {assert.equal(options.streamInfo.samplingRate,16000);return input;};
  const run = new NativeDictation({
    listening:()=>events.push(['listening']), preview:text=>events.push(['preview',text]),
    completed:text=>events.push(['complete',text]), failed:key=>events.push(['error',key]),
    cancelled:()=>events.push(['cancel'])
  },'test-owner');
  return {run, engine, input, frames, events};
}

test('captured PCM crosses callback boundaries without dropping bytes and final speech is offered once', async () => {
  const {run,engine,input,frames,events} = setup();
  await run.start({}); await tick();
  assert.deepEqual(events,[['listening']]);
  input.emit(new Uint8Array(511).fill(1).buffer);
  input.emit(new Uint8Array(432).fill(2).buffer);
  engine.listener.onResult(engine.session,{result:'正在',isFinal:false});
  engine.listener.onResult(engine.session,{result:'今天',isFinal:true});
  engine.listener.onResult(engine.session,{result:'下雨',isFinal:false});
  run.finish(); run.finish();
  assert.equal(engine.finishCount,1);
  assert.equal(input.released,1);
  assert.deepEqual(frames.flat(),[...Array(511).fill(1),...Array(432).fill(2),...Array(337).fill(0)]);
  engine.listener.onComplete(engine.session,'');
  engine.listener.onComplete(engine.session,'');
  assert.equal(engine.shutdownCount,1);
  assert.deepEqual(events.slice(1),[['preview','正在'],['preview','今天'],['preview','今天下雨'],['complete','今天下雨']]);
});

test('background cancellation releases a late capturer and ignores recognition from a retired conversation', async () => {
  const {run,engine,input,events} = setup();
  let ready;
  createInput = () => new Promise(resolve => {ready = resolve;});
  await run.start({});
  NativeDictation.cancelActive();
  ready(input); await tick();
  engine.listener.onResult(engine.session,{result:'old',isFinal:true});
  engine.listener.onError(engine.session,1,'late');
  assert.equal(input.released,1);
  assert.equal(engine.cancelCount,1);
  assert.equal(engine.shutdownCount,1);
  assert.deepEqual(events,[['cancel']]);
});

test('permission denial and an unavailable engine each produce an actionable result without opening a microphone', async () => {
  for (const denied of [true,false]) {
    const {run,events} = setup();
    createInput = () => assert.fail('microphone was opened');
    if (denied) globalThis.__phonePermission = async () => {throw new Error('knowledge_recording_microphone_denied');};
    else globalThis.__phoneSpeech = async () => {throw new Error('no offline service');};
    await run.start({});
    assert.deepEqual(events,[['error',denied ? 'knowledge_recording_microphone_denied' : 'phone_dictation_unavailable']]);
  }
});

test('the time limit ends capture once even when a late PCM callback arrives', async () => {
  const {run,input,engine,frames} = setup();
  await run.start({}); await tick();
  const deliver = input.emit;
  deliver(new ArrayBuffer(16000 * 2 * 61));
  deliver(new ArrayBuffer(640));
  assert.equal(engine.finishCount,1);
  assert.equal(frames.length,2998);
  run.cancel();
});

function reviewHarness() {
  const native=setup(), control=new PhoneDictationControl();
  const state={owner:'A',drafts:{A:'existing',B:'another'},insertions:0};
  control.getUIContext=()=>({getHostContext:()=>({})});
  control.owner='A';control.language='zh-CN';
  control.allowed=owner=>state.owner===owner;
  control.insert=(text,owner)=>{
    if (state.owner!==owner) return false;
    state.drafts[owner]+=' '+text;state.insertions++;return true;
  };
  const finish=()=>{native.engine.listener.onResult(native.engine.session,{result:'今天',isFinal:true});
    native.engine.listener.onComplete(native.engine.session);};
  return {native,control,state,finish};
}

test('the explicit keyboard choice opens the input method directly and preserves the draft', () => {
  const {control, state} = reviewHarness();
  let opened = 0;
  control.chooseLanguage = () => 'keyboard';
  control.keyboard = () => opened++;
  control.start();
  assert.equal(opened, 1);
  assert.equal(control.shown, false);
  assert.equal(state.drafts.A, 'existing');
  assert.equal(state.insertions, 0);
});
test('a confirmed dictation is inserted exactly once and appends to the latest typed draft',async()=>{
  const {control,state,finish}=reviewHarness();
  control.start();await tick();state.drafts.A+=' edited';finish();
  assert.equal(control.phase,'complete');
  control.accept();control.accept();
  assert.equal(state.drafts.A,'existing edited 今天');assert.equal(state.insertions,1);
  assert.equal(control.shown,false);assert.equal(control.transcript,'');
});
test('pending accept is rejected when either the rendered owner or the actual conversation moves',async()=>{
  for (const switchProp of [false,true]) {
    const {control,state,finish}=reviewHarness();
    control.start();await tick();finish();state.owner='B';
    if (switchProp) control.owner='B';
    control.accept();
    assert.deepEqual(state.drafts,{A:'existing',B:'another'});assert.equal(state.insertions,0);
  }
});
test('denial, cancel during recording, and closing an already completed review preserve the original draft',async()=>{
  for (const action of ['denied','cancel','complete-close']) {
    const {control,state,native,finish}=reviewHarness();
    if (action==='denied') globalThis.__phonePermission=async()=>{throw new Error('knowledge_recording_microphone_denied')};
    control.start();await tick();
    if (action==='denied') assert.equal(control.phase,'error');
    if (action==='complete-close') finish();
    control.close();
    if (action==='cancel') native.engine.listener.onComplete(native.engine.session);
    control.accept();
    assert.equal(state.drafts.A,'existing');assert.equal(state.insertions,0);
  }
});
