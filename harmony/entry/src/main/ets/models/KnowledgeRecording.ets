import {MeetingHubAudio,MeetingHubChoice} from './MeetingHubAudio';
import type {MeetingAudioConsent,MeetingAudioIO,MeetingAudioPipe} from './MeetingHubAudio';
import type {HubTransport} from '../platform/HubTransport';
import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';
import type { MeetingJournal, MeetingRoomIO } from './MeetingRoom';
import { MeetingRoom, newMeetingJournal } from './MeetingRoom';
import { NativeRecording } from '../platform/NativeRecording';
import type { NativeRecordingStop } from '../platform/NativeRecording';

const MAX_TRANSCRIPT_BYTES: number = 4 * 1024 * 1024 - 4096;
const MAX_DURATION_SECONDS: number = 60 * 60;

export interface KnowledgeRecordingSegment {
  start: number;
  end: number;
  text: string;
}

export interface MeetingUploadRole { attendance:string; }

export interface KnowledgeRecordingUpload {
  room?:MeetingUploadRole;
  private?:boolean;
  transcription?:string;
  timeline?:Record<string,Object>;
  spoken?:KnowledgeRecordingSegment[];
  revision: number;
  started_at: string;
  ended_at: string;
  duration: number;
  language: string;
  device: string;
  title: string;
  bookmarks: Object[];
  segments: KnowledgeRecordingSegment[];
  complete: boolean;
  kind: string;
}

/** The owned local draft and the acknowledgement journal for one idempotent Hub recording PUT. */
export interface KnowledgeRecordingEntry {
  id: string;
  profileId: string;
  hubId: string;
  managerId: string;
  startedAt: string;
  endedAt: string;
  duration: number;
  audioBytes: number;
  language: string;
  title: string;
  kind?: string;
  roomRole?: string;
  privateMark?: boolean;
  askedTranscription?:string;
  effectiveTranscription?:string;
  transcript: string;
  revision: number;
  captureState: string;
  transcriptionState: string;
  uploadState: string;
  uploadBody: KnowledgeRecordingUpload | undefined;
  serverStatus: string;
  serverRevision: number;
  interrupted: boolean;
  error: string;
  uploadError: string;
}

export interface RecordingTransport {
  request(path: string, method?: string, body?: string, operationId?: string): Promise<string>;
}

export interface RecordingRepository { transport: RecordingTransport; }

interface RecordingProfile {
  id: string;
  hubId: string;
  manager: string;
  baseUrl: string;
  pin: string;
}

interface RecordingClient {
  connected: boolean;
  demo: boolean;
  profile: RecordingProfile;
  scope(): string;
  connectionGeneration(): number;
  owns(generation: number, scope: string): boolean;
  viewFor(row: ResourceRow): Promise<ClientModel>;
  repo(): RecordingRepository;
}

interface RecordingLocalSnapshot {
  root: ClientModel;
  revision: number;
  generation: number;
  scope: string;
  profileId: string;
  hubId: string;
  rootManager: string;
  ownerManager: string;
  forgetEpoch: number;
}

interface RecordingRemoteSnapshot extends RecordingLocalSnapshot {
  localSnapshot: RecordingLocalSnapshot;
  owner: ClientModel;
  ownerGeneration: number;
  ownerScope: string;
  ownerProfile: RecordingProfile;
  repository: RecordingRepository;
}

function utf8Length(value: string): number {
  let bytes = 0;
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    if (code < 0x80) bytes++;
    else if (code < 0x800) bytes += 2;
    else if (code >= 0xd800 && code <= 0xdbff && index + 1 < value.length) {
      const next = value.charCodeAt(index + 1);
      if (next >= 0xdc00 && next <= 0xdfff) { bytes += 4; index++; }
      else bytes += 3;
    } else bytes += 3;
    if (bytes > MAX_TRANSCRIPT_BYTES) return bytes;
  }
  return bytes;
}

function id(): string {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (value: string): string => {
    const random = Math.floor(Math.random() * 16);
    return (value === 'x' ? random : (random & 0x3) | 0x8).toString(16);
  });
}

function profileCopy(profile: RecordingProfile): RecordingProfile {
  return { id: profile.id, hubId: profile.hubId, manager: profile.manager, baseUrl: profile.baseUrl, pin: profile.pin };
}

function copyEntry(entry: KnowledgeRecordingEntry): KnowledgeRecordingEntry {
  const copied: KnowledgeRecordingEntry = {
    id: entry.id,
    profileId: entry.profileId,
    hubId: entry.hubId,
    managerId: entry.managerId,
    startedAt: entry.startedAt,
    endedAt: entry.endedAt,
    duration: entry.duration,
    audioBytes: entry.audioBytes,
    language: entry.language,
    title: entry.title,
    kind: entry.kind === 'meeting' ? 'meeting' : 'note',
    roomRole:entry.roomRole,privateMark:entry.privateMark,askedTranscription:entry.askedTranscription,effectiveTranscription:entry.effectiveTranscription,
    transcript: entry.transcript,
    revision: entry.revision,
    captureState: entry.captureState,
    transcriptionState: entry.transcriptionState,
    uploadState: entry.uploadState,
    uploadBody: copyUploadBody(entry.uploadBody),
    serverStatus: entry.serverStatus,
    serverRevision: entry.serverRevision,
    interrupted: entry.interrupted,
    error: entry.error,
    uploadError: entry.uploadError
  };
  return copied;
}

function copyUploadBody(body: KnowledgeRecordingUpload | undefined): KnowledgeRecordingUpload | undefined {
  if (!body) return undefined;
  const copied: KnowledgeRecordingUpload = {
    revision: body.revision,
    started_at: body.started_at,
    ended_at: body.ended_at,
    duration: body.duration,
    language: body.language,
    device: body.device,
    title: body.title,
    bookmarks: body.bookmarks.slice(),
    segments: body.segments.map((item: KnowledgeRecordingSegment): KnowledgeRecordingSegment => {
      const segment: KnowledgeRecordingSegment = { start: item.start, end: item.end, text: item.text };
      return segment;
    }),
    complete: body.complete,
    kind: body.kind
  };
  if (body.room) { copied.room={attendance:body.room.attendance}; copied.private=body.private===true; }
  if (body.spoken) copied.spoken=body.spoken.slice();
  if (body.transcription) copied.transcription=body.transcription;
  if (body.timeline) copied.timeline=Wire.parse(JSON.stringify(body.timeline));
  return copied;
}

