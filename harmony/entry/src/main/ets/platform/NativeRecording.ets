import { meetingAudioNetwork } from './MeetingAudioNetwork';
import type {MeetingAudioConsent} from '../models/MeetingHubAudio';
import type { StreamNetworkRule } from './HubTransport';
import type { MeetingInputChoice } from '../models/PhoneMeetingOptions';
import type { MeetingJournal } from '../models/MeetingRoom';
import { abilityAccessCtrl, common, Permissions } from '@kit.AbilityKit';
import { audio } from '@kit.AudioKit';
import { speechRecognizer, textToSpeech } from '@kit.CoreSpeechKit';
import { fileIo } from '@kit.CoreFileKit';
import { util } from '@kit.ArkTS';
import type { KnowledgeRecordingEntry } from '../models/KnowledgeRecording';

const MICROPHONE: string = 'ohos.permission.MICROPHONE';
const SAMPLE_RATE: number = 16000;
const BYTES_PER_SECOND: number = SAMPLE_RATE * 2;
const MAX_RECORDING_SECONDS: number = 60 * 60;
const MAX_RECORDING_BYTES: number = BYTES_PER_SECOND * MAX_RECORDING_SECONDS;
const MAX_LOCAL_BYTES: number = 350 * 1024 * 1024;
const MAX_LOCAL_ENTRIES: number = 10;
const PCM_CHUNK_BYTES: number = 1280;

function utf8Length(value: string): number {
  let bytes = 0;
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    if (code < 0x80) bytes++;
    else if (code < 0x800) bytes += 2;
    else if (code >= 0xd800 && code <= 0xdbff && index + 1 < value.length && value.charCodeAt(index + 1) >= 0xdc00 && value.charCodeAt(index + 1) <= 0xdfff) {
      bytes += 4; index++;
    } else bytes += 3;
  }
  return bytes;
}

export async function requestRecordingMicrophone(context: Object): Promise<void> {
  const host = context as common.UIAbilityContext;
  const access = abilityAccessCtrl.createAtManager();
  let status = access.getSelfPermissionStatus(MICROPHONE as Permissions);
  if (status === abilityAccessCtrl.PermissionStatus.NOT_DETERMINED) {
    await access.requestPermissionsFromUser(host, [MICROPHONE] as Permissions[]);
    status = access.getSelfPermissionStatus(MICROPHONE as Permissions);
  }
  if (status === abilityAccessCtrl.PermissionStatus.DENIED || status === abilityAccessCtrl.PermissionStatus.RESTRICTED) {
    throw new Error('knowledge_recording_microphone_denied');
  }
  if (status !== abilityAccessCtrl.PermissionStatus.GRANTED) throw new Error('knowledge_recording_microphone_denied');
}

class NativeMeetingInput implements MeetingInputChoice {
  readonly id:number;readonly bluetooth:boolean;readonly name:string;
  constructor(device:audio.AudioDeviceDescriptor) {
    this.id=device.id;this.bluetooth=device.deviceType===audio.DeviceType.BLUETOOTH_SCO;this.name=device.name;
  }
}

export interface NativeRecordingStop {
  path: string;
  bytes: number;
  duration: number;
}

// Recognize from the same PCM frames saved by the recorder. Only completed utterances leave this recognizer.
export class NativeMeetingSpeech {
  private engine:speechRecognizer.SpeechRecognitionEngine|undefined;
  private session:string=util.generateRandomUUID();
  private ready:boolean=false;
  private closed:boolean=false;
  private frame:Uint8Array=new Uint8Array(640);
  private filled:number=0;
  private bytes:number=0;
  private queued:Uint8Array[]=[];
  private lastWrite:number=0;
  private pumpTimer:number=-1;
  private drain:()=>void=()=>{};
  private endedInput:boolean=false;
  private failedInput:boolean=false;
  private reportFailure:()=>void=()=>{};
  private lastEnd:number=0;
  private complete:()=>void=()=>{};
  private readonly done:Promise<void>;
  constructor() { this.done=new Promise<void>((resolve) => { this.complete=resolve; }); }

