import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';

let sdk;
globalThis.__playbackSdk = () => sdk;
const modules = {
  '@kit.AbilityKit': 'export const abilityAccessCtrl = {}, common = {}, Permissions = {};',
  '@kit.CoreSpeechKit': 'export const speechRecognizer = {}; export const textToSpeech={createEngine(){throw new Error("unexpected meeting speech output");}};',
  '@kit.ArkTS': 'export const util = {};',
  '@kit.CoreFileKit': `export const fileIo = {
    OpenMode: {READ_ONLY: 0},
    statSync: (...args) => globalThis.__playbackSdk().stat(...args),
    readTextSync: (...args) => globalThis.__playbackSdk().manifest(...args),
    openSync: (...args) => globalThis.__playbackSdk().open(...args),
    readSync: (...args) => globalThis.__playbackSdk().read(...args),
    closeSync: (...args) => globalThis.__playbackSdk().close(...args)
  };`,
  '@kit.AudioKit': `export const audio = {
    AudioState: {STATE_RUNNING: 2}, AudioChannel: {CHANNEL_1: 1},
    AudioSampleFormat: {SAMPLE_FORMAT_S16LE: 16}, AudioEncodingType: {ENCODING_TYPE_RAW: 0},
    SourceType:{SOURCE_TYPE_MIC:0,SOURCE_TYPE_VOICE_COMMUNICATION:7},
    getAudioManager:()=>({getStreamManager:()=>({isAcousticEchoCancelerSupported:(type)=>type===7 && globalThis.__playbackSdk().supportsEcho})}),
    createAudioCapturer:(...args)=>globalThis.__playbackSdk().createCapturer(...args),
    StreamUsage: {STREAM_USAGE_MUSIC: 1}, AudioDataCallbackResult: {VALID: 1, INVALID: 0},
    createAudioRenderer: (...args) => globalThis.__playbackSdk().createRenderer(...args)
  };`
};
registerHooks({
  resolve(specifier, context, next) {
  if (specifier==='./MeetingAudioNetwork') return {url:'data:text/javascript,export const meetingAudioNetwork = {}',shortCircuit:true};
    return Object.hasOwn(modules, specifier) ? {url: 'recording-playback:' + specifier, shortCircuit: true} : next(specifier, context);
  },
  load(url, context, next) {
    if (url.startsWith('recording-playback:')) return {format: 'module', source: modules[url.slice('recording-playback:'.length)], shortCircuit: true};
    if (url.endsWith('NativeRecording.ets'))
      return {format: 'module', source: stripTypeScriptTypes(readFileSync(new URL(url), 'utf8')), shortCircuit: true};
    return next(url, context);
  }
});
const {NativeRecording} = await import('../entry/src/main/ets/platform/NativeRecording.ets');

async function fixture() {
  const id = '00000000-0000-4000-8000-000000000001';
  const path = '/private/knowledge-recordings/' + id;
  const entry = {id, hubId: 'hub', profileId: 'profile', managerId: 'work', audioBytes: 6, captureState:'stopped'};
  const events = new Map();
  const renderer = {
    state: 0, started: 0, stopped: 0, drained: 0, released: 0,
    on(name, ...args) {
      if (name === 'markReach') assert.equal(args[0], 3);
      events.set(name, args.at(-1));
    },
    off(name) { events.delete(name); },
    async start() { this.started++; this.state = 2; },
    async stop() { this.stopped++; this.state = 3; },
    async drain() { this.drained++; },
    async release() { this.released++; }
  };
  const state = {
    entry: {...entry}, size: 6, opened: 0, closed: 0, readBytes: 0, creates: 0,
    stat(name) {
      assert.ok(name === '/private/knowledge-recordings' || name === path + '.pcm');
      return {size: name.endsWith('.pcm') ? this.size : 0};
    },
    manifest(name) { assert.equal(name, path + '.json'); return JSON.stringify(this.entry); },
    open(name) { assert.equal(name, path + '.pcm'); this.opened++; return {fd: 42}; },
    read(fd, buffer, {length}) {
      assert.equal(fd, 42);
      const bytes = new Uint8Array(buffer);
      for (let i = 0; i < length; i++) bytes[i] = ++this.readBytes;
      return length;
    },
    close(file) { assert.equal(file.fd, 42); this.closed++; },
    async createRenderer(config) {
      this.creates++;
      assert.deepEqual(config.streamInfo, {samplingRate: 16000, channels: 1, sampleFormat: 16, encodingType: 0});
      if (this.creation) await this.creation;
      return renderer;
    }
  };
  sdk = state;
  const platform = new NativeRecording();
  await platform.initialize({filesDir: '/private'});
  return {platform, entry, state, renderer, events};
}