function errorCode(error: Error): string {
  const value = error as Object as Record<string, Object>;
  const status = typeof value['status'] === 'number' ? value['status'] as number : 0;
  if (['knowledge_recording_microphone_denied', 'knowledge_recording_storage_full', 'knowledge_recording_too_short',
    'knowledge_recording_chinese_only', 'knowledge_recording_transcribe_failed', 'offline', 'request_updated'].includes(error.message)) return error.message;
  if (error.message === 'storage_unavailable') return 'knowledge_storage_unavailable';
  if (error.message === 'recording_limit') return 'knowledge_recording_limit';
  if (error.message === 'recording_write_failed') return 'knowledge_recording_write_failed';
  if (status === 401 || status === 403) return 'permission_denied';
  if (status === 410) return 'knowledge_recording_deleted';
  return 'knowledge_recording_transcribe_failed';
}

/** Capture → stop → explicit on-device transcription → idempotent transcript-only sync. */
@ObservedV2
export class KnowledgeRecording {
  private static readonly MAX_LIVE_MODELS: number = 32;
  private static readonly liveModels: Set<KnowledgeRecording> = new Set<KnowledgeRecording>();

  @Trace entries: KnowledgeRecordingEntry[] = [];
  @Trace entry: KnowledgeRecordingEntry | undefined = undefined;
  @Trace captureState: string = 'idle';
  @Trace transcriptionState: string = 'idle';
  @Trace uploadState: string = 'idle';
  @Trace error: string = '';
  @Trace transcript: string = '';
  @Trace playingId: string = '';
  @Trace roomSpeechProblem:string='';
  readonly room:MeetingRoom=new MeetingRoom();
  private playbackRevision: number = 0;

  private root: ClientModel | undefined;
  private platform: NativeRecording;
  private context: Object | undefined;
  @Trace hubAudio:MeetingHubAudio=new MeetingHubAudio();
  private audioEvents:(()=>void)|undefined;
  private ownerManager: string = '';
  private openRevision: number = 0;
  private local: RecordingLocalSnapshot | undefined;
  private activeTask: number = 0;
  private pendingStartTask: number = 0;
  private localWrite: Promise<void> = Promise.resolve();
  private forgetEpochs: Map<string, number> = new Map<string, number>();
  private locallyDeletedIds: Set<string> = new Set<string>();
  private activeEntryTasks: Set<string> = new Set<string>();
  private activeTranscriptionId: string = '';

  constructor(platform: NativeRecording) {
    if (KnowledgeRecording.liveModels.size >= KnowledgeRecording.MAX_LIVE_MODELS) throw new Error('recording_registry_full');
    this.platform = platform;
    KnowledgeRecording.liveModels.add(this);
  }

  async dispose(): Promise<void> {
    try { await this.stopForBackground(); } catch (_) { }
    this.clearAudio(); this.room.detach(); this.speakerRevision++; this.speakerChanging=false; this.openRevision++; this.activeTask++;
    this.local = undefined; this.root = undefined; this.context = undefined; this.ownerManager = '';
    this.activeTranscriptionId = '';
    this.entries = []; this.entry = undefined; this.transcript = '';
    this.captureState = 'idle'; this.transcriptionState = 'idle'; this.uploadState = 'idle'; this.error = '';
    KnowledgeRecording.liveModels.delete(this);
  }

  /** Stops capture and transcription even when their views remain mounted in the background. */
  static async backgroundAll(): Promise<void> {
    await Promise.all(Array.from(KnowledgeRecording.liveModels).map(async (model: KnowledgeRecording): Promise<void> => {
      try { await model.stopForBackground(); } catch (_) { }
    }));
  }

  /** Retires a forgotten Hub profile before waiting, then removes only that profile's local recordings. */
  static async forgetProfile(profileId: string, context: Object): Promise<void> {
    if (!profileId) return;
    const models = Array.from(KnowledgeRecording.liveModels), platforms = new Set<NativeRecording>();
    const retirements: Promise<void>[] = [];
    for (const model of models) {
      platforms.add(model.platform);
      retirements.push(model.retireProfile(profileId));
    }
    await Promise.all(retirements);
    await Promise.all(Array.from(platforms).map(async (platform: NativeRecording): Promise<void> => {
      await platform.forgetProfile(profileId);
    }));
    // The context is required even when no recording model is live this launch:
    // a fresh platform instance must scan and purge recordings left by prior sessions.
    await NativeRecording.purgeProfile(context, profileId);
  }

  private retireProfile(profileId: string): Promise<void> {
    const writes = this.localWrite;
    this.advanceForgetEpoch(profileId);
    const matches = this.local?.profileId === profileId;
    const playback = matches && this.playingId ? this.stopPlayback() : undefined;
    const capturing = matches && ['requesting', 'recording'].includes(this.captureState);
    const transcribing = matches && (!!this.activeTranscriptionId || this.transcriptionState === 'transcribing');
    if (matches) {
      this.openRevision++; this.activeTask++;
      this.local = undefined; this.root = undefined; this.context = undefined; this.ownerManager = '';
      this.entries = []; this.entry = undefined; this.transcript = '';
      this.captureState = 'idle'; this.transcriptionState = 'idle'; this.uploadState = 'idle'; this.error = '';
      this.activeTranscriptionId = '';
    }
    return (async (): Promise<void> => {
      if (playback) { try { await playback; } catch (_) { } }
      if (transcribing) { try { await this.platform.cancelTranscription(); } catch (_) { } }
      if (capturing) { try { await this.platform.stop(); } catch (_) { } }
      await writes.catch(() => undefined);
    })();
  }

  private forgetEpoch(profileId: string): number { return this.forgetEpochs.get(profileId) || 0; }
  private advanceForgetEpoch(profileId: string): void { this.forgetEpochs.set(profileId, this.forgetEpoch(profileId) + 1); }
  private profileEpochCurrent(snapshot: RecordingLocalSnapshot): boolean {
    return this.forgetEpoch(snapshot.profileId) === snapshot.forgetEpoch;
  }

  private async saveLocal(current: KnowledgeRecordingEntry, snapshot: RecordingLocalSnapshot): Promise<void> {
    const operation = this.localWrite.then(async (): Promise<void> => {
      if (!this.profileEpochCurrent(snapshot) || this.locallyDeletedIds.has(current.id)) return;
      await this.platform.save(current);
      // A write already in flight when Forget began is removed after it finishes.
      if (!this.profileEpochCurrent(snapshot) || this.locallyDeletedIds.has(current.id)) await this.platform.remove(current.id);
    });
    this.localWrite = operation.catch(() => undefined);
    await operation;
  }

  private async removeLocal(recordingId: string): Promise<void> {
    if (this.entry?.id===recordingId) { this.clearAudio(); this.room.detach(); }
    const operation = this.localWrite.then(async (): Promise<void> => { await this.platform.remove(recordingId); });
    this.localWrite = operation.catch(() => undefined);
    await operation;
  }