  async open(line:(start:number,end:number,text:string)=>void, unavailable:()=>void):Promise<void> {
    this.reportFailure=unavailable;
    const engine=await speechRecognizer.createEngine({language:'zh-CN',online:1,extraParams:{locate:'CN',recognizerMode:'long'}});
    if (this.closed) { engine.shutdown(); return; }
    this.engine=engine;
    let started:()=>void=()=>{};
    const start=new Promise<void>((resolve) => { started=resolve; });
    engine.setListener({
      onStart:(id:string):void => { if (id===this.session && !this.closed) { this.ready=true; started(); } },
      onEvent:(_id:string,_code:number,_message:string):void => {},
      onResult:(id:string,result:speechRecognizer.SpeechRecognitionResult):void => {
        if (id!==this.session || this.closed || !result.isFinal || !result.result.trim()) return;
        const end=this.bytes/BYTES_PER_SECOND; line(this.lastEnd,end,result.result.trim()); this.lastEnd=end;
      },
      onComplete:(id:string):void => { if (id===this.session) this.complete(); },
      onError:(id:string,_code:number,_message:string):void => {
        if (id!==this.session || this.closed) return;
        this.failedInput=true; this.ready=false; started(); this.complete(); this.drain(); unavailable();
      }
    });
    engine.startListening({sessionId:this.session,audioInfo:{audioType:'pcm',sampleRate:SAMPLE_RATE,soundChannel:1,sampleBit:16},
      extraParams:{recognitionMode:1,maxAudioDuration:3600040}});
    let timer:number=-1;
    try { await Promise.race([start,new Promise<void>((resolve)=>{timer=setTimeout(resolve,8000);})]); }
    finally { clearTimeout(timer); }
    if (!this.ready && !this.closed) { this.release(); throw new Error('phone_dictation_unavailable'); }
  }
  private pump():void {
    clearTimeout(this.pumpTimer);
    if (this.closed || this.failedInput || !this.engine || !this.ready) { this.drain(); return; }
    const remaining=20-(Date.now()-this.lastWrite);
    if (remaining>0) { this.pumpTimer=setTimeout(() => this.pump(),remaining); return; }
    if (!this.queued.length) { this.drain(); return; }
    try {
      const frame=this.queued.shift()!;
      this.engine.writeAudio(this.session,frame);
      this.bytes+=frame.byteLength;
      this.lastWrite=Date.now();
      this.pumpTimer=setTimeout(() => this.pump(),20);
    } catch (_) {
      this.failedInput=true; this.queued=[]; this.drain(); this.reportFailure();
    }
  }
  feed(buffer:ArrayBuffer):void {
    if (!this.ready || !this.engine || this.closed || this.endedInput || this.failedInput) return;
    const input=new Uint8Array(buffer);
    let offset=0;
    while (offset<input.length) {
      const count=Math.min(this.frame.length-this.filled,input.length-offset);
      this.frame.set(input.subarray(offset,offset+count),this.filled); this.filled+=count; offset+=count;
      if (this.filled===this.frame.length) {
        this.queued.push(this.frame); this.frame=new Uint8Array(640); this.filled=0;
      }
    }
    if (this.queued.length>240) { this.failedInput=true; this.queued=[]; this.reportFailure(); this.drain(); return; }
    this.pump();
  }
  async finish():Promise<void> {
    if (!this.engine || this.closed) { this.release(); return; }
    let timer:number=-1;
    try {
      this.endedInput=true;
      if (this.ready && this.filled) { this.queued.push(this.frame); this.filled=0; }
      const drained=new Promise<void>((resolve)=>{this.drain=resolve;});
      this.pump();
      await drained;
      if (this.closed || this.failedInput) return;
      this.engine.finish(this.session);
      await Promise.race([this.done,new Promise<void>((resolve)=>{timer=setTimeout(resolve,3000);})]);
    } catch (_) {} finally { clearTimeout(timer); this.release(); }
  }
  release():void {
    this.closed=true; this.ready=false; clearTimeout(this.pumpTimer); this.queued=[]; this.drain(); this.complete();
    const engine=this.engine; this.engine=undefined;
    if (engine) { try { engine.cancel(this.session); } catch (_) {} try { engine.shutdown(); } catch (_) {} }
  }
}

class FinalSpeechFrameError extends Error {
  constructor() { super('knowledge_recording_transcribe_failed'); }
}

/** App-private bounded PCM storage and foreground-only native audio / offline ASR. */
export class NativeRecording {
  private context: common.UIAbilityContext | undefined;
  private directory: string = '';
  private capturer: audio.AudioCapturer | undefined;
  private readListener: ((data: ArrayBuffer) => void) | undefined;
  private fileDescriptor: number = -1;
  private activeId: string = '';
  private activePath: string = '';
  private activeBytes: number = 0;
  private readFailed: boolean = false;
  private roomSpeech: NativeMeetingSpeech | undefined;
  private speechFailed: (()=>void) | undefined;
  private voiceStop: (()=>void) | undefined;
  private silenceUntil:number=0;
  private voiceEpoch:number=0;
  private echoActive:boolean=false;
  private changingSource:Promise<boolean>|undefined;
  private onCaptureFailure:((reason:string)=>void)|undefined;

  static supportsEchoCancellation():boolean {
    try { return audio.getAudioManager().getStreamManager().isAcousticEchoCancelerSupported(audio.SourceType.SOURCE_TYPE_VOICE_COMMUNICATION); }
    catch (_) { return false; }
  }

