import { Wire } from './Wire';

export interface MeetingRoleChange {at:number;attendance:string;}
export interface MeetingLine { start:number; end:number; text:string; }
export interface MeetingAsk { id:string; question:string; at:number; liveNext:number; state:string; answer:string; spoken?:boolean; speechStart?:number; speechEnd?:number; }
export interface MeetingJournal {
  id:string; profileId:string; hubId:string; managerId:string; startedAt:string; title:string; language:string;
  role:string; privateMark:boolean; revision:number; opened:boolean; endedAt:string; endRevision:number; ended:boolean;
  next:number; lines:MeetingLine[]; asks:MeetingAsk[];
  transcription?:string;effectiveTranscription?:string;roles?:MeetingRoleChange[];syncedLines?:number;
}
export interface MeetingRoomIO {
  current:()=>boolean;
  audioMode?:(ack:Record<string,Object>)=>void;
  audioRole?:(role:string)=>void;
  audioSpeaking?:(on:boolean,text:string)=>void;
  audioClock?:()=>number;
  speak?:(id:string,text:string,lang:string)=>Promise<void>;
  save:(journal:MeetingJournal)=>Promise<void>;
  request:(path:string,method:string,body?:Record<string,Object>)=>Promise<Record<string,Object>>;
}

function boundedText(raw:string,maxBytes:number):string {
  let result='',bytes=0;
  for (const symbol of raw.trim()) {
    const c=symbol.codePointAt(0) || 0, size=c<128 ? 1 : c<2048 ? 2 : c<65536 ? 3 : 4;
    if (bytes+size>maxBytes) break;
    result+=symbol; bytes+=size;
  }
  return result;
}

export function newMeetingJournal(id:string,profile:string,hub:string,manager:string,started:string,title:string,
  language:string,role:string,privateMark:boolean,transcription:string=''):MeetingJournal {
  const value:MeetingJournal={id:id,profileId:profile,hubId:hub,managerId:manager,startedAt:started,title:title,language:language,
    role:role,privateMark:privateMark,revision:Math.max(1,Date.parse(started)),opened:false,endedAt:'',endRevision:0,
    ended:false,next:0,lines:[],asks:[]};
  if(transcription==='hub'){value.transcription='hub';value.roles=[{at:0,attendance:role}];}
  return value;
}

/** The room transcript advances only with the Hub's acknowledged offset. A retry reuses exactly the same slot and ask ID. */
@ObservedV2
export class MeetingRoom {
  @Trace state:string='';
  @Trace problem:string='';
  @Trace latest:string='';
  @Trace shownAsks:MeetingAsk[]=[];
  @Trace liveNext:number=0;
  @Trace active:boolean=false;
  @Trace sending:boolean=false;
  private journal:MeetingJournal|undefined;
  private io:MeetingRoomIO|undefined;
  private generation:number=0;
  private writes:Promise<void>=Promise.resolve();
  private running:Promise<void>|undefined;
  private timer:number=-1;
  private retryMs:number=1000;
  private wake:boolean=false;
  private textBytes:number=0;
  private localAsks:Set<string>=new Set<string>();