  private async withEntryTask(recordingId: string, action: () => Promise<boolean>): Promise<boolean> {
    if (this.activeEntryTasks.has(recordingId)) return false;
    this.activeEntryTasks.add(recordingId);
    try { return await action(); }
    finally {
      this.activeEntryTasks.delete(recordingId);
      if (this.locallyDeletedIds.has(recordingId) && !this.entries.some((item: KnowledgeRecordingEntry) => item.id === recordingId))
        this.locallyDeletedIds.delete(recordingId);
    }
  }

  async open(client: ClientModel, ownerManager: string, context: Object): Promise<void> {
    if (this.playingId) this.stopPlayback().catch(() => undefined);
    if (this.captureState === 'recording') await this.stop().catch(() => false);
    if (this.captureState === 'requesting') {
      this.activeTask++;
      await this.platform.stop().catch(() => undefined);
    }
    if (this.activeTranscriptionId || this.transcriptionState === 'transcribing') await this.cancelTranscription().catch(() => undefined);
    this.clearAudio(); this.room.detach(); this.roomSpeechProblem=''; this.speakerRevision++; this.speakerChanging=false;
    const revision = ++this.openRevision;
    this.root = client; this.context = context; this.ownerManager = ownerManager || client.profile.manager;
    this.local = undefined;
    this.entries = []; this.entry = undefined; this.transcript = '';
    this.captureState = 'idle'; this.transcriptionState = 'idle'; this.uploadState = 'idle'; this.error = '';
    let snapshot: RecordingLocalSnapshot | undefined;
    try {
      snapshot = this.captureLocal(client, this.ownerManager, revision);
      this.local = snapshot;
      await this.platform.initialize(context);
      if (!this.isLocalCurrent(snapshot)) return;
      const loaded = await this.platform.load(snapshot.profileId, snapshot.hubId, snapshot.ownerManager);
      if (!this.isLocalCurrent(snapshot)) return;
      const recovered: KnowledgeRecordingEntry[] = [];
      for (const item of loaded) {
        let changed = false;
        if (item.transcriptionState === 'transcribing') {
          item.transcriptionState = 'failed'; item.error = 'knowledge_recording_transcribe_failed'; changed = true;
        }
        if (item.uploadState === 'uploading') {
          // A process can exit after PUT dispatch and before its acknowledgement is journaled.
          // The endpoint replaces by recording id/revision, so retry the saved body as unknown.
          item.uploadState = 'unknown'; item.uploadError = 'knowledge_recording_upload_unknown';
          item.serverStatus = ''; item.serverRevision = 0; changed = true;
        }
        if (changed) recovered.push(item);
      }
      this.entries = loaded;
      const selected = loaded.find((item: KnowledgeRecordingEntry) => item.uploadState === 'unknown' || item.uploadState === 'failed') || loaded[0];
      if (selected) this.selectEntry(selected.id);
      for (const item of recovered) {
        await this.saveLocal(item, snapshot);
        if (!this.isLocalCurrent(snapshot)) return;
      }
    } catch (failure) {
      if (revision === this.openRevision && this.root === client) this.error = errorCode(failure as Error);
    }
  }

  canPlay(): boolean {
    return !!this.entry && !!this.local && this.isLocalCurrent(this.local) && this.entry.captureState === 'stopped' &&
      this.entry.audioBytes > 0 && !['requesting','recording'].includes(this.captureState) && this.transcriptionState !== 'transcribing';
  }
  async playSelected(): Promise<boolean> {
    const snapshot = this.local, entry = this.entry;
    if (!snapshot || !entry || !this.canPlay()) return false;
    if (this.playingId) { await this.stopPlayback(); return false; }
    const revision = ++this.playbackRevision;
    const valid = (): boolean => this.playbackRevision === revision && this.playingId === entry.id && this.isLocalCurrent(snapshot) && this.entry?.id === entry.id;
    this.playingId = entry.id;
    try {
      await this.platform.playLocal(entry,valid,(failed:boolean) => {
        if (!valid()) return;
        this.playingId = ''; if (failed) this.error = 'phone_record_play_failed';
      });
      return valid();
    } catch (_) {
      if (valid()) { this.playingId = ''; this.error = 'phone_record_play_failed'; }
      return false;
    }
  }
  async stopPlayback(): Promise<void> {
    this.playbackRevision++;
    if (!this.playingId) return;
    this.playingId = ''; await this.platform.stopPlayback();
  }

  canSelectEntry(): boolean {
    return !this.pendingStartTask && this.activeEntryTasks.size === 0 &&
      !['requesting', 'recording'].includes(this.captureState) && this.transcriptionState !== 'transcribing' && this.uploadState !== 'uploading';
  }

  selectEntry(recordingId: string): void {
    if (!this.canSelectEntry()) return;
    const selected = this.entries.find((item: KnowledgeRecordingEntry) => item.id === recordingId);
    if (!selected) return;
    if (this.playingId && this.playingId !== selected.id) this.stopPlayback().catch(() => undefined);
    this.entry = selected; this.transcript = selected.transcript; this.captureState = selected.captureState;
    this.transcriptionState = selected.transcriptionState; this.uploadState = selected.uploadState;
    this.error = selected.error || selected.uploadError;
    this.clearAudio(); this.room.detach(); this.roomSpeechProblem='';
    if (selected.roomRole && this.local) {
      try {
        this.attachRoom(selected,this.local);
        if(selected.askedTranscription==='hub' && selected.endedAt)this.resumeMeetingAudio(selected,this.local);
        if (selected.endedAt) this.room.finish(selected.endedAt).then(() => this.room.sync()).catch(() => {});
        else this.room.sync().catch(() => {});
      } catch (_) { this.roomSpeechProblem='knowledge_storage_unavailable'; }
    }
  }

  canStart(): boolean { return !!this.local && !!this.context && this.pendingStartTask === 0 && this.activeEntryTasks.size === 0 &&
    this.uploadState !== 'uploading' && !['requesting', 'recording'].includes(this.captureState) && this.transcriptionState !== 'transcribing'; }
  canStop(): boolean { return this.captureState === 'recording'; }
  @Trace speakerChanging:boolean=false;
  private speakerRevision:number=0;