  private selectedInput:number=-1;
  static async meetingLanguages():Promise<string[]> {
    const engine=await speechRecognizer.createEngine({language:'zh-CN',online:1,extraParams:{locate:'CN',recognizerMode:'long'}});
    try { return await engine.listLanguages({sessionId:util.generateRandomUUID(),extraParams:{online:1}}); }
    finally { engine.shutdown(); }
  }
  private static inputDevices():audio.AudioDeviceDescriptors {
    try {
      const manager=audio.getAudioManager();
      if (typeof manager.getDeviceEnhanceManager!=='function' ||
        typeof manager.getDeviceEnhanceManager().selectInputDeviceForAudioCapturer!=='function') return [];
      const routes=manager.getRoutingManager();
      const all=routes.getAvailableDevices(audio.DeviceUsage.MEDIA_INPUT_DEVICES).concat(routes.getAvailableDevices(audio.DeviceUsage.CALL_INPUT_DEVICES));
      return all.filter((device:audio.AudioDeviceDescriptor,index:number) => (device.deviceType===audio.DeviceType.MIC ||
        device.deviceType===audio.DeviceType.BLUETOOTH_SCO) && all.findIndex((other:audio.AudioDeviceDescriptor)=>other.id===device.id)===index);
    } catch (_) { return []; }
  }
  static meetingInputs():MeetingInputChoice[] {
    const devices=NativeRecording.inputDevices();
    if (!devices.some((d:audio.AudioDeviceDescriptor)=>d.deviceType===audio.DeviceType.BLUETOOTH_SCO)) return [];
    return devices.map((d:audio.AudioDeviceDescriptor)=>new NativeMeetingInput(d));
  }
  private async openCapturer(withEcho:boolean):Promise<audio.AudioCapturer> {
    const chosen=this.selectedInput;
    const target=chosen<0 ? undefined : NativeRecording.inputDevices().find((d:audio.AudioDeviceDescriptor)=>d.id===chosen);
    if (chosen>=0 && !target) throw new Error('meeting_input_changed');
    const capturer=await audio.createAudioCapturer({
      streamInfo:{samplingRate:SAMPLE_RATE,channels:audio.AudioChannel.CHANNEL_1,sampleFormat:audio.AudioSampleFormat.SAMPLE_FORMAT_S16LE,
        encodingType:audio.AudioEncodingType.ENCODING_TYPE_RAW},
      capturerInfo:{source:withEcho ? audio.SourceType.SOURCE_TYPE_VOICE_COMMUNICATION : audio.SourceType.SOURCE_TYPE_MIC,capturerFlags:0}
    });
    if (target) {
      try {
        await audio.getAudioManager().getDeviceEnhanceManager().selectInputDeviceForAudioCapturer(capturer,target);
        if (!capturer.getCurrentInputDevices().some((d:audio.AudioDeviceDescriptor)=>d.id===chosen)) throw new Error('meeting_input_changed');
      } catch (_) { await capturer.release();throw new Error('meeting_input_changed'); }
    }
    return capturer;
  }

  private async releaseCapturer(capturer:audio.AudioCapturer,listener?:((data:ArrayBuffer)=>void)):Promise<void> {
    if (listener) { try { capturer.off('readData',listener); } catch (_) {} }
    try { if (capturer.state===audio.AudioState.STATE_RUNNING) await capturer.stop(); } catch (_) {}
    try { await capturer.release(); } catch (_) {}
  }

  stopRoomVoice():void { this.voiceEpoch++; this.voiceStop?.(); }

  /** The PCM file and recognizer stay open while the system replaces its capture source. */
  async enableSpeakerEcho(id:string):Promise<boolean> {
    if (!id || id!==this.activeId || this.fileDescriptor<0) return false;
    if (this.echoActive) return true;
    if (this.changingSource) return this.changingSource;
    if (!NativeRecording.supportsEchoCancellation() || !this.capturer || !this.readListener) return false;
    const gen=this.generation,listener=this.readListener,old=this.capturer;
    this.capturer=undefined;
    const run=this.changeCaptureSource(gen,old,listener);
    this.changingSource=run;
    try { return await run; } finally { if (this.changingSource===run) this.changingSource=undefined; }
  }

  private async changeCaptureSource(gen:number,old:audio.AudioCapturer,listener:(data:ArrayBuffer)=>void):Promise<boolean> {
    await this.releaseCapturer(old,listener);
    for (const echo of [true,false]) {
      if (gen!==this.generation) return false;
      let replacement:audio.AudioCapturer|undefined;
      try {
        replacement=await this.openCapturer(echo);
        if (gen!==this.generation) { await this.releaseCapturer(replacement); return false; }
        replacement.on('readData',listener); await replacement.start();
        if (gen!==this.generation) { await this.releaseCapturer(replacement,listener); return false; }
        this.capturer=replacement; this.echoActive=echo; return echo;
      } catch (_) { if (replacement) await this.releaseCapturer(replacement,listener); }
    }
    if (gen===this.generation) this.onCaptureFailure?.('recording_start_failed');
    return false;
  }

  async speakRoom(id:string,text:string,language:string,admit:()=>boolean):Promise<void> {
    this.voiceStop?.();
    const generation=++this.voiceEpoch;
    if (!admit()) return;
    const english=language.toLowerCase().startsWith('en');
    if (!english && !language.toLowerCase().startsWith('zh')) throw new Error('phone_meeting_voice_unavailable');
    const engine=await textToSpeech.createEngine({language:english?'en-US':'zh-CN',person:english?8:13,online:1});
    if (!admit() || generation!==this.voiceEpoch) { engine.shutdown(); return; }
    let done:()=>void=()=>{}, failed=false, timer:number=-1;
    const complete=new Promise<void>((resolve)=>{done=resolve;});
    const stop=():void=>{try{engine.stop();}catch(_){} done();};
    try {
      engine.setListener({
        onStart:(requestId:string,_response:textToSpeech.StartResponse):void => {},
        onComplete:(requestId:string,_response:textToSpeech.CompleteResponse):void => { if(requestId===id)done(); },
        onStop:(requestId:string,_response:textToSpeech.StopResponse):void => { if(requestId===id)done(); },
        onError:(requestId:string,_code:number,_message:string):void=>{if(requestId===id){failed=true;done();}}
      });
      this.voiceStop=stop; this.silenceUntil=Number.MAX_SAFE_INTEGER;
      engine.speak(text.slice(0,4096),{requestId:id});
      await Promise.race([complete,new Promise<void>((resolve)=>{timer=setTimeout(()=>{failed=true;resolve();},90000);})]);
    } finally {
      clearTimeout(timer);
      if (this.voiceStop===stop) {this.voiceStop=undefined;this.silenceUntil=Date.now()+350;}
      try {engine.stop();} catch (_) {} try {engine.shutdown();} catch (_) {}
    }
    if (failed) throw new Error('phone_meeting_voice_unavailable');
  }

