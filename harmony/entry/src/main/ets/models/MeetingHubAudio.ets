import type {ClientModel} from './ClientModel';
import {Wire} from './Wire';

type Fields=Record<string,Object>;
const rate=16000;
const bytesPerSample=2;
const liveChunk=1600;
const maxChunk=32000;
const ringSamples=60*rate;
const maxMeetingSamples=12*3600*rate;

export class MeetingHubChoice {
  readonly scope:string;
  readonly generation:number;
  readonly profile:string;
  readonly hub:string;
  readonly manager:string;
  readonly pin:string;
  readonly endpoint:string;
  readonly serviceRef:string;
  readonly serviceName:string;
  readonly wifiOnly:boolean;
  constructor(client:ClientModel,serviceRef:string,serviceName:string,wifiOnly:boolean) {
    this.scope=client.scope();this.generation=client.connectionGeneration();this.profile=client.profile.id;
    this.hub=client.profile.hubId;this.manager=client.profile.manager;this.pin=client.profile.pin;this.endpoint=client.profile.baseUrl;
    this.serviceRef=serviceRef;this.serviceName=serviceName;this.wifiOnly=wifiOnly;
  }
  sameOwner(client:ClientModel):boolean {
    return !client.demo && client.owns(this.generation,this.scope) && this.profile===client.profile.id &&
      this.hub===client.profile.hubId && this.manager===client.profile.manager && this.pin===client.profile.pin && this.endpoint===client.profile.baseUrl;
  }
  matches(client:ClientModel):boolean {
    return this.sameOwner(client) && client.connected && ['meeting_conversation','meeting_live_view','meeting_transcription'].every((flag:string)=>client.features.includes(flag));
  }
}

/** Stored only in the private recording directory. A re-pair cannot authorize an older meeting's bytes. */
export interface MeetingAudioConsent {
  id:string;profileId:string;hubId:string;managerId:string;endpoint:string;pin:string;
  credentialKey:string;serviceRef:string;serviceName:string;wifiOnly:boolean;
}

export interface MeetingAudioPipe {
  connect(onMessage:(body:string)=>void,onState?:(state:string)=>void):Promise<void>;
  send(body:string):boolean;
  sendBinaryAsync(body:ArrayBuffer):Promise<void>;
  close():void;
}
export interface MeetingAudioIO {
  current:()=>boolean;
  // An empty network key means the current network cannot carry audio. Its identity changes when the rule or path changes.
  network:()=>string;
  // Fresh server capabilities/provider. A definitive refusal revokes further audio, a temporary read failure retries.
  authorize:()=>Promise<boolean>;
  pipe:(network:string)=>MeetingAudioPipe;
  pcm:(from:number,samples:number)=>Promise<ArrayBuffer>;
  live:(from:number)=>Promise<Fields>;
  timeline:()=>Fields;
  // On unrestricted paths the normal HTTP client can repair a stopped recording via the documented gap PUT.
  // Restricted paths use seek and binary on a network-bound audio socket instead.
  uploadGap?:(network:string,from:number,bytes:ArrayBuffer,admit:()=>boolean)=>Promise<Fields>;
  stopGap?:()=>void;
  retire?:()=>void;
}
class AudioRange {
  first:number;end:number;
  constructor(first:number,end:number) {this.first=first;this.end=end;}
}
function sample(raw:Fields,key:string):number {
  const at=Wire.number(raw,key,-1);return at>=0 && at<=12*3600 ? Math.round(at*rate) : -1;
}
function gapRanges(raw:Fields):AudioRange[] {
  const xs=raw['gaps'];if (!Array.isArray(xs)) return [];
  const result:AudioRange[]=[];
  for(const x of xs as Object[]) {
    if (!Array.isArray(x) || x.length!==2 || typeof x[0]!=='number' || typeof x[1]!=='number') continue;
    const a=Math.round(Number(x[0])*rate),b=Math.round(Number(x[1])*rate);
    if (Number.isSafeInteger(a)&&Number.isSafeInteger(b)&&a>=0&&b>a&&b<=maxMeetingSamples) result.push(new AudioRange(a,b));
  }
  return result;
}