  async setRoomSpeaker(on:boolean,echo:boolean):Promise<boolean> {
    const entry=this.entry,local=this.local;
    if (!entry || !local || !this.isLocalCurrent(local) || this.captureState!=='recording' || !['listener','participant'].includes(entry.roomRole || '') || !this.room.active || this.speakerChanging) return false;
    const run=++this.speakerRevision;
    const current=():boolean => this.speakerRevision===run && this.isLocalCurrent(local) && this.entry?.id===entry.id && this.captureState==='recording';
    const copy=copyEntry(entry); copy.roomRole=on ? 'participant' : 'listener';
    this.speakerChanging=true;
    try {
      if (!on) this.platform.stopRoomVoice();
      this.entry=copy;
      await this.saveLocal(copy,local); if (!current()) return false;
      this.entries=this.entries.map((item:KnowledgeRecordingEntry)=>item.id===copy.id ? copy : item);
      const changed=await this.room.changeSpeaker(on);
      if (!current()) return false;
      if (on && changed && echo && !await this.platform.enableSpeakerEcho(entry.id) && current()) this.roomSpeechProblem='phone_meeting_echo_restore';
      return changed;
    } catch (_) {
      if (current()) { if (on && this.entry===copy) this.entry=entry; this.roomSpeechProblem='knowledge_storage_unavailable'; }
      return false;
    } finally { if (this.speakerRevision===run) this.speakerChanging=false; }
  }
  canTranscribe(): boolean { return !!this.entry && this.entry.captureState === 'stopped' && this.entry.transcriptionState !== 'complete' && this.entry.transcriptionState !== 'transcribing'; }
  canRetryUpload(): boolean { return !!this.entry && !!this.entry.uploadBody && ['unknown', 'failed'].includes(this.entry.uploadState); }
  canDeleteLocal(): boolean { return !!this.entry && this.entry.captureState !== 'recording' && this.entry.transcriptionState !== 'transcribing' && this.entry.uploadState !== 'uploading'; }

  private clearAudio():void { this.audioEvents?.();this.audioEvents=undefined;this.hubAudio.clear(); }
  withdrawHubAudio():void { this.hubAudio.withdraw();this.audioEvents?.();this.audioEvents=undefined; }
  private async choiceReady(choice:MeetingHubChoice,client:ClientModel):Promise<boolean> {
    if(!choice.matches(client)) return false;
    const repo=client.repo(),value=await repo.call('/api/v1/assistant');
    return choice.matches(client) && repo===client.repo() && Wire.text(value,'manager',client.profile.manager)===choice.manager &&
      ['device','hub'].includes(Wire.text(value,'meeting_transcription_default')) && Wire.bool(value,'meeting_transcription_ready') &&
      Wire.text(value,'meeting_transcription_extension')===choice.serviceRef;
  }
  private resumeMeetingAudio(entry:KnowledgeRecordingEntry,local:RecordingLocalSnapshot):void {
    const owner=this.root,grant=this.platform.meetingConsent(entry.id);
    if(!owner || !grant || !owner.connected || !this.isLocalCurrent(local))return;
    const expired=Date.now()-Date.parse(entry.endedAt)>30*60*1000;
    const choice=new MeetingHubChoice(owner,grant.serviceRef,grant.serviceName,grant.wifiOnly);
    if(expired || entry.effectiveTranscription==='device' || grant.profileId!==choice.profile || grant.hubId!==choice.hub ||
      grant.managerId!==choice.manager || grant.pin!==choice.pin || grant.endpoint!==choice.endpoint ||
      owner.repo().transport.meetingCredentialKey()!==grant.credentialKey || !choice.matches(owner)){
      this.platform.clearMeetingConsent(entry.id);return;
    }
    const journal=this.platform.loadRoom(entry.id,entry.profileId,entry.hubId,entry.managerId);
    if(journal?.effectiveTranscription!=='hub')return;
    this.attachAudio(entry,local,choice,owner,grant);
    this.hubAudio.resumeStopped(Math.floor(entry.audioBytes/2),Date.parse(entry.endedAt));
  }
  private attachAudio(entry:KnowledgeRecordingEntry,local:RecordingLocalSnapshot,choice:MeetingHubChoice,owner:ClientModel,grant:MeetingAudioConsent):void {
    const repo=owner.repo(),path='/api/v1/recordings/'+encodeURIComponent(entry.id), api=repo.transport;
    let repairing:HubTransport|undefined;
    const current=():boolean => {
      if(!this.isLocalCurrent(local) || this.entry?.id!==entry.id || this.locallyDeletedIds.has(entry.id) || !choice.sameOwner(owner) || choice.manager!==entry.managerId)return false;
      if(!owner.connected)return true;
      try{return choice.matches(owner) && owner.repo()===repo;}catch(_){return false;}
    };
    const io:MeetingAudioIO={
      current:current,network:():string=>current() && owner.connected ? this.platform.meetingNetworkKey(choice.wifiOnly):'',
      authorize:async ():Promise<boolean>=>current() && api.meetingCredentialKey()===grant.credentialKey && await this.choiceReady(choice,owner) && current(),
      pipe:(network:string):MeetingAudioPipe=>{if(!current())throw new Error('request_updated');return api.stream(path+'/audio',this.platform.meetingNetworkBinding(network,choice.wifiOnly,current));},
      pcm:(from:number,samples:number):Promise<ArrayBuffer>=>this.platform.readMeetingPCM(entry.id,from,samples),
      live:async (from:number):Promise<Record<string,Object>>=>{
        if(!current() || !owner.connected)throw new Error('request_updated');
        const result=await repo.call(path+'/live?from='+String(from));if(!current())throw new Error('request_updated');return result;
      },timeline:():Record<string,Object>=>this.room.audioTimeline(),
      uploadGap:async (key:string,from:number,data:ArrayBuffer,admit:()=>boolean):Promise<Record<string,Object>>=>{
        if(!current() || !admit() || !key.startsWith('any:'))throw new Error('request_updated');
        const route=path+'/audio?at='+String(from/16000);
        const child=api.stream(route,this.platform.meetingNetworkBinding(key,choice.wifiOnly,admit));repairing=child;
        try{
          if(!admit())throw new Error('request_updated');
          const response=await child.putMeetingGap(route,data);
          if(!current() || !admit())throw new Error('request_updated');
          return Wire.parse(response);
        }finally{child.close();if(repairing===child)repairing=undefined;}
      },stopGap:():void=>{repairing?.close();repairing=undefined;},
      retire:():void=>{try{this.platform.clearMeetingConsent(entry.id);}catch(_){this.error='knowledge_storage_unavailable';}}
    };
    this.hubAudio.begin(io,entry.roomRole || 'off');
    this.audioEvents=owner.observeEvents((raw:string):void=>{
      if(!current()) {this.withdrawHubAudio();return;}
      if(raw.length>2*1024*1024)return;
      const kind=Wire.text(Wire.parse(raw),'type');
      if(['assistant','assistant_config','assistant_settings','plugin','plugins','extensions','features'].includes(kind)) this.hubAudio.recheck();
    });
  }
  private attachRoom(entry:KnowledgeRecordingEntry, local:RecordingLocalSnapshot):void {
    if (!entry.roomRole || !this.isLocalCurrent(local) || this.entry?.id!==entry.id) return;
    const state=this.platform.loadRoom(entry.id,entry.profileId,entry.hubId,entry.managerId) ||
      newMeetingJournal(entry.id,entry.profileId,entry.hubId,entry.managerId,entry.startedAt,entry.title,entry.language,entry.roomRole,entry.privateMark===true,entry.askedTranscription || '');
    this.platform.saveRoom(state);
    const current=():boolean => this.isLocalCurrent(local) && this.entry?.id===entry.id && !this.locallyDeletedIds.has(entry.id);
    const io:MeetingRoomIO={
      current:current,
      speak:async (requestId:string,text:string,language:string):Promise<void> => {
        const admitted=():boolean => current() && this.captureState==='recording' && this.entry?.roomRole==='participant';
        if (admitted()) await this.platform.speakRoom(requestId,text,language,admitted);
      },
      save:async (journal:MeetingJournal):Promise<void> => { if (!current()) throw new Error('request_updated'); this.platform.saveRoom(journal); },
      request:async (path:string,method:string,body?:Record<string,Object>):Promise<Record<string,Object>> => {
        const owner=await this.captureRemote(local);
        if (!current() || !this.isRemoteCurrent(owner) || !this.recordMatches(entry,owner)) throw new Error('request_updated');
        const raw=await owner.repository.transport.request(path,method,body ? JSON.stringify(body) : '');
        if (!current() || !this.isRemoteCurrent(owner)) throw new Error('request_updated');
        return Wire.parse(raw);
      }
    };
    if(entry.askedTranscription==='hub'){
      io.audioMode=(ack:Record<string,Object>):void=>{if(current()){entry.effectiveTranscription=Wire.text(ack,'transcription')==='hub' ? 'hub':'device';this.hubAudio.effective(ack);}};
      io.audioClock=():number=>this.hubAudio.seconds();
      io.audioRole=(role:string):void=>this.hubAudio.roleChanged(role);
      io.audioSpeaking=(on:boolean,text:string):void=>this.hubAudio.voice(on,text);
    }
    this.room.attach(state,io);
  }