  async startRoomSpeech(line:(from:number,to:number,text:string)=>void,failed:()=>void):Promise<void> {
    this.roomSpeech?.release();
    const speech=new NativeMeetingSpeech(); this.roomSpeech=speech; this.speechFailed=failed;
    try { await speech.open(line,failed); }
    catch (_) { if (this.roomSpeech===speech) { speech.release(); this.roomSpeech=undefined; failed(); } }
  }

  private generation: number = 0;
  private stopping: Promise<NativeRecordingStop> | undefined;
  private asr: speechRecognizer.SpeechRecognitionEngine | undefined;
  private asrSession: string = '';
  private cancelAsr: boolean = false;
  private asrCompletion: ((error?: Error) => void) | undefined;
  private asrPending: Promise<string> | undefined;
  private playbackGeneration: number = 0;
  private releasePlayer: (() => Promise<void>) | undefined;

  /** Purges a forgotten profile even when no Knowledge recording model was opened this launch. */
  static async purgeProfile(context: Object, profileId: string): Promise<void> {
    const platform = new NativeRecording();
    await platform.initialize(context);
    await platform.forgetProfile(profileId);
  }

  async initialize(context: Object): Promise<void> {
    const host = context as common.UIAbilityContext;
    if (!host.filesDir) throw new Error('storage_unavailable');
    this.context = host;
    this.directory = host.filesDir + '/knowledge-recordings';
    try { fileIo.statSync(this.directory); } catch (_) { fileIo.mkdirSync(this.directory, true); }
    try {await meetingAudioNetwork.initialize(host);}catch(_){}
  }

  async ensureMicrophonePermission(context: Object): Promise<void> { await requestRecordingMicrophone(context); }

