import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

const recordingSdkFiles = new Map();
const recordingSdkDirectories = new Set();
const recordingFileIo = {
  statSync(path) {
    if (recordingSdkDirectories.has(path) || recordingSdkFiles.has(path)) return {};
    throw new Error('not_found');
  },
  mkdirSync(path) { recordingSdkDirectories.add(path); },
  listFileSync(directory) {
    const prefix = directory + '/';
    return [...recordingSdkFiles.keys()].filter((path) => path.startsWith(prefix)).map((path) => path.slice(prefix.length));
  },
  readTextSync(path) {
    if (!recordingSdkFiles.has(path)) throw new Error('not_found');
    return recordingSdkFiles.get(path);
  },
  unlinkSync(path) {
    if (!recordingSdkFiles.delete(path)) throw new Error('not_found');
  }
};
globalThis.__recordingNativeTest = { fileIo: recordingFileIo };

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === './MeetingAudioNetwork') return {url:'data:text/javascript,export const meetingAudioNetwork = {}',shortCircuit:true};
    if (specifier === './MeetingHubAudio' || specifier === './Wire' && context.parentURL?.endsWith('/MeetingHubAudio.ets')) return {url:new URL(specifier+'.ets',context.parentURL).href,shortCircuit:true};
    if (context.parentURL?.endsWith('/KnowledgeRecording.ets') && specifier === './MeetingRoom')
      return { url: new URL('./MeetingRoom.ets', context.parentURL).href, shortCircuit: true };
    if ((context.parentURL?.endsWith('/MeetingRoom.ets') || context.parentURL?.endsWith('/KnowledgeRecording.ets')) && specifier === './Wire')
      return { url: new URL('./Wire.ets', context.parentURL).href, shortCircuit: true };
    if (context.parentURL?.endsWith('/KnowledgeRecording.ets') && specifier === '../platform/NativeRecording')
      return { url: new URL('../platform/NativeRecording.ets', context.parentURL).href, shortCircuit: true };
    if (context.parentURL?.endsWith('/NativeRecording.ets') && specifier.startsWith('@kit.'))
      return { url: 'recording-sdk:' + specifier, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.startsWith('recording-sdk:')) {
      const specifier = url.slice('recording-sdk:'.length);
      const sources = {
        '@kit.AbilityKit': 'export const abilityAccessCtrl = {}; export const common = {}; export const Permissions = {};',
        '@kit.AudioKit': 'export const audio = {};',
        '@kit.CoreSpeechKit': 'export const speechRecognizer = {}; export const textToSpeech = {};',
        '@kit.CoreFileKit': 'export const fileIo = globalThis.__recordingNativeTest.fileIo;',
        '@kit.ArkTS': 'export const util = {};'
      };
      return { format: 'module', source: sources[specifier], shortCircuit: true };
    }
    if (url.endsWith('.ets')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const { KnowledgeRecording } = await import('../entry/src/main/ets/models/KnowledgeRecording.ets');

function clone(value) { return structuredClone(value); }
const models = new Set();

function newModel(platform) {
  const model = new KnowledgeRecording(platform);
  models.add(model);
  return model;
}

async function disposeModels() {
  for (const model of models) await model.dispose();
  models.clear();
}

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

class RecordingPlatformStub {
  records = new Map();
  rooms = new Map();
  loadRoom(id) { return this.rooms.has(id) ? clone(this.rooms.get(id)) : undefined; }
  saveRoom(room) { this.rooms.set(room.id,clone(room)); }
  async startRoomSpeech(final) { this.finalLine=final; }
  spoken=[];
  async speakRoom(id,text,language,admit) { if (admit()) this.spoken.push({id,text,language}); }
  echoRequests=[];
  stopRoomVoice() { this.stops=(this.stops || 0)+1; }
  async enableSpeakerEcho(id) { this.echoRequests.push(id); return this.activeId===id; }
  activeId = '';
  bytes = 64000;
  transcribeCalls = [];
  removed = [];
  cancelCalls = 0;
  transcribeResult = '你好';
  transcribeGate = undefined;
  nextSave = undefined;

  async initialize() {}
  async ensureMicrophonePermission() {}
  async start(_context, id) { this.activeId = id; return '/private/' + id + '.pcm'; }
  async stop() {
    const id = this.activeId; this.activeId = '';
    return { path: id ? '/private/' + id + '.pcm' : '', bytes: id ? this.bytes : 0, duration: id ? this.bytes / 32000 : 0 };
  }
  async save(entry) {
    if (this.nextSave && this.nextSave.matches(entry)) {
      const pending = this.nextSave; this.nextSave = undefined;
      pending.started.resolve(); await pending.release.promise;
    }
    this.records.set(entry.id, clone(entry));
  }
  async load(profileId, hubId, managerId) {
    return [...this.records.values()].filter((entry) => entry.profileId === profileId && entry.hubId === hubId && entry.managerId === managerId).map(clone);
  }
  async transcribe(id, language) {
    this.transcribeCalls.push({ id, language });
    if (this.transcribeGate) return this.transcribeGate.promise;
    if (this.transcribeResult instanceof Error) throw this.transcribeResult;
    return this.transcribeResult;
  }
  async cancelTranscription() {
    this.cancelCalls++;
    this.transcribeGate?.reject(new Error('transcription_cancelled'));
    this.transcribeGate = undefined;
  }
  async remove(id) { this.removed.push(id); this.records.delete(id); }
  blockNextUploadSave() {
    return this.blockNextSave((entry) => entry.uploadState === 'uploading');
  }
  blockNextTranscriptionSave() {
    return this.blockNextSave((entry) => entry.transcriptionState === 'transcribing');
  }
  blockNextSave(matches) {
    const pending = { matches, started: deferred(), release: deferred() };
    this.nextSave = pending;
    return pending;
  }
  async forgetProfile(profileId) {
    for (const entry of [...this.records.values()]) if (entry.profileId === profileId) await this.remove(entry.id);
  }
}

function makeClient(overrides = {}) {
  const profile = {
    id: overrides.id || 'profile-1', hubId: overrides.hubId || 'hub-1', manager: overrides.manager || 'manager-1',
    baseUrl: 'https://hub.invalid', pin: 'pin-1'
  };
  const state = { connected: overrides.connected ?? true, generation: 3, children: new Map(), calls: [] };
  const repository = { transport: { async request(path, method, body) {
    const call = { path, method, body };
    state.calls.push(call);
    if (overrides.request) return overrides.request(call);
    const data = JSON.parse(body);
    return JSON.stringify({ id: path.slice('/api/v1/recordings/'.length), revision: data.revision, status: 'stored' });
  } } };
  const client = {
    connected: state.connected, demo: false, features: overrides.features || [], profile,
    scope() { return JSON.stringify([profile.id, profile.hubId, profile.manager]); },
    connectionGeneration() { return state.generation; },
    owns(generation, scope) { return this.connected && generation === state.generation && scope === this.scope(); },
    async viewFor(row) { return state.children.get(row.manager) || client; },
    repo() { return repository; }
  };
  state.client = client; state.repository = repository;
  return state;
}

async function opened(options = {}) {
  const platform = new RecordingPlatformStub();
  const state = makeClient(options);
  const model = newModel(platform);
  await model.open(state.client, options.ownerManager || state.client.profile.manager, { filesDir: '/private' });
  return { model, platform, state };
}

async function startAndStop(model) {
  assert.equal(await model.start(), true);
  const id = model.entry.id;
  assert.equal(model.captureState, 'recording');
  assert.equal(await model.stop(), true);
  assert.equal(model.captureState, 'stopped');
  return id;
}

test('stop closes the capture into a durable local recording without uploading audio', async () => {
  const { model, platform, state } = await opened();
  const id = await startAndStop(model);
  assert.equal(model.entry.audioBytes, 64000);
  assert.equal(model.entry.duration, 2);
  assert.equal(platform.records.get(id).captureState, 'stopped');
  assert.deepEqual(state.calls, []);
});

test('meeting preflight keeps its chosen title and kind through local capture, reopening, and transcript publication', async () => {
  const { model, platform, state } = await opened();
  assert.equal(await model.start('meeting', '  Design review  '), true);
  const id = model.entry.id;
  assert.equal(await model.stop(), true);
  assert.equal(platform.records.get(id).kind, 'meeting');
  const reopened = newModel(platform);
  await reopened.open(state.client, state.client.profile.manager, { filesDir: '/private' });
  reopened.selectEntry(id);
  assert.equal(await reopened.transcribeAndUpload(), true);
  const body = JSON.parse(state.calls[0].body);
  assert.equal(body.title, 'Design review');
  assert.equal(body.kind, 'meeting');
  assert.equal(Object.hasOwn(body, 'audio'), false);
});

test('only an explicitly invited meeting streams final synthetic lines to its dedicated room; answering and ending never send PCM or target the main chat',async()=>{
  const live=[],asks=[],requests=[];
  const e=await opened({features:['meeting_attendance','meeting_conversation'],request:async({method,path,body})=>{
    const v=body ? JSON.parse(body) : {};
    requests.push({method,path,v});
    const rid=path.split('/')[4];
    if (method==='PUT') return JSON.stringify({id:rid,status:'stored',revision:v.revision,attendance:v.room.attendance,room:{state:'ready'}});
    if (method==='POST' && path.endsWith('/live')) {
      for (const [i,line] of v.segments.entries()) if(v.from+i>=live.length) live.push(line);
      return JSON.stringify({next:live.length,attendance:'listener',room:{state:'ready'}});
    }
    if (method==='POST' && path.endsWith('/ask')) {
      const ask={id:v.id,question:v.question,state:'answered',answer:'Synthetic room answer'}; asks.push(ask); return JSON.stringify({ask});
    }
    return JSON.stringify({next:live.length,asks,attendance:'listener',room:{state:'ready'}});
  }});
  assert.equal(await e.model.start('meeting','Review','listener',true),true);
  try {
    await e.model.room.sync();
    await e.platform.finalLine(0,1,'Finished line');
    await e.model.room.sync();
    assert.equal(await e.model.askRoom('What was decided?'),true);
    assert.equal(e.model.room.shownAsks[0].answer,'Synthetic room answer');
    assert.equal(await e.model.stop(),true); await e.model.room.sync();
    assert.deepEqual(live,[{start:0,end:1,text:'Finished line'}]);
    assert.equal(e.platform.rooms.get(e.model.entry.id).ended,true);
    const start=requests.find(x=>x.method==='PUT');
    assert.deepEqual(start.v.room,{attendance:'listener'}); assert.equal(start.v.private,true);
    assert.equal(Object.hasOwn(start.v,'attendance'),false); assert.equal(Object.hasOwn(start.v,'ended_at'),false);
    const end=requests.filter(x=>x.method==='PUT').at(-1);
    assert.equal(end.v.complete,true);
    assert.ok(end.v.revision>start.v.revision);
    assert.ok(requests.every(x=>x.path.startsWith('/api/v1/recordings/') && !Object.hasOwn(x.v,'audio') && !Object.hasOwn(x.v,'attending_session')));
  } finally { await e.model.dispose(); }
  const unsupported=await opened({features:['meeting_attendance']});
  assert.equal(await unsupported.model.start('meeting','Review','listener'),false);
  assert.equal(unsupported.state.calls.length,0);
});

test('only a participant who asked for an audible answer invokes the speaker, and an acknowledged answer plays once',async()=>{
  for (const [role,speak,count] of [['listener',true,0],['participant',false,0],['participant',true,1]]) {
    let ask;
    const e=await opened({features:['meeting_conversation','meeting_attendance'],request:async({method,path,body})=>{
      const v=body ? JSON.parse(body) : {};
      if (method==='PUT') return JSON.stringify({id:path.split('/').at(-1),status:'stored',attendance:role,room:{state:'ready'}});
      if (method==='POST' && path.endsWith('/ask')) {
        ask={id:v.id,state:'answered',answer:'Synthetic answer',speak,lang:'en-US'};
        return JSON.stringify({ask});
      }
      return JSON.stringify({next:0,room:{state:'ready'},asks:ask?[ask]:[]});
    }});
    assert.equal(await e.model.start('meeting','Fixture',role),true);await e.model.room.sync();
    assert.equal(await e.model.askRoom('Question?'),true);
    await e.model.room.sync(); await e.model.room.sync();
    assert.equal(e.platform.spoken.length,count);
    if (count) assert.equal(e.platform.spoken[0].text,'Synthetic answer');
    await e.model.stop(); await e.model.room.sync(); await e.model.dispose();
  }
});

test('changing the speaker updates the existing private room before any new asks and requests native echo only when opted in',async()=>{
  const uploads=[]; let activeRole='off';
  const e=await opened({features:['meeting_conversation','meeting_attendance'],request:async({method,path,body})=>{
    const value=body ? JSON.parse(body) : {};
    if (method==='PUT') { uploads.push(value); activeRole=value.room.attendance;
      return JSON.stringify({id:path.split('/').at(-1),status:'stored',attendance:activeRole,room:{state:'ready'}}); }
    return JSON.stringify({next:0,asks:[],room:{state:'ready'}});
  }});
  try {
    assert.equal(await e.model.start('meeting','Synthetic speaker','listener',true),true); await e.model.room.sync();
    assert.equal(await e.model.setRoomSpeaker(true,false),true); assert.equal(e.platform.echoRequests.length,0);
    assert.equal(await e.model.setRoomSpeaker(false,false),true); assert.equal(e.platform.stops,1);
    assert.equal(await e.model.setRoomSpeaker(true,true),true); assert.deepEqual(e.platform.echoRequests,[e.model.entry.id]);
    assert.equal(e.model.entry.roomRole,'participant');
    assert.deepEqual(uploads.map(u=>u.room.attendance),['listener','participant','listener','participant']);
    assert.ok(uploads.every((u,n)=>u.private && !Object.hasOwn(u,'attending_session') && (!n || u.revision>uploads[n-1].revision)));
    await e.model.stop(); await e.model.room.sync();
    assert.equal(await e.model.setRoomSpeaker(false,true),false);
  } finally { await e.model.dispose(); }
});

test('transcription failure keeps local audio and an explicit retry can transcribe and sync text', async () => {
  const { model, platform, state } = await opened();
  const id = await startAndStop(model);
  platform.transcribeResult = new Error('speech_engine_failed');
  assert.equal(await model.transcribeAndUpload(), false);
  assert.equal(model.transcriptionState, 'failed');
  assert.equal(platform.records.get(id).audioBytes, 64000);
  assert.deepEqual(platform.removed, []);

  platform.transcribeResult = '你好，世界。';
  assert.equal(await model.transcribeAndUpload(), true);
  assert.equal(model.transcriptionState, 'complete');
  assert.equal(model.uploadState, 'stored');
  assert.equal(state.calls.length, 1);
  const call = state.calls[0], body = JSON.parse(call.body);
  assert.equal(call.path, '/api/v1/recordings/' + id);
  assert.equal(call.method, 'PUT');
  assert.equal(body.device, 'phone');
  assert.equal(body.complete, true);
  assert.deepEqual(body.segments, [{ start: 0, end: 2, text: '你好，世界。' }]);
  assert.equal(Object.hasOwn(body, 'audio'), false);
});

test('an unknown PUT is journaled and restart retries the identical id, revision and body', async () => {
  const timeout = new Error('connection_lost'); timeout.status = 0; timeout.resultUnknown = true;
  const first = await opened({ request: async () => { throw timeout; } });
  const id = await startAndStop(first.model);
  assert.equal(await first.model.transcribeAndUpload(), false);
  assert.equal(first.model.uploadState, 'unknown');
  const originalCall = first.state.calls[0];
  const originalBody = JSON.parse(originalCall.body);

  const reopened = newModel(first.platform);
  const online = makeClient();
  await reopened.open(online.client, online.client.profile.manager, { filesDir: '/private' });
  assert.equal(reopened.entry.id, id);
  assert.equal(reopened.uploadState, 'unknown');
  assert.equal(await reopened.retryUpload(), true);
  assert.equal(online.calls.length, 1);
  assert.equal(online.calls[0].path, originalCall.path);
  assert.equal(online.calls[0].method, originalCall.method);
  assert.deepEqual(JSON.parse(online.calls[0].body), originalBody);
  assert.equal(reopened.uploadState, 'stored');
});

test('restart turns an interrupted upload into a retryable unknown without sending automatically', async () => {
  const first = await opened();
  const id = await startAndStop(first.model);
  assert.equal(await first.model.transcribeAndUpload(), true);
  const saved = clone(first.platform.records.get(id));
  const body = clone(saved.uploadBody);
  saved.uploadState = 'uploading'; saved.uploadError = ''; saved.serverStatus = ''; saved.serverRevision = 0;
  await first.platform.save(saved);

  const online = makeClient();
  const reopened = newModel(first.platform);
  await reopened.open(online.client, online.client.profile.manager, { filesDir: '/private' });
  assert.equal(reopened.uploadState, 'unknown');
  assert.equal(online.calls.length, 0);
  assert.equal(await reopened.retryUpload(), true);
  assert.equal(online.calls.length, 1);
  assert.deepEqual(JSON.parse(online.calls[0].body), body);
});

test('a deleted server recording keeps its local transcript and does not offer futile retries', async () => {
  const gone = new Error('recording_deleted'); gone.status = 410;
  const { model, platform, state } = await opened({ request: async () => { throw gone; } });
  const id = await startAndStop(model);
  assert.equal(await model.transcribeAndUpload(), false);
  assert.equal(model.transcriptionState, 'complete');
  assert.equal(model.uploadState, 'deleted');
  assert.equal(model.entry.uploadError, 'knowledge_recording_deleted');
  assert.equal(model.canRetryUpload(), false);
  assert.equal(platform.records.get(id).transcript, '你好');
  assert.equal(state.calls.length, 1);
});

test('local deletion serializes behind a pending journal save and blocks late resurrection', async () => {
  const { model, platform, state } = await opened();
  const id = await startAndStop(model);
  platform.transcribeResult = '删除竞态';
  const save = platform.blockNextUploadSave();
  const sync = model.transcribeAndUpload();
  await save.started.promise;
  assert.equal(model.canDeleteLocal(), true);
  const deletion = model.deleteLocal();
  save.release.resolve();
  assert.equal(await deletion, true);
  assert.equal(await sync, false);
  assert.equal(platform.records.has(id), false);
  assert.equal(model.entry, undefined);
  assert.equal(state.calls.length, 0);
});

test('a forgotten scope cannot retry its journal into another profile', async () => {
  const timeout = new Error('connection_lost'); timeout.status = 0; timeout.resultUnknown = true;
  const { model, platform, state } = await opened({ request: async () => { throw timeout; } });
  const id = await startAndStop(model);
  assert.equal(await model.transcribeAndUpload(), false);
  assert.equal(state.calls.length, 1);

  state.client.connected = false;
  state.client.profile.id = '';
  state.client.profile.hubId = '';
  state.client.profile.manager = '';
  state.generation++;
  assert.equal(await model.retryUpload(), false);
  assert.equal(state.calls.length, 1);

  const replacement = makeClient({ id: 'profile-2', hubId: 'hub-2', manager: 'manager-2' });
  await model.open(replacement.client, replacement.client.profile.manager, { filesDir: '/private' });
  assert.equal(model.entries.length, 0);
  assert.ok(platform.records.has(id));
  assert.deepEqual(replacement.calls, []);
});

test('previous recordings can be selected while idle, but selection and capture stay locked during upload', async () => {
  const { model, state } = await opened();
  const earlierId = await startAndStop(model);
  const latestId = await startAndStop(model);
  assert.equal(model.entries.length, 2);
  assert.equal(model.entry.id, latestId);
  assert.equal(model.canSelectEntry(), true);
  model.selectEntry(earlierId);
  assert.equal(model.entry.id, earlierId);
  model.selectEntry(latestId);

  const requestStarted = deferred(), requestGate = deferred();
  state.repository.transport.request = async (path, method, body) => {
    requestStarted.resolve();
    await requestGate.promise;
    const data = JSON.parse(body);
    return JSON.stringify({ id: path.slice('/api/v1/recordings/'.length), revision: data.revision, status: 'stored' });
  };
  const upload = model.transcribeAndUpload();
  await requestStarted.promise;
  assert.equal(model.uploadState, 'uploading');
  assert.equal(model.canStart(), false);
  assert.equal(model.canSelectEntry(), false);
  model.selectEntry(earlierId);
  assert.equal(model.entry.id, latestId);
  requestGate.resolve();
  assert.equal(await upload, true);
  assert.equal(model.canSelectEntry(), true);
  model.selectEntry(earlierId);
  assert.equal(model.entry.id, earlierId);
});

test('backgrounding stops capture and cancels an active transcription without syncing', async () => {
  const { model, platform, state } = await opened();
  assert.equal(await model.start(), true);
  const id = model.entry.id;
  await model.stopForBackground();
  assert.equal(model.captureState, 'stopped');
  assert.equal(platform.records.get(id).captureState, 'stopped');

  const gate = deferred(); platform.transcribeGate = gate;
  const transcription = model.transcribeAndUpload();
  for (let turn = 0; turn < 100 && platform.transcribeCalls.length === 0; turn++) await Promise.resolve();
  assert.equal(platform.transcribeCalls.length, 1);
  assert.equal(model.transcriptionState, 'transcribing');
  await model.stopForBackground();
  assert.equal(platform.cancelCalls, 1);
  assert.equal(model.transcriptionState, 'idle');
  assert.equal(platform.records.get(id).captureState, 'stopped');
  assert.deepEqual(state.calls, []);
  assert.equal(await transcription, false);
});

test('backgrounding during the transcription journal write does not start ASR afterward', async () => {
  const { model, platform, state } = await opened();
  const id = await startAndStop(model);
  const save = platform.blockNextTranscriptionSave();
  const transcription = model.transcribeAndUpload();
  await save.started.promise;
  await model.stopForBackground();
  save.release.resolve();
  assert.equal(await transcription, false);
  assert.equal(platform.transcribeCalls.length, 0);
  assert.equal(platform.records.get(id).transcriptionState, 'idle');
  assert.deepEqual(state.calls, []);
});

test('global background hook stops every registered capture while preserving its local file', async () => {
  const first = await opened(), second = await opened({ id: 'profile-2', hubId: 'hub-2', manager: 'manager-2' });
  assert.equal(await first.model.start(), true);
  assert.equal(await second.model.start(), true);
  const firstId = first.model.entry.id, secondId = second.model.entry.id;
  await KnowledgeRecording.backgroundAll();
  assert.equal(first.model.captureState, 'stopped');
  assert.equal(second.model.captureState, 'stopped');
  assert.equal(first.platform.records.get(firstId).captureState, 'stopped');
  assert.equal(second.platform.records.get(secondId).captureState, 'stopped');
  assert.deepEqual(first.state.calls, []);
  assert.deepEqual(second.state.calls, []);
});

test('forget retires matching models before pending saves finish and preserves other profiles', async () => {
  const forgotten = await opened(), other = await opened({ id: 'profile-2', hubId: 'hub-2', manager: 'manager-2' });
  await forgotten.model.start();
  const forgottenId = forgotten.model.entry.id;
  await forgotten.model.stop();
  await other.model.start();
  const otherId = other.model.entry.id;

  const save = forgotten.platform.blockNextUploadSave();
  const sync = forgotten.model.transcribeAndUpload();
  await save.started.promise;
  const forgetting = KnowledgeRecording.forgetProfile('profile-1', { filesDir: '/private' });
  assert.equal(forgotten.model.entry, undefined);
  assert.equal(other.model.entry.id, otherId);
  save.release.resolve();
  await forgetting;
  assert.equal(await sync, false);
  assert.equal(forgotten.platform.records.has(forgottenId), false);
  assert.equal(forgotten.state.calls.length, 0);
  assert.equal(other.platform.records.get(otherId).profileId, 'profile-2');
  assert.equal(other.model.captureState, 'recording');

  const repaired = newModel(forgotten.platform);
  await repaired.open(forgotten.state.client, forgotten.state.client.profile.manager, { filesDir: '/private' });
  assert.equal(repaired.entries.length, 0);
  await KnowledgeRecording.backgroundAll();
});

test('forget purges persisted files when no recording model was opened this launch', async () => {
  await disposeModels();
  recordingSdkFiles.clear(); recordingSdkDirectories.clear();
  const context = { filesDir: '/prior-session' }, directory = '/prior-session/knowledge-recordings';
  recordingSdkDirectories.add(directory);
  const forgottenId = '00000000-0000-4000-8000-000000000001';
  const otherId = '00000000-0000-4000-8000-000000000002';
  const manifest = (id, profileId) => JSON.stringify({ id, profileId, hubId: 'hub-' + profileId, managerId: 'manager-1' });
  recordingSdkFiles.set(directory + '/' + forgottenId + '.json', manifest(forgottenId, 'profile-1'));
  recordingSdkFiles.set(directory + '/' + forgottenId + '.pcm', 'profile 1 audio');
  recordingSdkFiles.set(directory + '/' + otherId + '.json', manifest(otherId, 'profile-2'));
  recordingSdkFiles.set(directory + '/' + otherId + '.pcm', 'profile 2 audio');

  await KnowledgeRecording.forgetProfile('profile-1', context);
  assert.equal(recordingSdkFiles.has(directory + '/' + forgottenId + '.json'), false);
  assert.equal(recordingSdkFiles.has(directory + '/' + forgottenId + '.pcm'), false);
  assert.equal(recordingSdkFiles.has(directory + '/' + otherId + '.json'), true);
  assert.equal(recordingSdkFiles.has(directory + '/' + otherId + '.pcm'), true);
});

test('local playback uses the selected capture and stops when the page backgrounds or the paired profile changes',async()=>{
  const {model,platform,state}=await opened();
  const id=await startAndStop(model);
  let admitted,ended,stops=0;
  platform.playLocal=async(entry,admit,done)=>{assert.equal(entry.id,id);admitted=admit;ended=done;};
  platform.stopPlayback=async()=>{stops++;};
  assert.equal(await model.playSelected(),true);assert.equal(admitted(),true);
  await model.stopForBackground();assert.equal(stops,1);assert.equal(admitted(),false);assert.equal(model.playingId,'');
  ended(true);assert.equal(model.error,'');
  assert.equal(await model.playSelected(),true);
  state.generation++;assert.equal(admitted(),false);
  await model.dispose();assert.equal(stops,2);
});

test('an invalid audio file reports a local playback error without requesting a recording upload',async()=>{
  const {model,platform,state}=await opened();
  await startAndStop(model);const before=state.calls.length;
  platform.playLocal=async()=>{throw new Error('invalid audio');};
  assert.equal(await model.playSelected(),false);assert.equal(model.playingId,'');
  assert.equal(model.error,'phone_record_play_failed');assert.equal(state.calls.length,before);
});