  async askRoom(question:string):Promise<boolean> {
    const entry=this.entry;
    if (!entry || this.captureState!=='recording' || !['participant','listener'].includes(entry.roomRole || '') || !this.room.active) return false;
    return this.room.ask(id(),question,Math.max(0,(Date.now()-Date.parse(entry.startedAt))/1000));
  }

  async start(kind: string = 'note', title: string = '', invitedRole:string = 'off', privateMark:boolean = false, hubChoice?:MeetingHubChoice,inputId:number=-1): Promise<boolean> {
    if (this.playingId) {
      const before = this.entry, owner = this.local;
      await this.stopPlayback();
      if (before !== this.entry || owner !== this.local) return false;
    }
    const snapshot = this.local, context = this.context, client = this.root;
    if (!snapshot || !context || !client || !this.canStart() || !this.isLocalCurrent(snapshot)) return false;
    const invited=kind==='meeting' && ['listener','participant'].includes(invitedRole);
    if(hubChoice && (kind!=='meeting' || !hubChoice.sameOwner(client) || hubChoice.manager!==snapshot.ownerManager)){ this.error='request_updated';return false; }
    if (privateMark && (kind!=='meeting' || !client.connected || !client.features.includes('meeting_conversation'))) {
      this.error='phone_meeting_unavailable';return false;
    }
    if (invited && (!client.features.includes('meeting_conversation') || !client.features.includes('meeting_attendance'))) {
      this.error='phone_meeting_unavailable'; return false;
    }
    this.clearAudio(); this.room.detach(); this.roomSpeechProblem='';
    const task = ++this.activeTask;
    this.pendingStartTask = task;
    this.error = ''; this.captureState = 'requesting';
    try {
      await this.platform.ensureMicrophonePermission(context);
      if (task !== this.activeTask || !this.isLocalCurrent(snapshot)) {
        if (task === this.activeTask && this.captureState === 'requesting') this.captureState = 'idle';
        return false;
      }
      if (privateMark && (!client.connected || !client.features.includes('meeting_conversation'))) {
        this.captureState='idle';this.error='phone_meeting_unavailable';return false;
      }
      let hub=false;
      if(hubChoice){ try{hub=await this.choiceReady(hubChoice,client);}catch(_){}
        if(task!==this.activeTask || !this.isLocalCurrent(snapshot) || !hubChoice.sameOwner(client)){this.captureState='idle';this.error='request_updated';return false;}
      }
      const startedAt = new Date().toISOString(), recordingId = id().toLowerCase();
      const entry: KnowledgeRecordingEntry = {
        id: recordingId, profileId: snapshot.profileId, hubId: snapshot.hubId, managerId: snapshot.ownerManager,
        startedAt, endedAt: '', duration: 0, audioBytes: 0, language: 'zh-CN', title: title.trim().slice(0,256), kind: kind === 'meeting' ? 'meeting' : 'note', transcript: '', revision: 0,
        captureState: 'recording', transcriptionState: 'idle', uploadState: 'idle', uploadBody: undefined,
        serverStatus: '', serverRevision: 0, interrupted: false, error: '', uploadError: ''
      };
      if (invited || hub || privateMark) { entry.roomRole=invited ? invitedRole:'off'; entry.privateMark=privateMark; }
      let grant:MeetingAudioConsent|undefined;
      if(hub && hubChoice){
        entry.askedTranscription='hub';
        grant={id:entry.id,profileId:hubChoice.profile,hubId:hubChoice.hub,managerId:hubChoice.manager,endpoint:hubChoice.endpoint,pin:hubChoice.pin,
          serviceRef:hubChoice.serviceRef,serviceName:hubChoice.serviceName,wifiOnly:hubChoice.wifiOnly,credentialKey:client.repo().transport.meetingCredentialKey()};
        this.platform.saveMeetingConsent(grant);
      }
      await this.saveLocal(entry, snapshot);
      if (task !== this.activeTask || !this.isLocalCurrent(snapshot)) {
        await this.platform.remove(entry.id);
        if (task === this.activeTask && this.captureState === 'requesting') this.captureState = 'idle';
        return false;
      }
      this.entry = entry; this.entries = [entry, ...this.entries]; this.transcript = '';
      if(hub && hubChoice && grant)this.attachAudio(entry,snapshot,hubChoice,client,grant);
      else if(hubChoice)this.hubAudio.useDevice('unavailable');
      if (entry.roomRole) this.attachRoom(entry,snapshot);
      if (invited || hub) {
        await this.platform.startRoomSpeech((from:number,to:number,text:string) => {
          if (this.isLocalCurrent(snapshot) && this.entry?.id===entry.id) this.room.line(from,to,text).catch(() => {});
        },() => { if (this.isLocalCurrent(snapshot) && this.entry?.id===entry.id) this.roomSpeechProblem='phone_meeting_speech_unavailable'; });
        if (task!==this.activeTask || !this.isLocalCurrent(snapshot)) { await this.platform.stop(); return false; }
      }
      await this.platform.start(context, recordingId, (reason: string) => { this.stopForCaptureFailure(reason).catch(() => undefined); },invitedRole==='participant' && invited,hub ? (samples:number):void=>this.hubAudio.captured(samples) : undefined,inputId);
      if (task !== this.activeTask || !this.isLocalCurrent(snapshot)) { await this.stopEntry(entry, true); return false; }
      this.captureState = 'recording'; if (entry.roomRole) this.room.sync().catch(() => {}); return true;
    } catch (failure) {
      const current = this.entry, failureFields = failure as Object as Record<string, Object>;
      let stopped = failureFields['recordingStop'] ? failureFields['recordingStop'] as Object as NativeRecordingStop : undefined;
      if (current?.captureState === 'recording' && !stopped) {
        try { stopped = await this.platform.stop(); } catch (_) { }
      }
      if (!this.isLocalCurrent(snapshot)) {
        if (current?.captureState === 'recording') await this.stopEntry(current, true, stopped).catch(() => false);
        return false;
      }
      const code = errorCode(failure as Error);
      if (current?.captureState === 'recording') {
        const hasAudio = await this.stopEntry(current, false, stopped);
        const finished = this.entry;
        if (hasAudio && finished?.id === current.id) {
          const kept = copyEntry(finished); kept.error = code;
          await this.persistLocal(kept, snapshot); this.error = code;
        } else if (!hasAudio && finished?.id === current.id) {
          this.entries = this.entries.filter((item: KnowledgeRecordingEntry) => item.id !== current.id);
          this.entry = undefined; this.captureState = 'idle'; this.error = code;
          await this.platform.remove(current.id).catch(() => undefined);
        }
      } else {
        if (task === this.activeTask && this.captureState === 'requesting') this.captureState = 'idle';
        this.error = task === this.activeTask ? code : '';
      }
      return false;
    } finally {
      if (this.pendingStartTask === task) this.pendingStartTask = 0;
    }
  }