  detach():void {
    this.generation++; clearTimeout(this.timer); this.timer=-1; this.running=undefined; this.io=undefined; this.journal=undefined;
    this.state=''; this.problem=''; this.latest=''; this.shownAsks=[]; this.liveNext=0; this.active=false; this.sending=false;
    this.wake=false; this.retryMs=1000; this.textBytes=0; this.localAsks.clear();
  }
  attach(journal:MeetingJournal,io:MeetingRoomIO):void {
    this.detach();
    this.journal=journal; this.io=io; this.active=true; this.state=journal.ended ? 'ended' : 'starting';
    this.textBytes=journal.lines.reduce((sum:number,line:MeetingLine) => sum+this.bytes(line.text),0);
    this.show();
  }
  private bytes(text:string):number {
    let bytes=0;
    for (const symbol of text) { const c=symbol.codePointAt(0)||0; bytes+=c<128 ? 1 : c<2048 ? 2 : c<65536 ? 3 : 4; }
    return bytes;
  }
  private show():void {
    const s=this.journal;
    if (!s) return;
    this.latest=s.lines.length ? s.lines[s.lines.length-1].text : '';
    this.shownAsks=s.asks.map((a:MeetingAsk):MeetingAsk => ({id:a.id,question:a.question,at:a.at,liveNext:a.liveNext,state:a.state,answer:a.answer}));
    this.liveNext=s.next;
  }
  private owns(io:MeetingRoomIO,n:number):boolean { return n===this.generation && this.io===io && io.current(); }
  private async persist(io:MeetingRoomIO,n:number):Promise<void> {
    if (!this.journal || !this.owns(io,n)) throw new Error('request_updated');
    const saved=JSON.parse(JSON.stringify(this.journal)) as MeetingJournal;
    const op=this.writes.catch(() => {}).then(async ():Promise<void> => {
      if (!this.owns(io,n)) throw new Error('request_updated');
      await io.save(saved);
    });
    this.writes=op;
    await op;
  }
  async line(start:number,end:number,text:string):Promise<void> {
    const s=this.journal,io=this.io,n=this.generation;
    if (!s || !io || !this.owns(io,n) || s.endedAt || !Number.isFinite(start) || !Number.isFinite(end)) return;
    const value=boundedText(text,4000),bytes=this.bytes(value);
    if (!value) return;
    if (s.lines.length>=20000 || this.textBytes+bytes>4*1024*1024) { this.problem='phone_meeting_full'; return; }
    const from=Math.max(0,start);
    s.lines.push({start:from,end:Math.max(from,end),text:value}); this.textBytes+=bytes; this.show();
    try { await this.persist(io,n); } catch (_) { if (this.owns(io,n)) this.problem='knowledge_storage_unavailable'; return; }
    if (this.owns(io,n)) await this.sync();
  }
  async ask(id:string,question:string,at:number):Promise<boolean> {
    const s=this.journal,io=this.io,n=this.generation,value=boundedText(question,4000);
    if (!s || !io || !this.owns(io,n) || s.endedAt || !value || !id || s.asks.some((a:MeetingAsk) => a.id===id)) return false;
    this.localAsks.add(id);
    s.asks.push({id:id,question:value,at:Math.max(0,at),liveNext:s.effectiveTranscription==='hub' ? s.next:s.lines.length,state:'queued',answer:''}); this.show();
    try { await this.persist(io,n); } catch (_) { if (this.owns(io,n)) this.problem='knowledge_storage_unavailable'; return false; }
    await this.sync(); return this.owns(io,n);
  }
  async changeSpeaker(on:boolean):Promise<boolean> {
    const s=this.journal,io=this.io,n=this.generation,role=on ? 'participant' : 'listener';
    if (!s || !io || !this.owns(io,n) || s.endedAt || !['listener','participant'].includes(s.role)) return false;
    if (s.role!==role) {
      s.role=role; if(s.transcription==='hub' && s.roles && s.roles.length<100){s.roles.push({at:io.audioClock?.() || 0,attendance:role});io.audioRole?.(role);}
      s.revision=Math.max(Date.now(),s.revision+1); s.opened=false;
    }
    await this.sync();
    return this.owns(io,n) && s.role===role && s.opened;
  }
  async finish(endedAt:string):Promise<void> {
    const s=this.journal,io=this.io,n=this.generation;
    if (!s || !io || !this.owns(io,n) || !endedAt || s.ended) return;
    if (!s.endedAt) { s.endedAt=endedAt; s.endRevision=Math.max(s.revision+1,Date.parse(endedAt)); }
    try { await this.persist(io,n); } catch (_) { if (this.owns(io,n)) this.problem='knowledge_storage_unavailable'; return; }
    if (this.owns(io,n)) await this.sync();
  }
  private upload(s:MeetingJournal,ending:boolean):Record<string,Object> {
    const room=Wire.payload(['attendance'],[s.role]);
    const body=Wire.payload(['revision','started_at','duration','language','device','title','bookmarks','segments','complete','kind','room','private'],
      [ending ? s.endRevision : s.revision,s.startedAt,ending ? Math.max(0,(Date.parse(s.endedAt)-Date.parse(s.startedAt))/1000) : 0,
       s.language,'phone',s.title,[],ending || s.effectiveTranscription==='hub' ? s.lines : [],ending,'meeting',room,s.privateMark]);
    if(s.transcription==='hub'){
      body['transcription']=s.effectiveTranscription==='device' ? 'device':'hub';body['timeline']=this.audioTimeline();
      body['duration']=Math.max(Wire.number(body,'duration'),this.io?.audioClock?.() || 0);
    }
    if (ending) body['ended_at']=s.endedAt;
    const spoken=this.spokenLines(); if (spoken.length) body['spoken']=spoken;
    return body;
  }
  audioTimeline():Record<string,Object> {
    const s=this.journal;
    return Wire.payload(['roles','speaking'],[s?.roles || [],this.spokenLines().slice(0,1000)]);
  }
  private modeAck(s:MeetingJournal,io:MeetingRoomIO,ack:Record<string,Object>):void {
    if(s.transcription!=='hub')return;
    if(s.effectiveTranscription!=='device')s.effectiveTranscription=Wire.text(ack,'transcription')==='hub' ? 'hub':'device';
    if(s.effectiveTranscription==='device')io.audioMode?.(Wire.payload(['transcription','transcription_problem'],['device',Wire.text(ack,'transcription_problem')||'device']));
    else io.audioMode?.(ack);
  }
  spokenLines():MeetingLine[] {
    const list=this.journal?.asks || [];
    return list.filter((ask:MeetingAsk) => ask.speechStart!==undefined && ask.speechEnd!==undefined && ask.speechEnd>ask.speechStart)
      .map((ask:MeetingAsk):MeetingLine => ({start:ask.speechStart!,end:ask.speechEnd!,text:ask.answer}));
  }
  private async reply(ask:MeetingAsk,result:Record<string,Object>,s:MeetingJournal,io:MeetingRoomIO,n:number):Promise<void> {
    ask.state=Wire.text(result,'state',ask.state); ask.answer=Wire.text(result,'answer',ask.answer);
    this.show();
    const age=Date.now()-Date.parse(s.startedAt)-ask.at*1000;
    if (ask.state!=='answered' || !ask.answer || s.role!=='participant' || !Wire.bool(result,'speak') ||
      !this.localAsks.has(ask.id) || age>60000 || ask.spoken || s.endedAt || !io.speak) return;
    ask.spoken=true;
    await this.persist(io,n);
    if (!this.owns(io,n) || s.endedAt) return;
    ask.speechStart=io.audioClock ? io.audioClock() : Math.max(0,(Date.now()-Date.parse(s.startedAt))/1000);
    io.audioSpeaking?.(true,ask.answer);
    try {
      await io.speak(ask.id,ask.answer,Wire.text(result,'lang',s.language));
    } catch (_) { if (this.owns(io,n)) this.problem='phone_meeting_voice_unavailable'; }
    finally {
      if (this.owns(io,n)) {
        ask.speechEnd=Math.max(ask.speechStart,io.audioClock ? io.audioClock() : (Date.now()-Date.parse(s.startedAt))/1000);
        io.audioSpeaking?.(false,'');
      }
    }
  }
  async sync():Promise<void> {
    const s=this.journal,io=this.io,n=this.generation;
    if (!s || !io || !this.owns(io,n)) return;
    this.wake=true;
    if (this.running) { await this.running; return; }
    clearTimeout(this.timer); this.timer=-1;
    const run=this.drain(s,io,n); this.running=run;
    try { await run; } finally { if (this.running===run) this.running=undefined; }
  }
  private retry(delay:number,io:MeetingRoomIO,n:number):void {
    if (!this.owns(io,n)) return;
    clearTimeout(this.timer);
    this.timer=setTimeout(() => { if (this.owns(io,n)) this.sync().catch(() => {}); },delay);
  }
  private async drain(s:MeetingJournal,io:MeetingRoomIO,n:number):Promise<void> {
    this.sending=true;
    const path='/api/v1/recordings/'+encodeURIComponent(s.id);
    try {
      do {
        this.wake=false;
        await this.persist(io,n);
        if (!this.owns(io,n)) return;
        if (!s.opened) {
          const requestedRevision=s.revision;
          const ack=await io.request(path,'PUT',this.upload(s,false));
          if (!this.owns(io,n)) return;
          if (s.revision!==requestedRevision) { this.wake=true; continue; }
          if (Wire.text(ack,'id')!==s.id || Wire.text(ack,'status')==='expired' ||
            Wire.text(ack,'attendance')!==s.role || !Object.keys(Wire.object(ack,'room')).length) {
            this.problem='phone_meeting_unavailable'; this.state='unavailable'; return;
          }
          this.modeAck(s,io,ack);this.state=Wire.text(Wire.object(ack,'room'),'state','starting'); s.opened=true;
          await this.persist(io,n);
        }
        if (!this.owns(io,n)) return;
        if (!s.opened) { this.wake=true; continue; }
        // In hub mode the manager owns live-log indices. Local recognition fills the recording upload's segments.
        if(s.effectiveTranscription==='hub' && !s.endedAt && (s.syncedLines || 0)<s.lines.length){
          const count=s.lines.length;s.revision=Math.max(Date.now(),s.revision+1);
          const ack=await io.request(path,'PUT',this.upload(s,false));
          if(!this.owns(io,n))return;
          if(Wire.text(ack,'id')!==s.id)throw new Error('request_updated');
          this.modeAck(s,io,ack);s.syncedLines=count;await this.persist(io,n);
        }
        const next=s.next, batch=s.effectiveTranscription==='hub' ? []:s.lines.slice(next,next+64);
        if (batch.length && !s.ended) {
          const ack=await io.request(path+'/live','POST',Wire.payload(['from','segments'],[next,batch]));
          if (!this.owns(io,n)) return;
          const acknowledged=Wire.number(ack,'next',-1);
          if (acknowledged<next+batch.length || acknowledged>s.lines.length) throw new Error('request_updated');
          s.next=acknowledged;
          await this.persist(io,n); this.show(); this.wake=true; continue;
        }
        for (const ask of s.asks) {
          if (!s.opened) break;
          if (!this.owns(io,n)) return;
          if (ask.state!=='queued' || ask.liveNext>s.next || s.ended) continue;
          const reply=await io.request(path+'/ask','POST',Wire.payload(['id','question','at','live_next'],[ask.id,ask.question,ask.at,ask.liveNext]));
          if (!this.owns(io,n)) return;
          const resolved=Wire.object(reply,'ask');
          if (Wire.text(resolved,'id')!==ask.id) throw new Error('request_updated');
          await this.reply(ask,resolved,s,io,n); await this.persist(io,n);
        }
        if (!this.owns(io,n)) return;
        if (!s.opened) { this.wake=true; continue; }
        if (s.endedAt && !s.ended) {
          const ack=await io.request(path,'PUT',this.upload(s,true));
          if (!this.owns(io,n)) return;
          if (Wire.text(ack,'id')!==s.id || !Object.keys(Wire.object(ack,'room')).length) {
            this.state='unavailable'; this.problem='phone_meeting_unavailable'; return;
          }
          this.modeAck(s,io,ack);s.ended=true; this.state='ended'; await this.persist(io,n);
        }
        if (!this.owns(io,n)) return;
        const live=await io.request(path+'/live','GET');
        if (!this.owns(io,n)) return;
        const remoteNext=Wire.number(live,'next',-1);
        if (remoteNext<0 || s.effectiveTranscription!=='hub' && remoteNext>s.lines.length) throw new Error('request_updated');
        if (!s.ended && remoteNext!==s.next) { s.next=remoteNext; this.wake=s.effectiveTranscription!=='hub'; }
        const remoteAsks=Wire.rows(live,'asks');
        for (const ask of s.asks) {
          const result=remoteAsks.find((v:Record<string,Object>) => Wire.text(v,'id')===ask.id);
          if (result) await this.reply(ask,result,s,io,n);
        }
        this.state=s.ended ? 'ended' : Wire.text(Wire.object(live,'room'),'state',this.state);
        if (this.problem!=='phone_meeting_voice_unavailable')
          this.problem=Wire.text(live,'problem') || Wire.text(Wire.object(live,'room'),'problem') ? 'phone_meeting_unavailable' : '';
        this.show(); await this.persist(io,n); this.retryMs=1000;
      } while (this.wake && this.owns(io,n));
      if (!s.ended || s.asks.some((a:MeetingAsk) => a.state==='pending')) this.retry(5000,io,n);
    } catch (failure) {
      if (!this.owns(io,n)) return;
      const error=failure as Object as Record<string,Object>, code=Wire.text(error,'code'), status=Wire.number(error,'status');
      if (status===410 || status===403 || status===404 && code!=='recording_not_found' || code==='live_full') {
        this.state='unavailable'; this.problem=code==='live_full' ? 'phone_meeting_full' : 'phone_meeting_unavailable'; return;
      }
      if (code==='recording_not_found') s.opened=false;
      if (code==='live_gap') {
        const live=await io.request(path+'/live','GET').catch(():Record<string,Object> => ({}));
        if (!this.owns(io,n)) return;
        const next=Wire.number(live,'next',-1);
        if (next>=0 && next<=s.lines.length) s.next=next;
      }
      this.problem='phone_meeting_retry'; this.retry(this.retryMs,io,n); this.retryMs=Math.min(30000,this.retryMs*2);
    } finally { if (this.generation===n) this.sending=false; }
  }
}