/** A meeting's one audio socket. The recorder is the sole writer; back pressure and replays read its private PCM by sample. */
@ObservedV2
export class MeetingHubAudio {
  @Trace phase:string='device';
  @Trace caption:string='';
  @Trace latestLine:string='';
  @Trace problem:string='';
  @Trace ackSeconds:number=0;
  @Trace liveNext:number=0;
  private io:MeetingAudioIO|undefined;
  private socket:MeetingAudioPipe|undefined;
  private epoch:number=0;
  private socketEpoch:number=0;
  private networkKey:string='';
  private total:number=0;
  private next:number=0;
  private wireAt:number=0;
  private role:string='off';
  private speakingText:string='';
  private speakingAt:number=-1;
  private fallback:boolean=false;
  private stopped:boolean=false;
  private ready:boolean=false;
  private endedOnWire:boolean=false;
  private ackedMode:boolean=false;
  private connecting:boolean=false;
  private draining:boolean=false;
  private wake:boolean=false;
  private retry:number=-1;
  private delay:number=500;
  private gaps:AudioRange[]=[];
  private reading:boolean=false;
  private pollDue:number=0;
  private nextAuth:number=0;
  private restoreOnly:boolean=false;
  private repairUntil:number=0;

  begin(io:MeetingAudioIO,role:string):void {
    this.clear();this.io=io;this.role=role;this.phase='negotiating';
  }
  clear():void {
    this.epoch++;this.io?.stopGap?.();this.dropSocket();clearTimeout(this.retry);this.retry=-1;this.io=undefined;this.phase='device';
    this.caption='';this.latestLine='';this.problem='';this.ackSeconds=0;this.liveNext=0;this.total=0;this.next=0;
    this.stopped=false;this.ready=false;this.endedOnWire=false;this.ackedMode=false;this.connecting=false;this.draining=false;
    this.wake=false;this.fallback=false;this.role='off';this.speakingAt=-1;this.speakingText='';this.gaps=[];this.reading=false;this.pollDue=0;this.nextAuth=0;this.delay=500;this.restoreOnly=false;this.repairUntil=0;
  }
  private dropSocket():void {
    this.socketEpoch++;const socket=this.socket;this.socket=undefined;this.ready=false;this.networkKey='';if(socket)socket.close();
  }
  effective(response:Fields):void {
    if (!this.io || this.fallback || this.phase==='revoked' || this.phase==='final' || this.phase==='failed') return;
    const mode=Wire.text(response,'transcription');
    if (mode!=='hub') { this.fail(Wire.text(response,'transcription_problem') || 'device');return; }
    this.ackedMode=true;this.tick();
  }
  private fail(code:string):void {
    this.fallback=true;this.ackedMode=false;this.caption='';this.problem=code;this.phase='fallback';
    clearTimeout(this.retry);this.retry=-1;this.dropSocket();
    this.io?.stopGap?.();this.io?.retire?.();
  }
  withdraw():void {
    if (!this.io || this.phase==='device' || this.phase==='revoked') return;
    this.fail('withdrawn');this.phase='revoked';
  }
  recheck():void {
    if (!this.io || this.fallback || !this.ackedMode)return;
    this.dropSocket();this.connecting=false;this.nextAuth=0;this.phase='reconnecting';this.tick();
  }
  seconds():number {return this.total/rate;}
  usePhoneLines(role:string):boolean {
    return this.phase!=='live' || role==='off' || !!this.problem && this.problem!=='no_interim';
  }
  showCaption():boolean {return this.phase==='live' && !this.problem;}
  useDevice(reason:string):void {this.fail(reason);}
  captured(samples:number):void {
    if (!Number.isSafeInteger(samples) || samples<this.total || samples>maxMeetingSamples || this.stopped) return;
    this.total=samples;if(this.ackedMode)this.tick();
  }
  finish(samples:number):void {
    if (!this.io || this.stopped) return;
    this.captured(samples);this.stopped=true;this.repairUntil=Date.now()+1800000;if(this.ackedMode){this.phase='finishing';this.tick();}
  }
  resumeStopped(samples:number,endedAt:number):void {
    if(!this.io || !Number.isSafeInteger(samples) || samples<1 || samples>maxMeetingSamples)return;
    if(!Number.isFinite(endedAt) || endedAt<=0 || endedAt>Date.now()+60000 || endedAt+1800000<=Date.now()){this.fail('transcription_final');return;}
    this.total=samples;this.stopped=true;this.restoreOnly=true;this.endedOnWire=true;this.ackedMode=true;
    this.repairUntil=endedAt+1800000;
    this.phase='finishing';this.tick();
  }
  roleChanged(role:string):void {
    if (!['off','listener','participant'].includes(role)) return;
    this.role=role;if(this.ready)this.writeFrame('role',Wire.payload(['at','attendance'],[this.total/rate,role]));
  }
  voice(on:boolean,text:string):void {
    const at=this.total/rate;
    this.speakingAt=on ? at : -1;this.speakingText=on ? text : '';
    if(this.ready)this.writeFrame('speaking',Wire.payload(['on','at','text'],[on,at,on ? text : '']));
  }
  private owned(io:MeetingAudioIO,n:number):boolean { return this.io===io && this.epoch===n && !this.fallback && io.current(); }
  private route(io:MeetingAudioIO,n:number,s:number):boolean {
    return this.owned(io,n) && this.socketEpoch===s && !!this.socket && !!this.networkKey && io.network()===this.networkKey;
  }
  private writeFrame(kind:string,body:Fields):boolean {
    const io=this.io;if (!io || !this.route(io,this.epoch,this.socketEpoch))return false;
    body['type']=kind;return this.socket!.send(JSON.stringify(body));
  }
  private later(ms:number):void {
    if(this.retry!==-1 || !this.io || this.fallback || this.phase==='final' || this.phase==='failed')return;
    const rev=this.epoch;this.retry=setTimeout(()=>{this.retry=-1;if(this.epoch===rev)this.tick();},ms);
  }
  networkChanged():void { if(this.io && this.ackedMode){this.io.stopGap?.();this.tick();} }
  private tick():void {
    const io=this.io,n=this.epoch;if(!io || !this.ackedMode || this.fallback || this.phase==='final'||this.phase==='failed')return;
    if(!this.owned(io,n)){this.withdraw();return;}
    if(this.stopped && this.repairUntil>0 && Date.now()>this.repairUntil){this.fail('transcription_final');return;}
    const network=io.network();
    if(!network || this.socket && network!==this.networkKey){
      this.dropSocket();this.connecting=false;this.phase=network ? 'reconnecting' : 'waiting_network';
      if(!network){this.later(1000);return;}
    }
    if(this.endedOnWire && !this.socket){if(Date.now()>=this.pollDue)this.readLive();this.later(1500);return;}
    if(!this.socket && !this.connecting && this.retry===-1){this.open(io,n,network);return;}
    if(this.ready && this.socket){
      if(Date.now()>=this.nextAuth && !this.connecting){this.validate(io,n);return;}
      this.pump();
    }
  }
  private async validate(io:MeetingAudioIO,n:number):Promise<void>{
    this.connecting=true;this.nextAuth=Date.now()+15000;
    try{const allowed=await io.authorize();if(this.owned(io,n)&&!allowed)this.fail('provider_changed');}
    catch(_){if(this.owned(io,n)){this.dropSocket();this.phase='reconnecting';this.later(this.delay);}}
    finally{if(this.epoch===n){this.connecting=false;if(this.owned(io,n)&&this.ready)this.pump();}}
  }
  private async open(io:MeetingAudioIO,n:number,network:string):Promise<void> {
    const serial=++this.socketEpoch;this.connecting=true;this.phase=this.ackSeconds>0 ? 'reconnecting' : 'connecting';
    try{
      if(!await io.authorize()){if(this.owned(io,n)&&serial===this.socketEpoch)this.fail('provider_changed');return;}
      if(!this.owned(io,n)||serial!==this.socketEpoch||io.network()!==network)return;
      const pipe=io.pipe(network);this.socket=pipe;this.networkKey=network;this.ready=false;this.endedOnWire=false;
      let established=false;
      await pipe.connect((raw:string)=>this.frame(io,n,serial,raw),(state:string)=>{
        if(state==='ready'){established=true;return;}
        // An upgrade rejection carries the status on connect's promise. A generic close must not discard it.
        if(!established || !this.owned(io,n)||this.socketEpoch!==serial)return;
        this.disconnect(io,n,serial);
      });
      established=true;
      if(!this.route(io,n,serial))return;
      this.nextAuth=Date.now()+15000;
      if(!this.writeFrame('start',Wire.payload(['at','timeline'],[Math.max(0,this.total-ringSamples)/rate,io.timeline()])))throw new Error('socket_closed');
      if(this.speakingAt>=0)this.writeFrame('speaking',Wire.payload(['at','on','text'],[this.speakingAt,true,this.speakingText]));
    }catch(error){
      if(this.owned(io,n)&&this.socketEpoch===serial){
        const code=(error as Error).message||'';
        if(code==='audio_network_unverified'){this.dropSocket();this.connecting=false;this.phase='waiting_network';this.problem=code;this.later(3000);return;}
        if ([401,403,404,410].includes(Wire.number(error as Object as Fields,'status'))) this.fail('audio_forbidden');
        else this.disconnect(io,n,serial);
      }
    }finally{if(this.epoch===n && this.socketEpoch===serial){this.connecting=false;if(this.ready)this.pump();}}
  }
  private disconnect(io:MeetingAudioIO,n:number,s:number):void {
    if(!this.owned(io,n)||this.socketEpoch!==s)return;
    const ended=this.endedOnWire;this.dropSocket();this.connecting=false;this.caption='';
    this.phase=ended ? 'finishing':'reconnecting';this.later(ended ? 200:this.delay);
    if(!ended)this.delay=Math.min(15000,this.delay*2);
  }
  private frame(io:MeetingAudioIO,n:number,s:number,raw:string):void {
    if(!this.route(io,n,s)||raw.length>2*1024*1024)return;
    const data=Wire.parse(raw),type=Wire.text(data,'type');
    if(type==='ready'){
      if(this.ready)return;
      const from=sample(data,'from'),next=Wire.number(data,'next',-1);
      if(from<0||from>this.total||next<0||!Number.isInteger(next)){this.fail('bad_frame');return;}
      this.wireAt=from;this.next=this.restoreOnly ? this.total:Math.max(from,this.stopped ? 0 : this.total-ringSamples);
      this.gaps=gapRanges(data).filter((g:AudioRange)=>g.first<this.total);
      if(this.next>from && !this.restoreOnly)this.gaps.push(new AudioRange(from,this.next));
      this.ready=true;this.delay=500;this.problem='';this.ackSeconds=from/rate;this.phase=this.stopped ? 'finishing':'live';
      this.writeFrame('role',Wire.payload(['at','attendance'],[this.total/rate,this.role]));
      if(next>this.liveNext)this.readLive();this.pump();return;
    }
    if(type==='error' && Wire.bool(data,'fatal')){
      const code=Wire.text(data,'code')||'unknown';
      if(code==='transcription_final'){
        this.endedOnWire=true;this.disconnect(io,n,s);this.readLive();
      }else this.fail(code);
      return;
    }
    if(type==='problem'){this.problem=Wire.text(data,'code');if(this.problem)this.caption='';return;}
    if(!this.ready)return;
    if(type==='ack'){const at=sample(data,'at');if(at>=0&&at<=this.total)this.ackSeconds=at/rate;}
    if(type==='interim' && !this.problem)this.caption=Wire.text(data,'text');
    if(type==='line'){
      const position=Wire.number(data,'n',-1),t=Wire.text(data,'text');
      if(t && Number.isInteger(position) && position>=this.liveNext){
        if(position>this.liveNext)this.readLive();
        else {this.latestLine=t;this.liveNext=position+1;this.caption='';}
      }
    }
  }
  private async readLive():Promise<void> {
    const io=this.io,n=this.epoch;if(!io||this.reading||!this.owned(io,n))return;
    this.reading=true;this.pollDue=Date.now()+1200;
    try{
      const info=await io.live(this.liveNext);
      if(!this.owned(io,n))return;
      const block=Wire.object(info,'transcription'),state=Wire.text(block,'state'),lines=Wire.rows(info,'lines');
      for (const line of lines) {
        const at=Wire.number(line,'n',-1);
        if(at!==this.liveNext)continue;
        this.latestLine=Wire.text(line,'text')||this.latestLine;this.liveNext++;
      }
      if(state==='final'||state==='failed'){
        this.phase=state;this.caption='';this.dropSocket();clearTimeout(this.retry);this.retry=-1;io.retire?.();
      }else if(this.stopped && !this.socket && state==='finishing' && Wire.text(block,'mode')==='hub'){
        const gaps=gapRanges(block).filter((gap:AudioRange)=>gap.first<this.total);
        if(gaps.length)await this.repair(io,n,gaps[0]);
      }else if(this.restoreOnly && Wire.text(block,'mode')==='device'){this.fail('device');}
    }catch(_){}finally{if(this.epoch===n)this.reading=false;}
  }
  private async repair(io:MeetingAudioIO,n:number,gap:AudioRange):Promise<void>{
    const key=io.network();
    if(!key || !this.owned(io,n))return;
    if(!await io.authorize()){
      if(this.owned(io,n))this.fail('provider_changed');
      return;
    }
    const admit=():boolean=>this.owned(io,n) && io.network()===key;
    if(!admit())return;
    if(key.startsWith('wifi:') || !io.uploadGap){
      // RCP cannot bind an HTTP request to an unmetered path. The official audio socket also accepts earlier ranges.
      this.restoreOnly=true;this.endedOnWire=false;await this.open(io,n,key);return;
    }
    const take=Math.min(maxChunk,this.total-gap.first,gap.end-gap.first);
    const bytes=await io.pcm(gap.first,take);
    if(!admit() || bytes.byteLength!==take*bytesPerSample)return;
    const result=await io.uploadGap(key,gap.first,bytes,admit);
    if(!admit() || !Array.isArray(result['gaps']))return;
    this.pollDue=0;
    clearTimeout(this.retry);this.retry=-1;this.later(30);
  }
  private async pump():Promise<void> {
    if(this.draining){this.wake=true;return;}
    const n=this.epoch,s=this.socketEpoch,io=this.io,pipe=this.socket;
    if(!io||!pipe||!this.ready||this.endedOnWire||this.connecting||!this.route(io,n,s))return;
    this.draining=true;
    try{
      do{
        this.wake=false;
        // Earlier missing spans are valid WebSocket seek+binary frames. Replaying them on this same bound socket
        // also keeps Wi-Fi-only uploads on the OS network selected for the live stream.
        const gap=this.stopped ? this.gaps[0] : undefined,at=gap ? gap.first:this.next,last=gap ? Math.min(this.total,gap.end):this.total;
        if(at>=last){
          if(gap){this.gaps.shift();this.wake=true;continue;}
          if(this.stopped){
            if(!this.writeFrame('end',Wire.payload(['at'],[this.total/rate])))throw new Error('socket_closed');
            this.endedOnWire=true;this.phase='finishing';this.later(1500);
          }
          break;
        }
        const take=Math.min(last-at,(gap || last-at>4*liveChunk) ? maxChunk:liveChunk);
        const bytes=await io.pcm(at,take);
        if(!this.route(io,n,s))break;
        if(bytes.byteLength!==take*bytesPerSample)throw new Error('local_audio');
        if(this.wireAt!==at && !this.writeFrame('seek',Wire.payload(['at'],[at/rate])))throw new Error('socket_closed');
        await pipe.sendBinaryAsync(bytes);if(!this.route(io,n,s))break;
        this.wireAt=at+take;
        if(gap){gap.first=at+take;if(gap.first>=gap.end)this.gaps.shift();}else this.next=at+take;
        this.wake=true;
      }while(this.wake && this.route(io,n,s));
    }catch(_){if(this.route(io,n,s))this.disconnect(io,n,s);}
    finally{if(this.epoch===n){this.draining=false;if(this.socketEpoch!==s && this.ready)this.pump();}}
  }
}