  async stop(): Promise<boolean> {
    const current = this.entry;
    if (!current || current.captureState !== 'recording') return false;
    return this.stopEntry(current, false);
  }

  async stopForBackground(): Promise<void> {
    if (this.playingId) await this.stopPlayback();
    if (this.captureState === 'recording' || this.captureState === 'requesting') {
      this.activeTask++;
      if (this.captureState === 'recording') await this.stop();
      else {
        const current = this.entry;
        let stopped: NativeRecordingStop | undefined;
        try { stopped = await this.platform.stop(); } catch (_) { }
        if (current?.captureState === 'recording') await this.stopEntry(current, false, stopped);
      }
    }
    if (this.activeTranscriptionId || this.transcriptionState === 'transcribing') await this.cancelTranscription();
  }

  async stopForCaptureFailure(reason: string): Promise<void> {
    const current = this.entry;
    if (!current || current.captureState !== 'recording') return;
    await this.stopEntry(current, false);
    if (this.entry?.id === current.id) {
      const stopped = copyEntry(this.entry); stopped.error = reason;
      await this.persistLocal(stopped, this.local);
      if (this.entry?.id === stopped.id) this.error = reason;
    }
  }

  private async stopEntry(current: KnowledgeRecordingEntry, stale: boolean, stoppedResult?: NativeRecordingStop): Promise<boolean> {
    const snapshot = this.local;
    const stopped = copyEntry(current);
    let result: NativeRecordingStop = stoppedResult || { path: '', bytes: stopped.audioBytes, duration: stopped.duration };
    if (!stoppedResult) { try { result = await this.platform.stop(); } catch (_) { } }
    stopped.audioBytes = Math.max(0, Math.min(result.bytes, 115200000));
    stopped.duration = Math.max(0, Math.min(result.duration, MAX_DURATION_SECONDS));
    stopped.endedAt = new Date().toISOString(); stopped.captureState = 'stopped'; stopped.interrupted = stale;
    if (stopped.audioBytes === 0) stopped.error = 'knowledge_recording_too_short';
    let saved = true;
    try { await this.persistLocal(stopped, snapshot); } catch (_) { saved = false; }
    if (!stale && snapshot && this.isLocalCurrent(snapshot) && this.entry?.id === stopped.id) {
      if (!saved) {
        this.entries = this.entries.map((item: KnowledgeRecordingEntry) => item.id === stopped.id ? stopped : item);
        this.entry = stopped;
      }
      this.captureState = 'stopped'; this.transcriptionState = stopped.transcriptionState; this.uploadState = stopped.uploadState;
      this.error = saved ? stopped.error : 'knowledge_storage_unavailable';
      if(stopped.askedTranscription==='hub')this.hubAudio.finish(Math.floor(stopped.audioBytes/2));
      if (stopped.roomRole && this.room.active) this.room.finish(stopped.endedAt).catch(() => {});
    }
    return stopped.audioBytes > 0 && saved;
  }

  /** Transcription starts only from this explicit action. The same stable id/revision is then uploaded as full state. */
  async transcribeAndUpload(): Promise<boolean> {
    if (this.playingId) {
      const before = this.entry, owner = this.local;
      await this.stopPlayback();
      if (before !== this.entry || owner !== this.local) return false;
    }
    const current = this.entry, snapshot = this.local;
    if (!current || !snapshot || !this.canTranscribe() || !this.isLocalCurrent(snapshot) || this.activeEntryTasks.has(current.id)) return false;
    if (current.language !== 'zh-CN') { this.error = 'knowledge_recording_chinese_only'; return false; }
    if (current.duration <= 0 || current.duration > MAX_DURATION_SECONDS || current.audioBytes <= 0) { this.error = 'knowledge_recording_too_short'; return false; }
    return this.withEntryTask(current.id, async (): Promise<boolean> => {
      this.activeTranscriptionId = current.id;
      try { return await this.transcribeAndUploadCurrent(current, snapshot); }
      finally { if (this.activeTranscriptionId === current.id) this.activeTranscriptionId = ''; }
    });
  }