test('the native output uses the private manifest, drains a padded final frame and releases only once', async () => {
  const {platform, entry, state, renderer, events} = await fixture();
  const finished = Promise.withResolvers();
  await platform.playLocal(entry, () => true, finished.resolve);
  assert.equal(renderer.started, 1);
  const callback = events.get('writeData');
  events.get('markReach')();
  const first = new Uint8Array(4), last = new Uint8Array(4).fill(255);
  assert.equal(callback(first.buffer), 1); assert.deepEqual([...first], [1, 2, 3, 4]);
  assert.equal(callback(last.buffer), 1); assert.deepEqual([...last], [5, 6, 0, 0]);
  assert.equal(callback(new ArrayBuffer(4)), 0);
  assert.equal(await finished.promise, false);
  await platform.stopPlayback();
  assert.deepEqual([state.closed, renderer.drained, renderer.stopped, renderer.released, events.size], [1, 1, 1, 1, 0]);
});

test('an owner revoked during native output creation cannot start or read local bytes', async () => {
  const {platform, entry, state, renderer} = await fixture();
  const gate = Promise.withResolvers();
  state.creation = gate.promise;
  let active = true, completions = 0;
  const opening = platform.playLocal(entry, () => active, () => completions++);
  const rejected = assert.rejects(opening, /request_updated/);
  assert.equal(state.creates, 1);
  active = false; await platform.stopPlayback(); gate.resolve(); await rejected;
  assert.deepEqual([renderer.started, state.readBytes, state.closed, renderer.released, completions], [0, 0, 1, 1, 0]);
});

test('a different stored owner or mismatching PCM size never reaches an audio renderer', async () => {
  const {platform, entry, state} = await fixture();
  for (const key of ['managerId', 'hubId', 'profileId', 'id', 'captureState', 'audioBytes']) {
    state.entry = {...entry, [key]: 'different'};
    await assert.rejects(platform.playLocal(entry, () => true, assert.fail), /request_updated/);
  }
  state.entry = {...entry};
  for (const size of [0, 4, 5, 8]) {
    state.size = size;
    await assert.rejects(platform.playLocal(entry, () => true, assert.fail), /phone_record_play_failed/);
  }
  assert.deepEqual([state.opened, state.readBytes, state.creates], [0, 0, 0]);
});

test('switching a live synthetic capture reports system echo support, preserves its file and recovers the ordinary source if AEC cannot start',async()=>{
  for (const fails of [false,true]) {
    const {platform,entry,state}=await fixture(), routes=[],released=[],frames=[];
    const capture=(type)=>({ state:2, on(_k,fn){this.emit=fn;},off(){}, async stop(){this.state=0;},
      async start(){if(fails && type===7) throw new Error('unavailable');this.state=2;},async release(){released.push(type);} });
    state.supportsEcho=false;
    state.createCapturer=async(config)=>{routes.push(config.capturerInfo.source);return capture(config.capturerInfo.source);};
    platform.activeId=entry.id; platform.fileDescriptor=17; platform.activeBytes=1280; platform.capturer=capture(0); platform.readListener=b=>frames.push(b.byteLength);
    assert.equal(await platform.enableSpeakerEcho(entry.id),false); assert.deepEqual(routes,[]);
    state.supportsEcho=true;
    assert.equal(await platform.enableSpeakerEcho('other'),false);
    assert.equal(await platform.enableSpeakerEcho(entry.id),!fails);
    assert.deepEqual(routes,fails ? [7,0] : [7]); assert.deepEqual(released,fails ? [0,7] : [0]);
    platform.capturer.emit(new ArrayBuffer(640)); assert.deepEqual(frames,[640]);
    assert.equal(platform.fileDescriptor,17); assert.equal(platform.activeBytes,1280);
  }
});

test('a stop racing an unfinished synthetic echo reconfiguration releases the new source before closing the recording',async()=>{
  const {platform,entry,state}=await fixture(), gate=Promise.withResolvers(), created=Promise.withResolvers();
  let starts=0,releases=0;
  const previous={state:2,off(){},async stop(){},async release(){}};
  state.supportsEcho=true;
  state.createCapturer=async()=>{created.resolve(); await gate.promise; return {state:0,async start(){starts++;},async release(){releases++;}};};
  platform.activeId=entry.id;platform.fileDescriptor=17;platform.capturer=previous;platform.readListener=()=>{};
  const change=platform.enableSpeakerEcho(entry.id);await created.promise;
  const stop=platform.stop();gate.resolve();assert.equal(await change,false);await stop;
  assert.deepEqual([starts,releases,platform.capturer,platform.fileDescriptor],[0,1,undefined,-1]);
});