  async start(context: Object, id: string, onLimit: (reason: string) => void,participant:boolean=false,onSamples?:((samples:number)=>void),inputId:number=-1): Promise<string> {
    if (this.capturer || this.fileDescriptor >= 0 || !this.directory) throw new Error('recording_busy');
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)) throw new Error('invalid_recording_id');
    const generation = ++this.generation;
    this.readFailed = false;
    await this.ensureMicrophonePermission(context);
    if (generation !== this.generation) throw new Error('request_updated');
    this.ensureCapacity();
    const path = this.audioPath(id);
    const output = fileIo.openSync(path, fileIo.OpenMode.CREATE | fileIo.OpenMode.WRITE_ONLY | fileIo.OpenMode.TRUNC);
    this.selectedInput=inputId;this.fileDescriptor = output.fd; this.activeId = id.toLowerCase(); this.activePath = path; this.activeBytes = 0;
    try {
      this.echoActive=participant && NativeRecording.supportsEchoCancellation(); this.onCaptureFailure=onLimit;
      let capturer:audio.AudioCapturer;
      try { capturer=await this.openCapturer(this.echoActive); }
      catch (failure) {
        if (!this.echoActive || generation!==this.generation) throw failure instanceof Error ? failure : new Error('recording_start_failed');
        this.echoActive=false; capturer=await this.openCapturer(false);
      }
      if (generation !== this.generation) { await capturer.release(); throw new Error('request_updated'); }
      this.capturer = capturer;
      const listener = (data: ArrayBuffer): void => {
        if (generation !== this.generation || this.fileDescriptor < 0 || this.readFailed) return;
        const bytes = data.byteLength;
        if (!bytes || bytes % 2 !== 0) { this.readFailed = true; onLimit('recording_write_failed'); return; }
        if (this.activeBytes + bytes > MAX_RECORDING_BYTES) { this.readFailed = true; onLimit('recording_limit'); return; }
        try {
          const written = fileIo.writeSync(this.fileDescriptor, data);
          if (written !== bytes) { this.readFailed = true; onLimit('recording_write_failed'); return; }
          this.activeBytes += written;
          if (onSamples) { try { onSamples(Math.floor(this.activeBytes/2)); } catch (_) {} }
          if (this.roomSpeech) {
            try { this.roomSpeech.feed(this.voiceStop || Date.now()<this.silenceUntil ? new ArrayBuffer(data.byteLength) : data); }
            catch (_) { this.roomSpeech.release(); this.roomSpeech=undefined; this.speechFailed?.(); }
          }
        } catch (_) { this.readFailed = true; onLimit('recording_write_failed'); }
      };
      this.readListener = listener;
      capturer.on('readData', listener);
      await capturer.start();
      if (generation !== this.generation) {
        const stopped = await this.stop();
        const failure = new Error('request_updated');
        (failure as Object as Record<string, Object>)['recordingStop'] = stopped as Object;
        throw failure;
      }
      return path;
    } catch (error) {
      const failure = error instanceof Error ? error : new Error('recording_start_failed');
      const fields = failure as Object as Record<string, Object>;
      let stopped = fields['recordingStop'] ? fields['recordingStop'] as Object as NativeRecordingStop : undefined;
      if (!stopped) {
        try { stopped = await this.stop(); }
        catch (_) { stopped = { path, bytes: this.activeBytes, duration: this.activeBytes / BYTES_PER_SECOND }; }
      }
      fields['recordingStop'] = stopped as Object;
      throw failure;
    }
  }

  async stop(): Promise<NativeRecordingStop> {
    this.stopRoomVoice();
    if (this.stopping) return this.stopping;
    const path = this.activePath, fd = this.fileDescriptor, bytes = this.activeBytes;
    const capturer = this.capturer, listener = this.readListener, speech=this.roomSpeech;
    const sourceChange=this.changingSource; this.echoActive=false; this.onCaptureFailure=undefined;
    this.roomSpeech=undefined; this.speechFailed=undefined;
    this.generation++;
    this.capturer = undefined; this.readListener = undefined; this.fileDescriptor = -1;
    this.activeId = ''; this.activePath = ''; this.activeBytes = 0;
    const result: NativeRecordingStop = { path, bytes, duration: bytes / BYTES_PER_SECOND };
    this.stopping = (async (): Promise<NativeRecordingStop> => {
      if (capturer) await this.releaseCapturer(capturer,listener);
      if (sourceChange) await sourceChange.catch(()=>false);
      if (speech) await speech.finish();
      if (fd >= 0) {
        try { fileIo.fsyncSync(fd); } catch (_) { }
        try { fileIo.closeSync(fd); } catch (_) { }
      }
      return result;
    })();
    try { return await this.stopping; } finally { this.stopping = undefined; }
  }

  async save(entry: KnowledgeRecordingEntry): Promise<void> {
    this.requireReady();
    if (!this.validId(entry.id) || JSON.stringify(entry).length > 5 * 1024 * 1024) throw new Error('storage_unavailable');
    this.writeDocument(this.manifestPath(entry.id),JSON.stringify(entry));
  }

  saveRoom(journal:MeetingJournal):void {
    const raw=JSON.stringify(journal);
    if (raw.length>6*1024*1024) throw new Error('storage_unavailable');
    this.writeDocument(this.roomPath(journal.id),raw);
  }
  loadRoom(id:string,profile:string,hub:string,manager:string):MeetingJournal|undefined {
    const path=this.roomPath(id);
    if (!fileIo.accessSync(path)) return undefined;
    const journal=JSON.parse(fileIo.readTextSync(path)) as MeetingJournal;
    if (journal.id!==id || journal.profileId!==profile || journal.hubId!==hub || journal.managerId!==manager ||
      !((journal.transcription==='hub' || journal.privateMark===true) && journal.role==='off') && !['listener','participant'].includes(journal.role) ||
      !Array.isArray(journal.lines) || !Array.isArray(journal.asks) || !Number.isSafeInteger(journal.next) || journal.next<0 ||
      journal.effectiveTranscription!=='hub' && journal.next>journal.lines.length) throw new Error('request_updated');
    return journal;
  }
  private roomPath(id:string):string { return this.manifestPath(id).replace(/\.json$/,'.room.json'); }
  private consentPath(id:string):string {return this.manifestPath(id).replace(/\.json$/,'.consent.json');}
  saveMeetingConsent(grant:MeetingAudioConsent):void {this.writeDocument(this.consentPath(grant.id),JSON.stringify(grant));}
  meetingConsent(id:string):MeetingAudioConsent|undefined {
    const path=this.consentPath(id);
    if(!fileIo.accessSync(path))return undefined;
    const data=JSON.parse(fileIo.readTextSync(path)) as MeetingAudioConsent;
    if(data.id!==id || typeof data.wifiOnly!=='boolean' || typeof data.serviceRef!=='string' || typeof data.serviceName!=='string' ||
      typeof data.credentialKey!=='string' || !/^[A-Za-z0-9_-]{43}$/.test(data.credentialKey))return undefined;
    return data;
  }
  clearMeetingConsent(id:string):void {
    const path=this.consentPath(id);
    if(fileIo.accessSync(path)){
      try{fileIo.unlinkSync(path);}catch(_){this.writeDocument(path,'{}');}
    }
  }
  private writeDocument(destination:string,content:string):void {
    const temporary = destination + '.tmp';
    const file = fileIo.openSync(temporary, fileIo.OpenMode.CREATE | fileIo.OpenMode.WRITE_ONLY | fileIo.OpenMode.TRUNC);
    let open = true;
    try {
      if (fileIo.writeSync(file.fd, content) !== utf8Length(content)) throw new Error('storage_unavailable');
      fileIo.fsyncSync(file.fd);
      fileIo.closeSync(file.fd); open = false;
      fileIo.renameSync(temporary, destination);
    } catch (error) {
      if (open) { try { fileIo.closeSync(file.fd); } catch (_) { } }
      try { fileIo.unlinkSync(temporary); } catch (_) { }
      throw error as Error;
    }
  }

  async load(profileId: string, hubId: string, managerId: string): Promise<KnowledgeRecordingEntry[]> {
    this.requireReady();
    const entries: KnowledgeRecordingEntry[] = [];
    for (const name of fileIo.listFileSync(this.directory)) {
      if (!/^[0-9a-f-]{36}\.json$/i.test(name)) continue;
      let entry: KnowledgeRecordingEntry;
      try { entry = JSON.parse(fileIo.readTextSync(this.directory + '/' + name)) as KnowledgeRecordingEntry; }
      catch (_) { continue; }
      if (!this.validId(entry.id) || entry.profileId !== profileId || entry.hubId !== hubId || entry.managerId !== managerId) continue;
      if (entry.captureState === 'recording') {
        const path = this.audioPath(entry.id);
        let bytes = 0;
        try { bytes = fileIo.statSync(path).size; } catch (_) { }
        if (bytes > 0 && bytes <= MAX_RECORDING_BYTES) {
          entry.audioBytes = bytes; entry.duration = bytes / BYTES_PER_SECOND; entry.captureState = 'stopped';
          entry.endedAt = new Date().toISOString(); entry.interrupted = true;
          await this.save(entry);
        } else {
          try { fileIo.unlinkSync(path); } catch (_) { }
          try { fileIo.unlinkSync(this.manifestPath(entry.id)); } catch (_) { }
          continue;
        }
      }
      entries.push(entry);
    }
    return entries.sort((left: KnowledgeRecordingEntry, right: KnowledgeRecordingEntry) => right.startedAt.localeCompare(left.startedAt));
  }

  meetingNetworkKey(wifiOnly:boolean):string {return meetingAudioNetwork.key(wifiOnly);}
  meetingNetworkBinding(key:string,wifiOnly:boolean,admit:()=>boolean=()=>true):StreamNetworkRule {return meetingAudioNetwork.binding(key,wifiOnly,admit);}
  async readMeetingPCM(id:string,from:number,samples:number):Promise<ArrayBuffer> {
    if(!this.validId(id) || !Number.isSafeInteger(from) || !Number.isSafeInteger(samples) || from<0 || samples<1 || samples>32000) throw new Error('recording_file_read_failed');
    const file=fileIo.openSync(this.audioPath(id),fileIo.OpenMode.READ_ONLY);
    try{
      const length=samples*2,offset=from*2;
      if(offset+length>fileIo.statSync(file.fd).size)throw new Error('recording_file_read_failed');
      const buffer=new ArrayBuffer(length);
      if(fileIo.readSync(file.fd,buffer,{offset:offset,length:length})!==length)throw new Error('recording_file_read_failed');
      return buffer;
    }finally{fileIo.closeSync(file);}
  }

  /** Plays an already captured app-private PCM file. No URL, microphone or Hub credential is involved. */
  async playLocal(entry: KnowledgeRecordingEntry, admit: () => boolean, done: (failed: boolean) => void): Promise<void> {
    const generation = ++this.playbackGeneration;
    const previous = this.releasePlayer; this.releasePlayer = undefined;
    if (previous) await previous();
    if (!admit() || generation !== this.playbackGeneration) throw new Error('request_updated');
    const stored = JSON.parse(fileIo.readTextSync(this.manifestPath(entry.id))) as KnowledgeRecordingEntry;
    if (stored.id !== entry.id || stored.hubId !== entry.hubId || stored.profileId !== entry.profileId ||
      stored.managerId !== entry.managerId || stored.captureState !== 'stopped' || stored.audioBytes !== entry.audioBytes)
      throw new Error('request_updated');
    const path = this.audioPath(entry.id), size = fileIo.statSync(path).size;
    if (size < 2 || size > MAX_RECORDING_BYTES || size !== entry.audioBytes || size % 2 !== 0)
      throw new Error('phone_record_play_failed');
    const file = fileIo.openSync(path, fileIo.OpenMode.READ_ONLY);
    let renderer: audio.AudioRenderer | undefined, closed = false, remaining = size, finishing = false;
    const release = async (): Promise<void> => {
      if (closed) return;
      closed = true;
      try { fileIo.closeSync(file); } catch (_) { }
      const device = renderer; renderer = undefined;
      if (!device) return;
      try { device.off('writeData'); device.off('markReach'); } catch (_) { }
      try { if (device.state === audio.AudioState.STATE_RUNNING) await device.stop(); } catch (_) { }
      try { await device.release(); } catch (_) { }
    };
    const finish = (failed:boolean): void => {
      if (finishing || closed) return;
      finishing = true;
      setTimeout(() => {
        if (generation !== this.playbackGeneration) return;
        this.releasePlayer = undefined;
        release().finally(() => { if (generation === this.playbackGeneration) done(failed); });
      }, 0);
    };
    this.releasePlayer = release;
    try {
      const device = await audio.createAudioRenderer({
        streamInfo: {samplingRate:SAMPLE_RATE,channels:audio.AudioChannel.CHANNEL_1,
          sampleFormat:audio.AudioSampleFormat.SAMPLE_FORMAT_S16LE,encodingType:audio.AudioEncodingType.ENCODING_TYPE_RAW},
        rendererInfo: {usage:audio.StreamUsage.STREAM_USAGE_MUSIC,rendererFlags:0}
      });
      if (closed || !admit() || generation !== this.playbackGeneration) { await device.release(); throw new Error('request_updated'); }
      renderer = device;
      device.on('markReach', size / 2, () => { if (!remaining) finish(false); });
      device.on('writeData', (buffer:ArrayBuffer):audio.AudioDataCallbackResult => {
        if (closed || finishing || !admit() || generation !== this.playbackGeneration) { finish(false); return audio.AudioDataCallbackResult.INVALID; }
        if (!remaining) return audio.AudioDataCallbackResult.INVALID;
        try {
          const chunk = Math.min(remaining,buffer.byteLength);
          if (chunk !== buffer.byteLength) new Uint8Array(buffer).fill(0);
          const read = fileIo.readSync(file.fd,buffer,{length:chunk});
          if (read !== chunk || !read) { finish(true); return audio.AudioDataCallbackResult.INVALID; }
          remaining -= read;
          if (!remaining) setTimeout(() => {
            if (closed || finishing || generation !== this.playbackGeneration) return;
            device.drain().then(() => finish(false)).catch(() => finish(true));
          },0);
          return audio.AudioDataCallbackResult.VALID;
        } catch (_) { finish(true); return audio.AudioDataCallbackResult.INVALID; }
      });
      await device.start();
      if (!admit() || generation !== this.playbackGeneration) throw new Error('request_updated');
    } catch (error) {
      if (this.releasePlayer === release) this.releasePlayer = undefined;
      await release(); throw error as Error;
    }
  }

  async stopPlayback(): Promise<void> {
    this.playbackGeneration++;
    const release = this.releasePlayer; this.releasePlayer = undefined;
    if (release) await release();
  }

  async transcribe(id: string, language: string): Promise<string> {
    this.requireReady();
    if (language !== 'zh-CN') throw new Error('knowledge_recording_chinese_only');
    const path = this.audioPath(id);
    let size = 0;
    try { size = fileIo.statSync(path).size; } catch (_) { throw new Error('recording_file_missing'); }
    if (size < 16000) throw new Error('knowledge_recording_too_short');
    if (size > MAX_RECORDING_BYTES || size % 2 !== 0) throw new Error('recording_invalid_size');
    if (this.asrPending) throw new Error('transcription_busy');
    this.cancelAsr = false;
    const operation = this.transcribeSavedPcm(path, size);
    this.asrPending = operation;
    try { return await operation; } finally { if (this.asrPending === operation) this.asrPending = undefined; }
  }

  async cancelTranscription(): Promise<void> {
    this.cancelAsr = true;
    const engine = this.asr, session = this.asrSession;
    if (engine && session) { try { engine.cancel(session); } catch (_) { } }
    this.asrCompletion?.(new Error('transcription_cancelled'));
    if (engine) await this.asrPending?.catch(() => undefined);
  }

  async remove(id: string): Promise<void> {
    this.requireReady();
    if (!this.validId(id) || id === this.activeId) throw new Error('recording_busy');
    for (const path of [this.audioPath(id),this.manifestPath(id),this.roomPath(id),this.roomPath(id)+'.tmp',this.consentPath(id),this.consentPath(id)+'.tmp']) {
      try { fileIo.unlinkSync(path); } catch (_) { }
    }
  }

  /** Removes only local recordings whose manifest belongs to this Hub profile. */
  async forgetProfile(profileId: string): Promise<void> {
    if (!profileId || !this.directory || !this.context) return;
    for (const name of fileIo.listFileSync(this.directory)) {
      const temporary = name.endsWith('.json.tmp');
      const manifestName = temporary ? name.slice(0, -4) : name;
      if (!/^[0-9a-f-]{36}(?:\.(?:room|consent))?\.json$/i.test(manifestName)) continue;
      const path = this.directory + '/' + name;
      let entry: KnowledgeRecordingEntry;
      try { entry = JSON.parse(fileIo.readTextSync(path)) as KnowledgeRecordingEntry; } catch (_) { continue; }
      const id = manifestName.slice(0,36);
      if (!this.validId(id) || !entry || typeof entry.id !== 'string' || entry.id.toLowerCase() !== id.toLowerCase() || entry.profileId !== profileId) continue;
      if (this.activeId.toLowerCase() === id.toLowerCase()) await this.stop();
      for (const ownedPath of [this.audioPath(id),this.manifestPath(id),this.manifestPath(id)+'.tmp',this.roomPath(id),this.roomPath(id)+'.tmp',this.consentPath(id),this.consentPath(id)+'.tmp']) {
        try { fileIo.unlinkSync(ownedPath); } catch (_) { }
      }
    }
  }

  private async transcribeSavedPcm(path:string,size:number):Promise<string> {
    try { return await this.runTranscription(path,size,PCM_CHUNK_BYTES); }
    catch (error) {
      // SpeechKit can reject finish after receiving every frame, even after producing partial text.
      // A fresh session reads the original file. Only its complete result is returned.
      if (!(error instanceof FinalSpeechFrameError) || this.cancelAsr) throw error as Error;
      return this.runTranscription(path,size,640);
    }
  }

  private async runTranscription(path: string, size: number, frameBytes:number): Promise<string> {
    const engine = await speechRecognizer.createEngine({
      language: 'zh-CN', online: 1, extraParams: { locate: 'CN', recognizerMode: 'long' }
    });
    const session = util.generateRandomUUID(); this.asr = engine; this.asrSession = session;
    let finished = false, allFramesSent=false, failed: Error | undefined, latest = '', finals: string[] = [];
    let started = false, notifyStarted: () => void = (): void => {};
    const startReady = new Promise<void>((resolve) => { notifyStarted = resolve; });
    let complete: () => void = (): void => {};
    const completion = new Promise<void>((resolve) => { complete = resolve; });
    const settle = (error?: Error): void => { if (finished) return; finished = true; failed = error; complete(); };
    this.asrCompletion = settle;
    engine.setListener({
      onStart: (_session: string, _message: string): void => {
        if (_session !== session || this.asr !== engine || this.asrSession !== session || finished || this.cancelAsr) return;
        started = true; notifyStarted();
      },
      onEvent: (_session: string, _code: number, _message: string): void => {},
      onResult: (_session: string, result: speechRecognizer.SpeechRecognitionResult): void => {
        if (_session !== session || this.asr !== engine || this.asrSession !== session || finished || this.cancelAsr) return;
        const text = result.result.trim();
        if (!text) return;
        latest = text;
        if (result.isFinal) finals.push(text);
      },
      onComplete: (_session: string, _message: string): void => { if (_session === session && this.asr === engine && this.asrSession === session) settle(); },
      onError: (_session: string, code: number, _message: string): void => {
        if (_session !== session || this.asr !== engine || this.asrSession !== session || finished) return;
        failed = code===1002200010 && allFramesSent && !this.cancelAsr ? new FinalSpeechFrameError() :
          new Error(code === 1002200012 ? 'knowledge_recording_microphone_denied' : 'knowledge_recording_transcribe_failed');
        settle(failed);
      }
    });
    try {
      if (this.cancelAsr) throw new Error('transcription_cancelled');
      engine.startListening({ sessionId: session, audioInfo: { audioType: 'pcm', sampleRate: SAMPLE_RATE, soundChannel: 1, sampleBit: 16 },
        // The engine rejects the frame reaching its limit. Allow one padded PCM
        // frame beyond the file so explicit finish, rather than the limit, ends it.
        extraParams: { recognitionMode: 1, maxAudioDuration: Math.max(20000, Math.ceil(size / BYTES_PER_SECOND * 1000) + 40) } });
      let startTimer: number = -1;
      try {
        await Promise.race([startReady, completion, new Promise<void>((_, reject): void => {
          startTimer = setTimeout((): void => reject(new Error('knowledge_recording_transcribe_failed')), 10000);
        })]);
      } finally { if (startTimer !== -1) clearTimeout(startTimer); }
      if (this.cancelAsr) throw new Error('transcription_cancelled');
      if (failed) throw failed as Error;
      if (!started || finished) throw new Error('knowledge_recording_transcribe_failed');
      const input = fileIo.openSync(path, fileIo.OpenMode.READ_ONLY);
      try {
        let offset = 0;
        while (offset < size) {
          if (this.cancelAsr) throw new Error('transcription_cancelled');
          if (failed) throw failed as Error;
          if (finished) throw new Error('knowledge_recording_transcribe_failed');
          const length = Math.min(frameBytes, size - offset);
          const source = new ArrayBuffer(length);
          const read = fileIo.readSync(input.fd, source, { length: length });
          if (read !== length) throw new Error('recording_file_read_failed');
          let send = new Uint8Array(source);
          if (send.length < 640) { const padded = new Uint8Array(640); padded.set(send); send = padded; }
          else if (send.length !== 640 && send.length !== 1280) { const padded = new Uint8Array(1280); padded.set(send); send = padded; }
          allFramesSent=offset+read===size;
          engine.writeAudio(session, send);
          offset += read;
          if (failed) throw failed as Error;
          if (finished && offset < size) throw new Error('knowledge_recording_transcribe_failed');
          // writeAudio queues PCM. Keep its 20/40 ms cadence through the final padded frame before finish.
          if (!finished) await new Promise<void>((resolve) => setTimeout(resolve, send.length === 640 ? 20 : 40));
        }
      } finally { fileIo.closeSync(input.fd); }
      if (this.cancelAsr) throw new Error('transcription_cancelled');
      if (failed) throw failed as Error;
      if (!finished) engine.finish(session);
      let timer: number = -1;
      try {
        await Promise.race([completion, new Promise<void>((_, reject): void => {
          timer = setTimeout((): void => reject(new Error('knowledge_recording_transcribe_failed')), 30000);
        })]);
      } finally { if (timer !== -1) clearTimeout(timer); }
      if (this.cancelAsr) throw new Error('transcription_cancelled');
      if (failed) throw failed as Error;
      const text = (finals.length ? finals.join('') : latest).trim();
      if (!text) throw new Error('knowledge_recording_transcribe_failed');
      return text;
    } catch (error) {
      if (error instanceof Error && error.message === 'transcription_cancelled') {
        try { engine.cancel(session); } catch (_) { }
        throw error;
      }
      try { engine.cancel(session); } catch (_) { }
      throw failed || error as Error;
    } finally {
      settle(failed || new Error('transcription_cancelled'));
      try { engine.shutdown(); } catch (_) { }
      if (this.asr === engine) this.asr = undefined;
      if (this.asrSession === session) this.asrSession = '';
      if (this.asrCompletion === settle) this.asrCompletion = undefined;
    }
  }

  private ensureCapacity(): void {
    let entries = 0, bytes = 0;
    for (const name of fileIo.listFileSync(this.directory)) {
      if (/^[0-9a-f-]{36}\.json$/i.test(name)) entries++;
      if (/^[0-9a-f-]{36}\.pcm$/i.test(name)) {
        try { bytes += fileIo.statSync(this.directory + '/' + name).size; } catch (_) { }
      }
    }
    if (entries > MAX_LOCAL_ENTRIES || bytes + MAX_RECORDING_BYTES > MAX_LOCAL_BYTES) throw new Error('knowledge_recording_storage_full');
  }

  private requireReady(): void { if (!this.directory || !this.context) throw new Error('storage_unavailable'); }
  private validId(id: string): boolean { return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id); }
  private audioPath(id: string): string { this.requireReady(); if (!this.validId(id)) throw new Error('invalid_recording_id'); return this.directory + '/' + id.toLowerCase() + '.pcm'; }
  private manifestPath(id: string): string { this.requireReady(); if (!this.validId(id)) throw new Error('invalid_recording_id'); return this.directory + '/' + id.toLowerCase() + '.json'; }
}