  private async transcribeAndUploadCurrent(current: KnowledgeRecordingEntry, snapshot: RecordingLocalSnapshot): Promise<boolean> {
    const task = ++this.activeTask;
    const progress = copyEntry(current);
    progress.transcriptionState = 'transcribing'; progress.error = '';
    this.transcriptionState = 'transcribing'; this.error = '';
    const result = copyEntry(progress);
    try {
      await this.persistLocal(progress, snapshot);
      if (task !== this.activeTask || !this.isLocalCurrent(snapshot)) {
        if (task !== this.activeTask && this.isLocalCurrent(snapshot)) {
          result.transcriptionState = result.transcript ? 'complete' : 'idle'; result.error = '';
          try { await this.persistLocal(result, snapshot); }
          catch (_) {
            if (this.isLocalCurrent(snapshot) && this.entry?.id === result.id) {
              this.entries = this.entries.map((item: KnowledgeRecordingEntry) => item.id === result.id ? result : item);
              this.entry = result; this.transcriptionState = result.transcriptionState;
              this.error = 'knowledge_storage_unavailable';
            }
          }
        }
        return false;
      }
      const text = (await this.platform.transcribe(result.id, result.language)).trim();
      if (task !== this.activeTask || text.length === 0 || utf8Length(text) > MAX_TRANSCRIPT_BYTES) throw new Error('knowledge_recording_transcribe_failed');
      const changed = text !== result.transcript || !result.uploadBody;
      result.transcript = text; result.transcriptionState = 'complete'; result.error = '';
      if (changed) {
        result.revision = Math.max(Date.now(), result.revision + 1, result.roomRole ? Date.parse(result.endedAt)+1 : 0);
        result.uploadBody = this.makeUpload(result);
        result.uploadState = 'idle'; result.serverStatus = ''; result.serverRevision = 0; result.uploadError = '';
      }
      await this.persistLocal(result, snapshot);
      if (!this.isLocalCurrent(snapshot)) return false;
      return await this.uploadEntry(result, snapshot);
    } catch (failure) {
      if (task !== this.activeTask) return false;
      result.transcriptionState = result.transcript ? 'complete' : (failure as Error).message === 'transcription_cancelled' ? 'idle' : 'failed';
      result.error = result.transcript ? '' : errorCode(failure as Error);
      try { await this.persistLocal(result, snapshot); }
      catch (_) {
        if (this.isLocalCurrent(snapshot) && this.entry?.id === result.id) {
          this.entries = this.entries.map((item: KnowledgeRecordingEntry) => item.id === result.id ? result : item);
          this.entry = result; this.transcriptionState = result.transcriptionState;
          this.error = 'knowledge_storage_unavailable';
        }
      }
      return false;
    }
  }

  async retryUpload(): Promise<boolean> {
    const current = this.entry, snapshot = this.local;
    if (!current || !snapshot || !this.canRetryUpload() || !this.isLocalCurrent(snapshot) || this.activeEntryTasks.has(current.id)) return false;
    return this.withEntryTask(current.id, async (): Promise<boolean> => await this.uploadEntry(current, snapshot));
  }

  async cancelTranscription(): Promise<void> {
    this.activeTask++;
    const snapshot = this.local;
    const activeId = this.activeTranscriptionId;
    await this.platform.cancelTranscription();
    const current = this.entries.find((item: KnowledgeRecordingEntry) => item.id === activeId) || this.entry;
    if (current?.transcriptionState === 'transcribing') {
      const cancelled = copyEntry(current);
      cancelled.transcriptionState = cancelled.transcript ? 'complete' : 'idle';
      await this.persistLocal(cancelled, snapshot);
      if (snapshot && this.isLocalCurrent(snapshot) && this.entry?.id === current.id) {
        this.transcriptionState = cancelled.transcriptionState;
        this.error = '';
      }
    }
  }

  async deleteLocal(): Promise<boolean> {
    if (this.playingId) {
      const before = this.entry, owner = this.local;
      await this.stopPlayback();
      if (before !== this.entry || owner !== this.local) return false;
    }
    const current = this.entry, snapshot = this.local;
    if (!current || !snapshot || !this.isLocalCurrent(snapshot) || !this.canDeleteLocal() || current.captureState === 'recording') return false;
    this.locallyDeletedIds.add(current.id);
    try {
      await this.removeLocal(current.id);
      if (!this.activeEntryTasks.has(current.id)) this.locallyDeletedIds.delete(current.id);
      if (!this.isLocalCurrent(snapshot) || this.entry?.id !== current.id) return true;
      this.entries = this.entries.filter((item: KnowledgeRecordingEntry) => item.id !== current.id);
      this.entry = this.entries[0]; this.transcript = this.entry?.transcript || '';
      this.captureState = this.entry?.captureState || 'idle'; this.transcriptionState = this.entry?.transcriptionState || 'idle';
      this.uploadState = this.entry?.uploadState || 'idle'; this.error = '';
      return true;
    } catch (_) { if (this.isLocalCurrent(snapshot) && this.entry?.id === current.id) this.error = 'storage_unavailable'; return false; }
  }

  private async uploadEntry(current: KnowledgeRecordingEntry, local: RecordingLocalSnapshot): Promise<boolean> {
    if (!this.entries.some((item: KnowledgeRecordingEntry) => item.id === current.id) || !current.uploadBody || !current.transcript ||
      this.locallyDeletedIds.has(current.id) || current.uploadState === 'uploading' ||
      ['stored', 'stale', 'expired'].includes(current.serverStatus)) return false;
    let owner: RecordingRemoteSnapshot;
    try { owner = await this.captureRemote(local); }
    catch (failure) {
      const failed = copyEntry(current); failed.uploadState = 'failed'; failed.uploadError = errorCode(failure as Error);
      await this.persistLocal(failed, local); return false;
    }
    if (!this.isRemoteCurrent(owner) || !this.recordMatches(current, owner)) {
      const failed = copyEntry(current); failed.uploadState = 'failed'; failed.uploadError = 'request_updated';
      await this.persistLocal(failed, local); return false;
    }
    const pending = copyEntry(current), body = pending.uploadBody;
    if (!body || !this.isRemoteCurrent(owner)) return false;
    pending.uploadState = 'uploading'; pending.uploadError = '';
    await this.persistLocal(pending, local);
    if (this.locallyDeletedIds.has(current.id) || !this.entries.some((item: KnowledgeRecordingEntry) => item.id === current.id) || !this.isRemoteCurrent(owner)) {
      const failed = copyEntry(pending); failed.uploadState = 'failed'; failed.uploadError = 'request_updated';
      await this.persistLocal(failed, local); return false;
    }
    const attempted = copyEntry(pending);
    const path = '/api/v1/recordings/' + current.id.toLowerCase();
    try {
      // Recording PUT is documented as an idempotent full-state write, not a client_ops receipt route.
      // The manifest journals this exact body/id/revision before dispatch and records the server status after reply.
      const raw = await owner.repository.transport.request(path, 'PUT', JSON.stringify(body));
      const answer = JSON.parse(raw) as Record<string, Object>;
      const ackId = typeof answer['id'] === 'string' ? answer['id'] as string : '';
      const ackRevision = typeof answer['revision'] === 'number' ? answer['revision'] as number : 0;
      const status = typeof answer['status'] === 'string' ? answer['status'] as string : '';
      if (ackId.toLowerCase() !== current.id.toLowerCase() || !Number.isInteger(ackRevision) || ackRevision <= 0 ||
        !['stored', 'stale', 'expired'].includes(status)) throw new Error('result_unknown');
      attempted.serverStatus = status; attempted.serverRevision = ackRevision; attempted.uploadState = status;
      attempted.uploadError = '';
      await this.persistLocal(attempted, local);
      const stillCurrent = this.isRemoteCurrent(owner);
      return stillCurrent && status === 'stored' && ackRevision >= body.revision;
    } catch (failure) {
      const fields = failure as Object as Record<string, Object>, status = typeof fields['status'] === 'number' ? fields['status'] as number : 0;
      const unknown = fields['resultUnknown'] === true || fields['resultPending'] === true || status === 0 || status >= 500 || (failure as Error).message === 'result_unknown';
      attempted.uploadState = unknown ? 'unknown' : status === 410 ? 'deleted' : 'failed';
      attempted.uploadError = status === 410 ? 'knowledge_recording_deleted' : unknown ? 'knowledge_recording_upload_unknown' : 'knowledge_recording_upload_failed';
      await this.persistLocal(attempted, local);
      return false;
    }
  }

  private makeUpload(current: KnowledgeRecordingEntry): KnowledgeRecordingUpload {
    const segment: KnowledgeRecordingSegment = { start: 0, end: current.duration, text: current.transcript };
    const body:KnowledgeRecordingUpload = {
      revision: current.revision, started_at: current.startedAt, ended_at: current.endedAt, duration: current.duration,
      language: current.language, device: 'phone', title: current.title, bookmarks: [], segments: [segment], complete: true, kind: current.kind === 'meeting' ? 'meeting' : 'note'
    };
    if(current.askedTranscription==='hub'){body.transcription=current.effectiveTranscription==='device' ? 'device':'hub';body.timeline=this.room.audioTimeline();}
    if (current.roomRole) {
      body.room={attendance:current.roomRole}; body.private=current.privateMark===true;
      const spoken=this.room.spokenLines();
      if (spoken.length && this.root?.features.includes('meeting_echo')) body.spoken=spoken;
    }
    return body;
  }

  private async captureRemote(local: RecordingLocalSnapshot): Promise<RecordingRemoteSnapshot> {
    if (!this.isLocalCurrent(local)) throw new Error('request_updated');
    const root = local.root as RecordingClient;
    if (!root.connected || root.demo) throw new Error(root.connected ? 'request_updated' : 'offline');
    let owner = root;
    if (local.ownerManager !== root.profile.manager) {
      const ref = new ResourceRow();
      ref.group = root.profile.id;
      ref.manager = local.ownerManager;
      owner = await root.viewFor(ref) as RecordingClient;
      if (!this.isLocalCurrent(local) || !owner.connected || owner.profile.id !== local.profileId || owner.profile.hubId !== local.hubId ||
        owner.profile.manager !== local.ownerManager) throw new Error('request_updated');
    }
    const ownerGeneration = owner.connectionGeneration(), ownerScope = owner.scope(), ownerProfile = profileCopy(owner.profile);
    const repository = owner.repo();
    const snapshot: RecordingRemoteSnapshot = {
      root: local.root,
      revision: local.revision,
      generation: local.generation,
      scope: local.scope,
      profileId: local.profileId,
      hubId: local.hubId,
      rootManager: local.rootManager,
      ownerManager: local.ownerManager,
      forgetEpoch: local.forgetEpoch,
      localSnapshot: local,
      owner: owner as ClientModel,
      ownerGeneration,
      ownerScope,
      ownerProfile,
      repository
    };
    if (!this.isRemoteCurrent(snapshot)) throw new Error('request_updated');
    return snapshot;
  }

  private captureLocal(client: ClientModel, manager: string, revision: number): RecordingLocalSnapshot {
    const root = client as RecordingClient, generation = client.connectionGeneration(), scope = client.scope();
    const snapshot: RecordingLocalSnapshot = {
      root: client, revision, generation, scope, profileId: root.profile.id, hubId: root.profile.hubId,
      rootManager: root.profile.manager, ownerManager: manager, forgetEpoch: this.forgetEpoch(root.profile.id)
    };
    if (!snapshot.profileId || root.demo || !root.owns(generation, scope) || root.profile.id !== snapshot.profileId ||
      root.profile.hubId !== snapshot.hubId || root.profile.manager !== snapshot.rootManager) throw new Error('request_updated');
    return snapshot;
  }

  private isLocalCurrent(snapshot: RecordingLocalSnapshot): boolean {
    if (this.openRevision !== snapshot.revision || this.root !== snapshot.root || this.local !== snapshot) return false;
    const root = snapshot.root as RecordingClient;
    return this.profileEpochCurrent(snapshot) && root.owns(snapshot.generation, snapshot.scope) && root.profile.id === snapshot.profileId && root.profile.hubId === snapshot.hubId &&
      root.profile.manager === snapshot.rootManager && this.ownerManager === snapshot.ownerManager;
  }

  private isRemoteCurrent(snapshot: RecordingRemoteSnapshot): boolean {
    const profile = snapshot.owner.profile;
    const expected = snapshot.ownerProfile;
    if (!this.isLocalCurrent(snapshot.localSnapshot) || !snapshot.owner.connected || !snapshot.owner.owns(snapshot.ownerGeneration, snapshot.ownerScope) ||
      profile.id !== expected.id || profile.hubId !== expected.hubId || profile.manager !== expected.manager ||
      profile.baseUrl !== expected.baseUrl || profile.pin !== expected.pin || profile.id !== snapshot.profileId ||
      profile.hubId !== snapshot.hubId || profile.manager !== snapshot.ownerManager) return false;
    try { return snapshot.owner.repo() === snapshot.repository; } catch (_) { return false; }
  }

  private recordMatches(current: KnowledgeRecordingEntry, owner: RecordingRemoteSnapshot): boolean {
    return current.profileId === owner.profileId && current.hubId === owner.hubId && current.managerId === owner.ownerManager;
  }

  private async persistLocal(current: KnowledgeRecordingEntry, snapshot: RecordingLocalSnapshot | undefined = this.local): Promise<void> {
    if (!snapshot) return;
    await this.saveLocal(current, snapshot);
    if (!snapshot || !this.isLocalCurrent(snapshot) || this.entry?.id !== current.id) return;
    const index = this.entries.findIndex((item: KnowledgeRecordingEntry) => item.id === current.id);
    if (index < 0) this.entries = [current, ...this.entries];
    else { const next = this.entries.slice(); next[index] = current; this.entries = next; }
    if (this.entry?.id === current.id) {
      this.entry = current; this.captureState = current.captureState; this.transcriptionState = current.transcriptionState;
      this.uploadState = current.uploadState; this.transcript = current.transcript;
    }
  }

}
